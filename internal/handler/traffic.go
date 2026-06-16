package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/akaere/autopeer-center/internal/clickhouse"
	"github.com/go-chi/chi/v5"
)

// TrafficHandler serves the admin traffic-analytics endpoints. A nil store means
// the feature is disabled (CLICKHOUSE_URL unset) and every endpoint returns 503.
type TrafficHandler struct {
	store *clickhouse.TrafficStore
}

func NewTrafficHandler(store *clickhouse.TrafficStore) *TrafficHandler {
	return &TrafficHandler{store: store}
}

// Enabled reports whether the traffic-analytics feature is active.
func (h *TrafficHandler) Enabled() bool { return h.store != nil }

type trafficPointResp struct {
	Time           time.Time         `json:"time"`
	SampledBytes   uint64            `json:"sampled_bytes"`
	SampledPackets uint64            `json:"sampled_packets"`
	V4Bytes        uint64            `json:"v4_bytes"`
	V6Bytes        uint64            `json:"v6_bytes"`
	ProtoBytes     map[string]uint64 `json:"proto_bytes"`
	ProtoPkts      map[string]uint64 `json:"proto_pkts"`
	SizeBuckets    map[string]uint64 `json:"size_buckets"`
}

type trafficTopResp struct {
	Label string `json:"label"`
	Pkts  uint64 `json:"pkts"`
	Bytes uint64 `json:"bytes"`
}

type trafficResponse struct {
	Enabled     bool               `json:"enabled"`
	SampleRatio float64            `json:"sample_ratio"`
	Points      []trafficPointResp `json:"points"`
	TopSrc      []trafficTopResp   `json:"top_src"`
	TopDst      []trafficTopResp   `json:"top_dst"`
	TopPorts    []trafficTopResp   `json:"top_ports"`
}

// PeerTraffic returns the per-interface (single peer) traffic series + Top-N.
func (h *TrafficHandler) PeerTraffic(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "traffic_analytics_disabled", "ClickHouse not configured")
		return
	}
	peerID := chi.URLParam(r, "id")
	hours := clampQuery(r, "hours", 24, 1, 168)
	top := clampQuery(r, "top", 10, 1, 50)

	series, err := h.store.PeerSeries(r.Context(), peerID, hours, top)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch traffic analytics")
		return
	}
	JSON(w, http.StatusOK, buildTrafficResponse(series))
}

// NodeTraffic returns the node-aggregated traffic series + node-level Top-N.
func (h *TrafficHandler) NodeTraffic(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "traffic_analytics_disabled", "ClickHouse not configured")
		return
	}
	nodeID := chi.URLParam(r, "id")
	hours := clampQuery(r, "hours", 24, 1, 168)
	top := clampQuery(r, "top", 10, 1, 50)
	bucket := clampQuery(r, "bucket", defaultBucketMinutes(hours), 1, 1440)

	series, err := h.store.NodeSeries(r.Context(), nodeID, hours, bucket, top)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch traffic analytics")
		return
	}
	JSON(w, http.StatusOK, buildTrafficResponse(series))
}

func buildTrafficResponse(s *clickhouse.TrafficSeries) trafficResponse {
	points := make([]trafficPointResp, 0, len(s.Points))
	for _, p := range s.Points {
		points = append(points, trafficPointResp{
			Time:           p.Time,
			SampledBytes:   p.SampledBytes,
			SampledPackets: p.SampledPackets,
			V4Bytes:        p.V4Bytes,
			V6Bytes:        p.V6Bytes,
			ProtoBytes:     p.ProtoBytes,
			ProtoPkts:      p.ProtoPkts,
			SizeBuckets:    p.SizeBuckets,
		})
	}
	return trafficResponse{
		Enabled:     true,
		SampleRatio: s.SampleRatio,
		Points:      points,
		TopSrc:      topRows(s.TopSrc),
		TopDst:      topRows(s.TopDst),
		TopPorts:    topRows(s.TopPorts),
	}
}

func topRows(rows []clickhouse.TopRow) []trafficTopResp {
	out := make([]trafficTopResp, 0, len(rows))
	for _, r := range rows {
		out = append(out, trafficTopResp{Label: r.Label, Pkts: r.Pkts, Bytes: r.Bytes})
	}
	return out
}

// defaultBucketMinutes targets ~200 points across the requested window.
func defaultBucketMinutes(hours int) int {
	b := hours * 60 / 200
	if b < 1 {
		b = 1
	}
	return b
}

func clampQuery(r *http.Request, key string, def, min, max int) int {
	if q := r.URL.Query().Get(key); q != "" {
		if v, err := strconv.Atoi(q); err == nil && v >= min && v <= max {
			return v
		}
	}
	return def
}
