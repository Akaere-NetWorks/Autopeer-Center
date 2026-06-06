package latency

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/akaere/autopeer-center/internal/queue"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("pkg", "latency")

type rttReading struct {
	Time     time.Time
	RttMs    float64
	Abnormal bool
}

type peerInfo struct {
	PeerID       string
	ContactEmail string
	RemoteASN    int64
	NodeName     string
}

type Checker struct {
	latency repository.LatencyRepository
	peers   repository.PeerRepository
	c       *cache.Cache

	getSetting          func(key string) string
	getThreshold        func(key string) string
	sendEmail           func(to, template string, vars map[string]interface{})
	adminEmails         []string
	getEmailLevel       func(asn int64) int
	notificationAllowed func(asn int64, key string) bool
	botNotifyPeer       func(asn int64, notifKey string, event string, data map[string]interface{})
}

func NewChecker(latency repository.LatencyRepository, peers repository.PeerRepository, c *cache.Cache) *Checker {
	return &Checker{latency: latency, peers: peers, c: c}
}

func (c *Checker) SetNotifier(
	adminEmails []string,
	sendEmail func(to, template string, vars map[string]interface{}),
	getSetting func(key string) string,
	getThreshold func(key string) string,
) {
	c.adminEmails = adminEmails
	c.sendEmail = sendEmail
	c.getSetting = getSetting
	c.getThreshold = getThreshold
}

func (c *Checker) SetEmailLevelFn(fn func(asn int64) int) {
	c.getEmailLevel = fn
}

func (c *Checker) SetNotificationAllowedFn(fn func(asn int64, key string) bool) {
	c.notificationAllowed = fn
}

func (c *Checker) SetBotNotifierFn(fn func(asn int64, notifKey string, event string, data map[string]interface{})) {
	c.botNotifyPeer = fn
}

// CheckAll enqueues individual peer check tasks via Asynq (fan-out pattern).
func (c *Checker) CheckAll(ctx context.Context, q *queue.Queue) error {
	if c.getSetting == nil || c.sendEmail == nil {
		return nil
	}
	if c.getSetting("peer_latency_high_enabled") != "true" {
		return nil
	}

	activePeers, err := c.latency.ListActivePeersWithNode(ctx)
	if err != nil {
		log.WithError(err).Error("query active peers failed")
		return err
	}
	if len(activePeers) == 0 {
		return nil
	}

	for _, ap := range activePeers {
		pi := peerInfo{
			PeerID:       ap.PeerID,
			ContactEmail: ap.ContactEmail,
			RemoteASN:    ap.RemoteASN,
			NodeName:     ap.NodeName,
		}
		payload, err := json.Marshal(pi)
		if err != nil {
			log.WithError(err).Warn("failed to marshal peer info")
			continue
		}
		if err := q.EnqueueLatencyCheckPeer(ctx, payload); err != nil {
			log.WithError(err).WithField("peer_id", pi.PeerID).Warn("failed to enqueue latency check peer task")
		}
	}
	return nil
}

// CheckPeerFromPayload deserializes a peer info payload and checks a single peer.
func (c *Checker) CheckPeerFromPayload(ctx context.Context, payload []byte) error {
	var pi peerInfo
	if err := json.Unmarshal(payload, &pi); err != nil {
		return err
	}
	c.checkPeer(ctx, pi)
	return nil
}

func (c *Checker) Run(ctx context.Context) {
	log.Info("latency checker started")
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("latency checker stopped")
			return
		case <-ticker.C:
			c.checkAll()
		}
	}
}

func (c *Checker) checkAll() {
	if c.getSetting == nil || c.sendEmail == nil {
		return
	}
	if c.getSetting("peer_latency_high_enabled") != "true" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	activePeers, err := c.latency.ListActivePeersWithNode(ctx)
	if err != nil {
		log.WithError(err).Error("query active peers failed")
		return
	}

	var peers []peerInfo
	for _, ap := range activePeers {
		peers = append(peers, peerInfo{
			PeerID:       ap.PeerID,
			ContactEmail: ap.ContactEmail,
			RemoteASN:    ap.RemoteASN,
			NodeName:     ap.NodeName,
		})
	}

	if len(peers) == 0 {
		return
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for _, p := range peers {
		wg.Add(1)
		go func(pi peerInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c.checkPeer(ctx, pi)
		}(p)
	}
	wg.Wait()
}

func (c *Checker) checkPeer(ctx context.Context, p peerInfo) {
	absThreshold := c.floatSetting("peer_latency_high_threshold_ms", 200)
	multiplier := c.floatSetting("peer_latency_high_multiplier", 2.0)
	sustainedCount := c.intSetting("peer_latency_high_sustained_count", 2)
	countThreshold := c.intSetting("peer_latency_high_count_threshold", 20)
	windowMinutes := c.intSetting("peer_latency_high_window_minutes", 60)
	cooldownHours := c.intSetting("peer_latency_high_cooldown_hours", 24)
	notifyAdmin := c.getSetting("peer_latency_high_notify_admin") == "true"
	recoverEnabled := c.getSetting("peer_latency_recovered_enabled") == "true"

	baseline, err := c.latency.GetPeerRttPercentile(ctx, p.PeerID)
	if err != nil {
		return
	}

	queryCtx, queryCancel := context.WithTimeout(ctx, 10*time.Second)
	defer queryCancel()
	rttPoints, err := c.latency.GetPeerRecentRtt(queryCtx, p.PeerID, windowMinutes)
	if err != nil {
		log.WithError(err).WithField("peer_id", p.PeerID).Error("query recent rtt failed")
		return
	}

	var readings []rttReading
	for _, pt := range rttPoints {
		if pt.RttMs == nil || pt.Time.IsZero() {
			continue
		}
		r := rttReading{
			Time:  pt.Time.Time,
			RttMs: *pt.RttMs,
		}
		effectiveBaseline := 1.0
		if baseline != nil && *baseline > 1.0 {
			effectiveBaseline = *baseline
		}
		r.Abnormal = r.RttMs > effectiveBaseline*multiplier || r.RttMs > absThreshold
		readings = append(readings, r)
	}

	if len(readings) == 0 {
		return
	}

	maxConsecutive := 0
	currentConsecutive := 0
	abnormalCount := 0
	for _, r := range readings {
		if r.Abnormal {
			currentConsecutive++
			abnormalCount++
			if currentConsecutive > maxConsecutive {
				maxConsecutive = currentConsecutive
			}
		} else {
			currentConsecutive = 0
		}
	}

	sustainedTrigger := maxConsecutive >= sustainedCount
	countTrigger := abnormalCount >= countThreshold
	anyTrigger := sustainedTrigger || countTrigger

	if anyTrigger {
		var notified bool
		_, _ = c.c.Get(cache.BucketAlerts, cache.PeerKey(cache.AlertLatency, p.PeerID), &notified)
		if notified {
			return
		}

		cooldown := time.Duration(cooldownHours) * time.Hour
		c.c.Put(cache.BucketAlerts, cache.PeerKey(cache.AlertLatency, p.PeerID), true, cooldown)
		c.c.Put(cache.BucketAlerts, cache.PeerKey(cache.LatencyActive, p.PeerID), true, 0)

		latestReading := readings[len(readings)-1]
		baselineStr := fmt.Sprintf("%.1f", absBaseline(baseline))
		triggerCond := "sustained"
		if countTrigger && !sustainedTrigger {
			triggerCond = "count"
		}
		if sustainedTrigger && countTrigger {
			triggerCond = "both"
		}

		var timepoints []map[string]interface{}
		for _, r := range readings {
			if r.Abnormal {
				timepoints = append(timepoints, map[string]interface{}{
					"time":   r.Time.UTC().Format("2006-01-02 15:04 UTC"),
					"rtt_ms": fmt.Sprintf("%.1f", r.RttMs),
				})
			}
		}

		vars := map[string]interface{}{
			"asn":              p.RemoteASN,
			"nodeName":         p.NodeName,
			"currentRtt":       fmt.Sprintf("%.1f", latestReading.RttMs),
			"baselineRtt":      baselineStr,
			"triggerCondition": triggerCond,
			"anomalyCount":     abnormalCount,
			"sustainedCount":   maxConsecutive,
			"timepoints":       timepoints,
		}

		if p.ContactEmail != "" {
			if c.notificationAllowed != nil && !c.notificationAllowed(p.RemoteASN, service.NotificationPeerLatencyHigh) {
				log.WithFields(logrus.Fields{"asn": p.RemoteASN}).Debug("latency-high suppressed by user notification preference")
			} else if c.getEmailLevel == nil || c.getEmailLevel(p.RemoteASN) >= 3 {
				c.sendEmail(p.ContactEmail, "peer-latency-high", vars)
			} else {
				log.WithFields(logrus.Fields{"asn": p.RemoteASN}).Debug("latency-high suppressed by user level preference")
			}
		}
		if notifyAdmin {
			for _, email := range c.adminEmails {
				if email != "" {
					c.sendEmail(email, "peer-latency-high", vars)
				}
			}
		}

		if c.botNotifyPeer != nil {
			c.botNotifyPeer(p.RemoteASN, service.NotificationPeerLatencyHigh, "peer.latency_high", vars)
		}

		log.WithFields(logrus.Fields{
			"peer_id": p.PeerID, "asn": p.RemoteASN, "node": p.NodeName,
			"trigger": triggerCond, "anomaly_count": abnormalCount,
			"sustained": maxConsecutive, "current_rtt": latestReading.RttMs,
		}).Info("latency alert sent")
		return
	}

	var active bool
	ok, _ := c.c.Get(cache.BucketAlerts, cache.PeerKey(cache.LatencyActive, p.PeerID), &active)
	if !ok || !active {
		return
	}
	if !recoverEnabled {
		return
	}

	trailingNormal := 0
	for i := len(readings) - 1; i >= 0; i-- {
		if !readings[i].Abnormal {
			trailingNormal++
		} else {
			break
		}
	}
	if trailingNormal < 2 {
		return
	}

	c.c.Delete(cache.BucketAlerts, cache.PeerKey(cache.LatencyActive, p.PeerID))
	c.c.Put(cache.BucketAlerts, cache.PeerKey(cache.AlertLatency, p.PeerID), true, 6*time.Hour)

	latestReading := readings[len(readings)-1]
	baselineStr := fmt.Sprintf("%.1f", absBaseline(baseline))

	vars := map[string]interface{}{
		"asn":         p.RemoteASN,
		"nodeName":    p.NodeName,
		"currentRtt":  fmt.Sprintf("%.1f", latestReading.RttMs),
		"baselineRtt": baselineStr,
	}

	if p.ContactEmail != "" {
		if c.notificationAllowed != nil && !c.notificationAllowed(p.RemoteASN, service.NotificationPeerLatencyRecovered) {
			log.WithFields(logrus.Fields{"asn": p.RemoteASN}).Debug("latency-recovered suppressed by user notification preference")
		} else if c.getEmailLevel == nil || c.getEmailLevel(p.RemoteASN) >= 3 {
			c.sendEmail(p.ContactEmail, "peer-latency-recovered", vars)
		} else {
			log.WithFields(logrus.Fields{"asn": p.RemoteASN}).Debug("latency-recovered suppressed by user level preference")
		}
	}
	if notifyAdmin {
		for _, email := range c.adminEmails {
			if email != "" {
				c.sendEmail(email, "peer-latency-recovered", vars)
			}
		}
	}

	if c.botNotifyPeer != nil {
		c.botNotifyPeer(p.RemoteASN, service.NotificationPeerLatencyRecovered, "peer.latency_recovered", vars)
	}

	log.WithFields(logrus.Fields{
		"peer_id": p.PeerID, "asn": p.RemoteASN, "node": p.NodeName,
		"current_rtt": latestReading.RttMs,
	}).Info("latency recovered notification sent")
}

func (c *Checker) floatSetting(key string, fallback float64) float64 {
	if c.getThreshold == nil {
		return fallback
	}
	v := c.getThreshold(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return fallback
	}
	return f
}

func (c *Checker) intSetting(key string, fallback int) int {
	if c.getThreshold == nil {
		return fallback
	}
	v := c.getThreshold(key)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil || i <= 0 {
		return fallback
	}
	return i
}

func absBaseline(b *float64) float64 {
	if b == nil || *b <= 0 {
		return 0
	}
	return *b
}
