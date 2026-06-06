package ws

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/sirupsen/logrus"
)

type peerContact struct {
	email string
	name  string
}

func (h *Hub) handleHeartbeat(nodeID string, data []byte) {
	log.WithField("node_id", nodeID).Debug("handleHeartbeat")

	var msg struct {
		Payload HeartbeatPayload `json:"payload"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		log.WithError(err).WithField("node_id", nodeID).Error("heartbeat unmarshal error")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	oldVersion, _ := h.nodes.GetField(ctx, nodeID, "agent_version")

	if msg.Payload.Version != "" {
		if err := h.nodes.SetAgentVersion(ctx, nodeID, msg.Payload.Version); err != nil {
			log.WithError(err).WithField("node_id", nodeID).Error("set agent version failed")
		}
	} else {
		if err := h.nodes.SetAgentState(ctx, nodeID, "online"); err != nil {
			log.WithError(err).WithField("node_id", nodeID).Error("set agent state online failed")
		}
	}

	if msg.Payload.NodeMetrics != nil {
		nm := msg.Payload.NodeMetrics
		err := h.metrics.InsertNodeMetric(ctx, &model.NodeMetric{
			NodeID:       nodeID,
			MemAllocMB:   int64PtrFromU64(nm.MemAllocMB),
			MemSysMB:     int64PtrFromU64(nm.MemSysMB),
			NumGoroutine: intPtrFromInt(nm.NumGoroutine),
			UptimeSecs:   int64PtrFromInt64(nm.UptimeSecs),
		})
		if err != nil {
			log.WithError(err).WithField("node_id", nodeID).Error("insert node_metrics failed")
		}
	}

	if oldVersion != "" && msg.Payload.Version != "" && oldVersion != msg.Payload.Version {
		if h.getSetting != nil && h.getSetting("agent_updated_enabled") == "true" && h.sendEmail != nil {
			notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 5*time.Second)
			nodeName, _ := h.nodes.GetField(notifyCtx, nodeID, "name")
			notifyCancel()
			for _, email := range h.adminEmails {
				if email != "" {
					h.sendEmail(email, "agent-updated", map[string]interface{}{
						"nodeName": nodeName, "oldVersion": oldVersion, "newVersion": msg.Payload.Version,
					})
				}
			}
			log.WithFields(logrus.Fields{
				"node_id": nodeID, "node_name": nodeName,
				"old_version": oldVersion, "new_version": msg.Payload.Version,
			}).Info("agent-updated notification")

			go h.NotifyBotAdmin("agent.updated", map[string]interface{}{
				"node_id": nodeID, "nodeName": nodeName,
				"oldVersion": oldVersion, "newVersion": msg.Payload.Version,
			})
		}
	}

	wasOffline := h.isNodeOfflineNotified(nodeID)
	h.setNodeOfflineNotified(nodeID, false)

	if wasOffline {
		backOnlineCtx, backOnlineCancel := context.WithTimeout(context.Background(), 5*time.Second)
		nodeName, _ := h.nodes.GetField(backOnlineCtx, nodeID, "name")
		backOnlineCancel()
		log.WithFields(logrus.Fields{"node_id": nodeID, "node_name": nodeName}).Info("node back online")
		if h.sendEmail != nil {
			for _, email := range h.adminEmails {
				if email != "" {
					go h.sendEmail(email, "node-back-online", map[string]interface{}{
						"nodeName": nodeName,
					})
				}
			}
		}
		go h.NotifyBotAdmin("node.online", map[string]interface{}{
			"node_id": nodeID, "nodeName": nodeName,
		})
	}

	contactMap := make(map[string]peerContact)
	if h.sendEmail != nil {
		contactCtx, contactCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer contactCancel()
		contacts, err := h.peers.ListActiveByNode(contactCtx, nodeID)
		if err == nil {
			for _, c := range contacts {
				contactMap[c.PeerID] = peerContact{email: c.Email, name: c.Name}
			}
		} else {
			log.WithError(err).WithField("node_id", nodeID).Error("prefetch peer contacts failed")
		}
	}

	for _, p := range msg.Payload.Peers {
		log.WithFields(logrus.Fields{
			"node_id": nodeID, "peer_id": p.PeerID, "asn": p.ASN,
			"bgp_state": p.BGPState,
		}).Debug("heartbeat peer")

		var lastHandshake *time.Time
		if p.WgLastHandshake != "" && p.WgLastHandshake != "0" {
			if ts, err := strconv.ParseInt(p.WgLastHandshake, 10, 64); err == nil && ts > 0 {
				t := time.Unix(ts, 0)
				lastHandshake = &t
			}
		}

		peerCtx, peerCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := h.metrics.InsertPeerMetricFromHeartbeat(peerCtx, p.PeerID, nodeID, &model.PeerMetric{
			RxBytes:          p.WgRxBytes,
			TxBytes:          p.WgTxBytes,
			BGPState:         p.BGPState,
			RttMs:            p.RttMs,
			LastHandshake:    lastHandshake,
			RoutesImported:   p.RoutesImported,
			RoutesExported:   p.RoutesExported,
			RoutesPreferred:  p.RoutesPreferred,
			BGPUptimeSecs:    p.BGPUptimeSecs,
			WgActualEndpoint: p.WgActualEndpoint,
		})
		peerCancel()
		if err != nil {
			log.WithError(err).WithFields(logrus.Fields{"node_id": nodeID, "peer_id": p.PeerID}).Error("insert peer_metrics error")
		}

		h.checkAlerts(ctx, nodeID, &p, lastHandshake, contactMap)
	}
}

func int64PtrFromU64(v uint64) *int64 {
	if v == 0 {
		return nil
	}
	i := int64(v)
	return &i
}

func intPtrFromInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func int64PtrFromInt64(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func (h *Hub) checkAlerts(_ context.Context, nodeID string, p *PeerHeartbeat, lastHandshake *time.Time, contactMap map[string]peerContact) {
	if h.getSetting == nil {
		return
	}

	log.WithFields(logrus.Fields{
		"node_id": nodeID, "peer_id": p.PeerID,
		"bgp_state": p.BGPState, "has_handshake": lastHandshake != nil,
	}).Debug("checkAlerts")

	contact, hasContact := contactMap[p.PeerID]
	contactEmail := contact.email
	nodeName := contact.name
	_ = hasContact

	type pendingAlert struct {
		email    string
		template string
		vars     map[string]interface{}
	}
	type pendingBotEvent struct {
		event string
		vars  map[string]interface{}
	}
	var alerts []pendingAlert
	var botEvents []pendingBotEvent

	bgpDownEnabled := h.getSetting != nil && h.getSetting("peer_bgp_down_enabled") == "true"
	bgpDownThreshold := ""
	if h.getThreshold != nil {
		bgpDownThreshold = h.getThreshold("peer_bgp_down_enabled")
	}
	bgpRecoveredEnabled := h.getSetting != nil && h.getSetting("peer_bgp_recovered_enabled") == "true"
	handshakeStaleEnabled := h.getSetting != nil && h.getSetting("peer_handshake_stale_enabled") == "true"
	handshakeThreshold := ""
	if h.getThreshold != nil {
		handshakeThreshold = h.getThreshold("peer_handshake_stale_enabled")
	}

	if bgpDownEnabled && p.BGPState != "" {
		if strings.ToLower(p.BGPState) != "established" && p.BGPState != "up" {
			failCount := h.incBGPFailCount(p.PeerID)
			t := 3
			if v, err := strconv.Atoi(bgpDownThreshold); err == nil && v > 0 {
				t = v
			}
			if failCount >= t && !h.isBGPDownNotified(p.PeerID) {
				h.setBGPDownNotified(p.PeerID, true)
				bgpDownVars := map[string]interface{}{
					"asn": p.ASN, "nodeName": nodeName, "bgpState": p.BGPState,
				}
				if contactEmail != "" {
					alerts = append(alerts, pendingAlert{contactEmail, "peer-bgp-down", bgpDownVars})
				}
				botEvents = append(botEvents, pendingBotEvent{"peer.bgp_down", bgpDownVars})
			}
		} else {
		if h.isBGPDownNotified(p.PeerID) && bgpRecoveredEnabled {
			bgpRecoveredVars := map[string]interface{}{
				"asn": p.ASN, "nodeName": nodeName,
			}
			if contactEmail != "" {
				alerts = append(alerts, pendingAlert{contactEmail, "peer-bgp-recovered", bgpRecoveredVars})
			}
			botEvents = append(botEvents, pendingBotEvent{"peer.bgp_recovered", bgpRecoveredVars})
		}
			h.setBGPDownNotified(p.PeerID, false)
			h.resetBGPFailCount(p.PeerID)
		}
	}

	if handshakeStaleEnabled && lastHandshake != nil {
		t := 5
		if v, err := strconv.Atoi(handshakeThreshold); err == nil && v > 0 {
			t = v
		}
		staleDuration := time.Since(*lastHandshake).Minutes()
		if staleDuration > float64(t) && !h.isHandshakeNotified(p.PeerID) {
			h.setHandshakeNotified(p.PeerID, true)
			handshakeVars := map[string]interface{}{
				"asn": p.ASN, "nodeName": nodeName, "lastHandshake": lastHandshake.Format(time.RFC3339),
			}
			if contactEmail != "" {
				alerts = append(alerts, pendingAlert{contactEmail, "peer-handshake-stale", handshakeVars})
			}
			botEvents = append(botEvents, pendingBotEvent{"peer.handshake_stale", handshakeVars})
		} else if staleDuration <= float64(t) {
			h.setHandshakeNotified(p.PeerID, false)
		}
	}

	// Map email template name → notification preference key
	alertTemplateToNotifKey := func(template string) string {
		switch template {
		case "peer-bgp-down":
			return service.NotificationPeerBGPDown
		case "peer-bgp-recovered":
			return service.NotificationPeerBGPRecovered
		case "peer-handshake-stale":
			return service.NotificationPeerHandshakeStale
		default:
			return template
		}
	}
	// Map bot event name → telegram notification preference key
	botEventToNotifKey := func(event string) string {
		switch event {
		case "peer.bgp_down":
			return service.NotificationPeerBGPDown
		case "peer.bgp_recovered":
			return service.NotificationPeerBGPRecovered
		case "peer.handshake_stale":
			return service.NotificationPeerHandshakeStale
		default:
			return event
		}
	}

	// Email dispatch
	if h.sendEmail != nil {
		for _, a := range alerts {
			notifKey := alertTemplateToNotifKey(a.template)
			if h.notificationAllowed != nil && !h.notificationAllowed(p.ASN, notifKey) {
				log.WithFields(logrus.Fields{"asn": p.ASN, "template": a.template}).Debug("user email alert suppressed by notification preference")
				continue
			}
			if h.getEmailLevel != nil {
				level := h.getEmailLevel(p.ASN)
				if level < 2 {
					log.WithFields(logrus.Fields{
						"asn": p.ASN, "template": a.template, "level": level,
					}).Debug("user email alert suppressed by level preference")
					continue
				}
			}
			h.sendEmail(a.email, a.template, a.vars)
		}
	}

	// Bot dispatch — independent of email gates
	if h.botBindings != nil && len(botEvents) > 0 {
		go func(events []pendingBotEvent) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			binding, err := h.botBindings.GetByASN(ctx, p.ASN)
			if err != nil || binding == nil {
				return
			}
			for _, ev := range events {
				notifKey := botEventToNotifKey(ev.event)
				if h.telegramNotificationAllowed != nil && !h.telegramNotificationAllowed(p.ASN, notifKey) {
					log.WithFields(logrus.Fields{"asn": p.ASN, "event": ev.event}).Debug("telegram alert suppressed by notification preference")
					continue
				}
				notifyData := map[string]interface{}{
					"peer_id":  p.PeerID,
					"asn":      p.ASN,
					"nodeName": nodeName,
				}
				for k, v := range ev.vars {
					notifyData[k] = v
				}
				h.NotifyBotUser(binding.TgUserID, ev.event, notifyData)
			}
		}(botEvents)
	}
}

func (h *Hub) handleAgentUpdating(ac *AgentConn) {
	log.WithField("node_id", ac.NodeID).Debug("handleAgentUpdating")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.nodes.SetAgentState(ctx, ac.NodeID, "updating"); err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("set agent state updating failed")
	}
	log.WithField("node_id", ac.NodeID).Info("agent.updating marked")

	h.notifyNodeOffline(ctx, ac.NodeID, true)
}

func (h *Hub) notifyNodeOffline(ctx context.Context, nodeID string, skipIfUpdating bool) {
	if h.getSetting == nil || h.getSetting("node_offline_enabled") != "true" {
		return
	}
	if h.sendEmail == nil {
		return
	}

	if h.isNodeOfflineNotified(nodeID) {
		return
	}
	h.setNodeOfflineNotified(nodeID, true)

	if skipIfUpdating {
		state, err := h.nodes.GetField(ctx, nodeID, "agent_state")
		if err != nil {
			log.WithError(err).WithField("node_id", nodeID).Debug("get agent_state for notifyNodeOffline failed")
		}
		if state == "updating" {
			return
		}
	}

	node, err := h.nodes.GetByID(ctx, nodeID)
	if err != nil {
		log.WithError(err).WithField("node_id", nodeID).Debug("get node for notifyNodeOffline failed")
	}

	nodeName := ""
	location := ""
	if node != nil {
		nodeName = node.Name
		location = node.Location
	}

	offlineSince := time.Now().UTC().Format(time.RFC3339)
	for _, email := range h.adminEmails {
		if email != "" {
			h.sendEmail(email, "node-offline", map[string]interface{}{
				"nodeName": nodeName, "location": location, "offlineSince": offlineSince,
			})
		}
	}
	log.WithFields(logrus.Fields{"node_id": nodeID, "node_name": nodeName, "location": location}).Info("node-offline alert")

	go h.NotifyBotAdmin("node.offline", map[string]interface{}{
		"node_id": nodeID, "nodeName": nodeName, "location": location,
	})
}
