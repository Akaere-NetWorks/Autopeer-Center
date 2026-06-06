package endpoint

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("pkg", "endpoint")

// PeerEndpointCheck holds data for a single peer's endpoint verification.
type PeerEndpointCheck struct {
	PeerID                 string
	NodeID                 string
	RemoteASN              int64
	RemoteEndpoint         string
	WgActualEndpoint       string
	BgpProtoName           string
	ContactEmail           string
	NodeName               string
	EndpointMismatchSince  *time.Time
	BGPSuspendedByEndpoint bool
}

type Checker struct {
	peers repository.PeerRepository
	hub   *ws.Hub
	cache *cache.Cache

	getSetting         func(key string) string
	sendEmail          func(to, template string, vars map[string]interface{})
	botNotifyPeer      func(asn int64, notifKey string, event string, data map[string]interface{})
	getEmailLevel      func(asn int64) int
	notificationAllowed func(asn int64, key string) bool
}

func NewChecker(
	peers repository.PeerRepository,
	hub *ws.Hub,
	cache *cache.Cache,
) *Checker {
	return &Checker{
		peers: peers,
		hub:   hub,
		cache: cache,
	}
}

func (c *Checker) SetNotifier(
	sendEmail func(to, template string, vars map[string]interface{}),
	getSetting func(key string) string,
) {
	c.sendEmail = sendEmail
	c.getSetting = getSetting
}

func (c *Checker) SetEmailLevelFn(fn func(asn int64) int)                      { c.getEmailLevel = fn }
func (c *Checker) SetNotificationAllowedFn(fn func(asn int64, key string) bool) { c.notificationAllowed = fn }
func (c *Checker) SetBotNotifierFn(fn func(asn int64, notifKey string, event string, data map[string]interface{})) {
	c.botNotifyPeer = fn
}

// Run starts an in-process goroutine loop (fallback when asynq is unavailable).
func (c *Checker) Run(ctx context.Context) {
	log.Info("endpoint checker started")

	// Resume all peers that were auto-suspended by the old endpoint checker logic.
	c.resumeAllSuspended(ctx)

	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("endpoint checker stopped")
			return
		case <-ticker.C:
			c.checkAll(ctx)
		}
	}
}

// CheckAll can be called by the asynq handler.
func (c *Checker) CheckAll(ctx context.Context) error {
	c.checkAll(ctx)
	return nil
}

func (c *Checker) checkAll(ctx context.Context) {
	if c.getSetting == nil || c.sendEmail == nil {
		return
	}
	if c.getSetting("endpoint_verification_enabled") != "true" {
		return
	}

	checkCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	peers, err := c.peers.ListActivePeersForEndpointCheck(checkCtx)
	if err != nil {
		log.WithError(err).Error("query peers for endpoint check failed")
		return
	}
	if len(peers) == 0 {
		return
	}

	thresholdMinutes := 30
	if v := c.getSetting("endpoint_mismatch_threshold_minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			thresholdMinutes = n
		}
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for _, p := range peers {
		wg.Add(1)
		go func(peer repository.PeerEndpointCheck) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c.checkPeer(checkCtx, peer, thresholdMinutes)
		}(p)
	}
	wg.Wait()
}

func (c *Checker) checkPeer(ctx context.Context, p repository.PeerEndpointCheck, thresholdMinutes int) {
	if p.WgActualEndpoint == "" {
		return
	}
	if p.RemoteEndpoint == "" {
		return
	}

	match, err := endpointsMatch(p.RemoteEndpoint, p.WgActualEndpoint)
	if err != nil {
		log.WithError(err).WithFields(logrus.Fields{
			"peer_id": p.PeerID, "remote_endpoint": p.RemoteEndpoint,
		}).Warn("endpoint comparison error, skipping")
		return
	}

	if match {
		c.handleMatch(ctx, p)
	} else {
		c.handleMismatch(ctx, p, thresholdMinutes)
	}
}

func (c *Checker) handleMatch(ctx context.Context, p repository.PeerEndpointCheck) {
	if p.EndpointMismatchSince == nil {
		return
	}

	log.WithFields(logrus.Fields{
		"peer_id": p.PeerID, "asn": p.RemoteASN,
	}).Info("endpoint mismatch resolved")

	if p.BGPSuspendedByEndpoint {
		if err := c.resumeBGP(ctx, p); err != nil {
			log.WithError(err).WithField("peer_id", p.PeerID).Error("failed to resume BGP after endpoint recovery; will retry next cycle")
			// Do NOT clear mismatch_since — keep the state so resumeBGP is retried on the next check cycle.
			return
		}
		if err := c.peers.SetBGPSuspendedByEndpoint(ctx, p.PeerID, false); err != nil {
			log.WithError(err).WithField("peer_id", p.PeerID).Error("failed to clear bgp_suspended_by_endpoint flag")
		}
	}

	if err := c.peers.SetEndpointMismatchSince(ctx, p.PeerID, nil); err != nil {
		log.WithError(err).WithField("peer_id", p.PeerID).Error("failed to clear endpoint_mismatch_since")
	}

	vars := map[string]interface{}{
		"asn":      p.RemoteASN,
		"nodeName": p.NodeName,
		"endpoint": p.RemoteEndpoint,
		"actual":   p.WgActualEndpoint,
	}
	if p.ContactEmail != "" && c.notificationAllowed != nil && c.notificationAllowed(p.RemoteASN, "peer_endpoint_recovered") {
		c.sendEmail(p.ContactEmail, "peer-endpoint-recovered", vars)
	}
	if c.botNotifyPeer != nil {
		c.botNotifyPeer(p.RemoteASN, "peer_endpoint_recovered", "peer.endpoint_recovered", vars)
	}
}

func (c *Checker) handleMismatch(ctx context.Context, p repository.PeerEndpointCheck, thresholdMinutes int) {
	now := time.Now()

	if p.EndpointMismatchSince == nil {
		if err := c.peers.SetEndpointMismatchSince(ctx, p.PeerID, &now); err != nil {
			log.WithError(err).WithField("peer_id", p.PeerID).Error("failed to set endpoint_mismatch_since")
			return
		}
		log.WithFields(logrus.Fields{
			"peer_id": p.PeerID, "asn": p.RemoteASN,
			"expected": p.RemoteEndpoint, "actual": p.WgActualEndpoint,
		}).Warn("endpoint mismatch detected, tracking")
		return
	}

	duration := now.Sub(*p.EndpointMismatchSince)
	if duration < time.Duration(thresholdMinutes)*time.Minute {
		log.WithFields(logrus.Fields{
			"peer_id": p.PeerID, "duration_minutes": int(duration.Minutes()),
		}).Debug("endpoint mismatch below threshold")
		return
	}

	if p.BGPSuspendedByEndpoint {
		return
	}

	log.WithFields(logrus.Fields{
		"peer_id": p.PeerID, "asn": p.RemoteASN,
		"duration_minutes": int(duration.Minutes()),
	}).Warn("endpoint mismatch threshold exceeded")

	vars := map[string]interface{}{
		"asn":      p.RemoteASN,
		"nodeName": p.NodeName,
		"endpoint": p.RemoteEndpoint,
		"actual":   p.WgActualEndpoint,
		"duration": int(duration.Minutes()),
	}
	if p.ContactEmail != "" && c.notificationAllowed != nil && c.notificationAllowed(p.RemoteASN, "peer_endpoint_mismatch") {
		c.sendEmail(p.ContactEmail, "peer-endpoint-mismatch", vars)
	}
	if c.botNotifyPeer != nil {
		c.botNotifyPeer(p.RemoteASN, "peer_endpoint_mismatch", "peer.endpoint_mismatch", vars)
	}
}

func (c *Checker) suspendBGP(ctx context.Context, p repository.PeerEndpointCheck) error {
	if c.hub == nil {
		return fmt.Errorf("hub not available")
	}
	_, err := c.hub.SendCommand(p.NodeID, ws.TypeBirdDisable, ws.BirdControlPayload{
		PeerID:    p.PeerID,
		ProtoName: p.BgpProtoName,
	})
	return err
}

func (c *Checker) resumeBGP(ctx context.Context, p repository.PeerEndpointCheck) error {
	if c.hub == nil {
		return fmt.Errorf("hub not available")
	}
	_, err := c.hub.SendCommand(p.NodeID, ws.TypeBirdEnable, ws.BirdControlPayload{
		PeerID:    p.PeerID,
		ProtoName: p.BgpProtoName,
	})
	return err
}

// resumeAllSuspended finds all peers with bgp_suspended_by_endpoint = true
// and resumes their BGP sessions. Called once at startup to recover from
// the previous auto-suspend behaviour.
func (c *Checker) resumeAllSuspended(ctx context.Context) {
	peers, err := c.peers.ListPeersSuspendedByEndpoint(ctx)
	if err != nil {
		log.WithError(err).Error("failed to list peers suspended by endpoint")
		return
	}
	if len(peers) == 0 {
		return
	}

	log.WithField("count", len(peers)).Info("resuming BGP for all endpoint-suspended peers")

	for _, p := range peers {
		if err := c.resumeBGP(ctx, p); err != nil {
			log.WithError(err).WithField("peer_id", p.PeerID).Error("failed to resume BGP for suspended peer")
			continue
		}
		if err := c.peers.SetBGPSuspendedByEndpoint(ctx, p.PeerID, false); err != nil {
			log.WithError(err).WithField("peer_id", p.PeerID).Error("failed to clear bgp_suspended_by_endpoint flag")
		}
		if p.EndpointMismatchSince != nil {
			if err := c.peers.SetEndpointMismatchSince(ctx, p.PeerID, nil); err != nil {
				log.WithError(err).WithField("peer_id", p.PeerID).Error("failed to clear endpoint_mismatch_since")
			}
		}
		log.WithField("peer_id", p.PeerID).Info("resumed BGP for endpoint-suspended peer")
	}
}
