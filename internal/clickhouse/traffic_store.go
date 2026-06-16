package clickhouse

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Top-N row kinds, stored in traffic_top.kind.
const (
	kindSrcIP   uint8 = 0
	kindDstIP   uint8 = 1
	kindDstPort uint8 = 2
)

// resolveTTL caps how long an asn→peer_id resolution is cached, to avoid hitting
// Postgres for every interface of every report.
const resolveTTL = 30 * time.Second

// ── Wire payload (decoded straight from the agent's traffic.report) ──────────

// Report is one traffic.report websocket message.
type Report struct {
	NodeID      string            `json:"node_id"`
	WindowStart string            `json:"window_start"`
	WindowEnd   string            `json:"window_end"`
	SampleRatio float64           `json:"sample_ratio"`
	Interfaces  []InterfaceReport `json:"interfaces"`
}

type InterfaceReport struct {
	ASN            int64                 `json:"asn"`
	Interface      string                `json:"interface"`
	PeerID         string                `json:"peer_id"`
	SampledPackets uint64                `json:"sampled_packets"`
	SampledBytes   uint64                `json:"sampled_bytes"`
	V4Packets      uint64                `json:"v4_packets"`
	V6Packets      uint64                `json:"v6_packets"`
	V4Bytes        uint64                `json:"v4_bytes"`
	V6Bytes        uint64                `json:"v6_bytes"`
	Proto          map[string]ProtoCount `json:"proto"`
	SizeBuckets    map[string]uint64     `json:"size_buckets"`
	TopSrc         []TopAddr             `json:"top_src"`
	TopDst         []TopAddr             `json:"top_dst"`
	TopPorts       []TopPort             `json:"top_ports"`
}

type ProtoCount struct {
	Pkts  uint64 `json:"pkts"`
	Bytes uint64 `json:"bytes"`
}

type TopAddr struct {
	Addr  string `json:"addr"`
	Pkts  uint64 `json:"pkts"`
	Bytes uint64 `json:"bytes"`
}

type TopPort struct {
	Port  uint16 `json:"port"`
	Pkts  uint64 `json:"pkts"`
	Bytes uint64 `json:"bytes"`
}

// ── Query result shapes ──────────────────────────────────────────────────────

type TrafficPoint struct {
	Time           time.Time
	SampledBytes   uint64
	SampledPackets uint64
	V4Bytes        uint64
	V6Bytes        uint64
	ProtoBytes     map[string]uint64
	ProtoPkts      map[string]uint64
	SizeBuckets    map[string]uint64
}

type TopRow struct {
	Label string
	Pkts  uint64
	Bytes uint64
}

type TrafficSeries struct {
	Points      []TrafficPoint
	TopSrc      []TopRow
	TopDst      []TopRow
	TopPorts    []TopRow
	SampleRatio float64
}

// PeerResolver maps an authenticated (node_id, asn) to a peers.id, or "" when no
// matching peer exists (e.g. peer already deleted — the sample is still stored
// under the node with an empty peer_id).
type PeerResolver func(ctx context.Context, nodeID string, asn int64) (string, error)

type resolveKey struct {
	nodeID string
	asn    int64
}

type resolveEntry struct {
	peerID string
	at     time.Time
}

// TrafficStore writes agent reports to and serves analytics queries from
// ClickHouse. A nil *TrafficStore is the single "feature disabled" signal used
// across center (WS drop / API 503 / stats flag).
type TrafficStore struct {
	conn    driver.Conn
	resolve PeerResolver

	cacheMu sync.Mutex
	cache   map[resolveKey]resolveEntry
}

func NewTrafficStore(conn driver.Conn, resolve PeerResolver) *TrafficStore {
	return &TrafficStore{
		conn:    conn,
		resolve: resolve,
		cache:   make(map[resolveKey]resolveEntry),
	}
}

func (s *TrafficStore) resolvePeerID(ctx context.Context, nodeID string, asn int64) string {
	key := resolveKey{nodeID: nodeID, asn: asn}
	s.cacheMu.Lock()
	if e, ok := s.cache[key]; ok && time.Since(e.at) < resolveTTL {
		s.cacheMu.Unlock()
		return e.peerID
	}
	s.cacheMu.Unlock()

	if s.resolve == nil {
		return ""
	}
	peerID, err := s.resolve(ctx, nodeID, asn)
	if err != nil {
		// Do NOT negative-cache a transient resolver/DB error: the repository
		// returns ("", nil) for a genuinely-absent peer and ("", err) only on a
		// real failure. Caching "" on error would mis-attribute a live peer's
		// samples to peer_id="" for the whole TTL.
		return ""
	}

	s.cacheMu.Lock()
	s.cache[key] = resolveEntry{peerID: peerID, at: time.Now()}
	s.cacheMu.Unlock()
	return peerID
}

// InsertReport batch-inserts one report's interfaces into traffic_samples and
// their Top-N entries into traffic_top. nodeID is the authenticated connection's
// node (the payload's node_id is ignored). The window's end time is used as the
// row timestamp.
func (s *TrafficStore) InsertReport(ctx context.Context, nodeID string, r *Report) error {
	if r == nil || len(r.Interfaces) == 0 {
		return nil
	}
	ts := parseWindowTime(r.WindowEnd)

	samplesBatch, err := s.conn.PrepareBatch(ctx, `INSERT INTO traffic_samples (
		time, node_id, peer_id, asn, sample_ratio,
		sampled_packets, sampled_bytes, v4_packets, v6_packets, v4_bytes, v6_bytes,
		proto_pkts, proto_bytes, size_buckets)`)
	if err != nil {
		return fmt.Errorf("prepare traffic_samples batch: %w", err)
	}
	topBatch, err := s.conn.PrepareBatch(ctx, `INSERT INTO traffic_top (
		time, node_id, peer_id, asn, kind, label, pkts, bytes)`)
	if err != nil {
		_ = samplesBatch.Abort()
		return fmt.Errorf("prepare traffic_top batch: %w", err)
	}

	for i := range r.Interfaces {
		iface := &r.Interfaces[i]
		peerID := s.resolvePeerID(ctx, nodeID, iface.ASN)
		asn := uint32(iface.ASN)
		protoPkts, protoBytes := splitProto(iface.Proto)
		sizeBuckets := iface.SizeBuckets
		if sizeBuckets == nil {
			sizeBuckets = map[string]uint64{}
		}

		if err := samplesBatch.Append(
			ts, nodeID, peerID, asn, r.SampleRatio,
			iface.SampledPackets, iface.SampledBytes,
			iface.V4Packets, iface.V6Packets, iface.V4Bytes, iface.V6Bytes,
			protoPkts, protoBytes, sizeBuckets,
		); err != nil {
			_ = samplesBatch.Abort()
			_ = topBatch.Abort()
			return fmt.Errorf("append traffic_samples row: %w", err)
		}

		for _, t := range iface.TopSrc {
			if err := topBatch.Append(ts, nodeID, peerID, asn, kindSrcIP, t.Addr, t.Pkts, t.Bytes); err != nil {
				_ = samplesBatch.Abort()
				_ = topBatch.Abort()
				return fmt.Errorf("append traffic_top src row: %w", err)
			}
		}
		for _, t := range iface.TopDst {
			if err := topBatch.Append(ts, nodeID, peerID, asn, kindDstIP, t.Addr, t.Pkts, t.Bytes); err != nil {
				_ = samplesBatch.Abort()
				_ = topBatch.Abort()
				return fmt.Errorf("append traffic_top dst row: %w", err)
			}
		}
		for _, t := range iface.TopPorts {
			label := strconv.FormatUint(uint64(t.Port), 10)
			if err := topBatch.Append(ts, nodeID, peerID, asn, kindDstPort, label, t.Pkts, t.Bytes); err != nil {
				_ = samplesBatch.Abort()
				_ = topBatch.Abort()
				return fmt.Errorf("append traffic_top port row: %w", err)
			}
		}
	}

	if err := samplesBatch.Send(); err != nil {
		_ = topBatch.Abort()
		return fmt.Errorf("send traffic_samples batch: %w", err)
	}
	if err := topBatch.Send(); err != nil {
		return fmt.Errorf("send traffic_top batch: %w", err)
	}
	return nil
}

// PeerSeries returns the per-interface time series plus that interface's Top-N
// tables over the last `hours` hours.
func (s *TrafficStore) PeerSeries(ctx context.Context, peerID string, hours, top int) (*TrafficSeries, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT time, sampled_bytes, sampled_packets, v4_bytes, v6_bytes,
		       proto_bytes, proto_pkts, size_buckets
		FROM traffic_samples
		WHERE peer_id = ? AND time > now() - toIntervalHour(?)
		ORDER BY time`, peerID, hours)
	if err != nil {
		return nil, err
	}
	points, err := scanPoints(rows)
	if err != nil {
		return nil, err
	}
	return s.assembleSeries(ctx, "peer_id", peerID, hours, top, points)
}

// NodeSeries returns the node-aggregated time series (bucketed to `bucketMinutes`)
// plus node-level Top-N tables over the last `hours` hours.
func (s *TrafficStore) NodeSeries(ctx context.Context, nodeID string, hours, bucketMinutes, top int) (*TrafficSeries, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT toStartOfInterval(time, toIntervalMinute(?)) AS b,
		       sum(sampled_bytes), sum(sampled_packets), sum(v4_bytes), sum(v6_bytes),
		       sumMap(proto_bytes), sumMap(proto_pkts), sumMap(size_buckets)
		FROM traffic_samples
		WHERE node_id = ? AND time > now() - toIntervalHour(?)
		GROUP BY b ORDER BY b`, bucketMinutes, nodeID, hours)
	if err != nil {
		return nil, err
	}
	points, err := scanPoints(rows)
	if err != nil {
		return nil, err
	}
	return s.assembleSeries(ctx, "node_id", nodeID, hours, top, points)
}

func (s *TrafficStore) assembleSeries(ctx context.Context, col, id string, hours, top int, points []TrafficPoint) (*TrafficSeries, error) {
	topSrc, err := s.queryTop(ctx, col, id, kindSrcIP, hours, top)
	if err != nil {
		return nil, err
	}
	topDst, err := s.queryTop(ctx, col, id, kindDstIP, hours, top)
	if err != nil {
		return nil, err
	}
	topPorts, err := s.queryTop(ctx, col, id, kindDstPort, hours, top)
	if err != nil {
		return nil, err
	}
	return &TrafficSeries{
		Points:      points,
		TopSrc:      topSrc,
		TopDst:      topDst,
		TopPorts:    topPorts,
		SampleRatio: s.latestSampleRatio(ctx, col, id, hours),
	}, nil
}

func scanPoints(rows driver.Rows) ([]TrafficPoint, error) {
	defer rows.Close()
	points := make([]TrafficPoint, 0)
	for rows.Next() {
		var p TrafficPoint
		if err := rows.Scan(
			&p.Time, &p.SampledBytes, &p.SampledPackets, &p.V4Bytes, &p.V6Bytes,
			&p.ProtoBytes, &p.ProtoPkts, &p.SizeBuckets,
		); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}

// queryTop runs the node- or peer-scoped Top-N aggregation. col is an internal
// constant ("peer_id" | "node_id"), never user input, so it is safe to inline.
func (s *TrafficStore) queryTop(ctx context.Context, col, id string, kind uint8, hours, limit int) ([]TopRow, error) {
	query := fmt.Sprintf(`
		SELECT label, sum(pkts) AS p, sum(bytes) AS b
		FROM traffic_top
		WHERE %s = ? AND kind = ? AND time > now() - toIntervalHour(?)
		GROUP BY label ORDER BY b DESC LIMIT ?`, col)
	rows, err := s.conn.Query(ctx, query, id, kind, hours, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TopRow, 0, limit)
	for rows.Next() {
		var r TopRow
		if err := rows.Scan(&r.Label, &r.Pkts, &r.Bytes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *TrafficStore) latestSampleRatio(ctx context.Context, col, id string, hours int) float64 {
	query := fmt.Sprintf(`
		SELECT sample_ratio FROM traffic_samples
		WHERE %s = ? AND time > now() - toIntervalHour(?)
		ORDER BY time DESC LIMIT 1`, col)
	var ratio float64
	if err := s.conn.QueryRow(ctx, query, id, hours).Scan(&ratio); err != nil {
		return 0
	}
	return ratio
}

func splitProto(proto map[string]ProtoCount) (pkts, bytes map[string]uint64) {
	pkts = make(map[string]uint64, len(proto))
	bytes = make(map[string]uint64, len(proto))
	for k, v := range proto {
		pkts[k] = v.Pkts
		bytes[k] = v.Bytes
	}
	return pkts, bytes
}

func parseWindowTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Now()
}
