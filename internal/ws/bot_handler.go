package ws

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"golang.org/x/crypto/bcrypt"
)

func validateBotTarget(target string) error {
	ip := net.ParseIP(target)
	if ip == nil {
		return fmt.Errorf("target %s is not a valid IP address (hostnames are not allowed)", target)
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return fmt.Errorf("target %s is a reserved address", target)
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 10 || (ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) || (ip4[0] == 192 && ip4[1] == 168) || (ip4[0] == 169 && ip4[1] == 254) {
			return fmt.Errorf("target %s is a private/link-local address", target)
		}
		if ip4[0] == 127 {
			return fmt.Errorf("target %s is a loopback address", target)
		}
		if bytesEqualVal(ip4, []byte{0, 0, 0, 0}) || bytesEqualVal(ip4, []byte{255, 255, 255, 255}) {
			return fmt.Errorf("target %s is a broadcast/unspecified address", target)
		}
	}
	return nil
}

func bytesEqualVal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (h *Hub) handleBotAuth(bc *BotConn, msg Message) {
	log.WithField("bot_id", bc.ID).Info("handleBotAuth")

	if h.GetBotSetting("bot_enabled") != "true" {
		log.WithField("bot_id", bc.ID).Warn("bot auth rejected: bot disabled")
		h.sendToBot(bc, TypeBotAuthAck, msg.ID, BotAuthAckPayload{Success: false, Error: "bot is disabled"})
		bc.Close()
		return
	}

	var payload BotAuthPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	if payload.Token == "" {
		log.WithField("bot_id", bc.ID).Warn("bot auth failed: empty token")
		h.sendToBot(bc, TypeBotAuthAck, msg.ID, BotAuthAckPayload{Success: false, Error: "invalid token"})
		bc.Close()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	setting, err := h.bot.GetSetting(ctx, "bot_auth_token")
	cancel()
	if err != nil {
		log.WithError(err).Warn("failed to query bot_auth_token")
		h.sendToBot(bc, TypeBotAuthAck, msg.ID, BotAuthAckPayload{Success: false, Error: "invalid token"})
		bc.Close()
		return
	}

	var dbHash string
	if setting.SettingValueHash != nil {
		dbHash = *setting.SettingValueHash
	}
	dbPlain := setting.SettingValue

	authenticated := false

	if dbHash != "" {
		if bcrypt.CompareHashAndPassword([]byte(dbHash), []byte(payload.Token)) == nil {
			authenticated = true
		}
	}

	if !authenticated && dbPlain != "" {
		if subtle.ConstantTimeCompare([]byte(payload.Token), []byte(dbPlain)) == 1 {
			authenticated = true
			go h.migrateBotTokenToHash(dbPlain)
		}
	}

	if !authenticated {
		log.WithField("bot_id", bc.ID).Warn("bot auth failed: invalid token")
		h.sendToBot(bc, TypeBotAuthAck, msg.ID, BotAuthAckPayload{Success: false, Error: "invalid token"})
		bc.Close()
		return
	}

	bc.authenticated = true
	h.sendToBot(bc, TypeBotAuthAck, msg.ID, BotAuthAckPayload{Success: true})

	settings := h.loadAllBotSettings()
	h.sendToBot(bc, TypeBotSettingsUpdate, "", BotSettingsPayload{Settings: settings})

	log.WithField("bot_id", bc.ID).Info("bot authenticated")
}

func (h *Hub) migrateBotTokenToHash(plainToken string) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plainToken), bcrypt.DefaultCost)
	if err != nil {
		log.WithError(err).Error("failed to bcrypt hash bot token")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = h.bot.ResetToken(ctx, string(hash), plainToken)
	if err != nil {
		log.WithError(err).Error("failed to migrate bot token to hash")
		return
	}
	log.Info("bot_auth_token migrated from plaintext to bcrypt hash")
}

func (h *Hub) handleBotPing(bc *BotConn, msg Message) {
	var payload BotCommandPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	if payload.Target == "" {
		h.sendToBot(bc, TypeBotPingResult, msg.ID, BotPingResult{
			Target:  "",
			Results: []PingResultEntry{{Success: false, Error: "missing target"}},
		})
		return
	}
	if err := validateBotTarget(payload.Target); err != nil {
		h.sendToBot(bc, TypeBotPingResult, msg.ID, BotPingResult{
			Target:  payload.Target,
			Results: []PingResultEntry{{Success: false, Error: err.Error()}},
		})
		return
	}

	nodeIDs := h.getOnlineNodes(payload.NodeID)
	if len(nodeIDs) == 0 {
		h.sendToBot(bc, TypeBotPingResult, msg.ID, BotPingResult{
			Target:  payload.Target,
			Results: []PingResultEntry{{Success: false, Error: "no online nodes available"}},
		})
		return
	}

	type nodeResult struct {
		nodeID   string
		nodeName string
		resp     *Message
		err      error
	}

	results := make([]nodeResult, len(nodeIDs))
	var wg sync.WaitGroup
	nodeSem := make(chan struct{}, 20)

	for i, nid := range nodeIDs {
		wg.Add(1)
		go func(idx int, nid string) {
			defer wg.Done()
			nodeSem <- struct{}{}
			defer func() { <-nodeSem }()
			var name string
			nameCtx, nameCancel := context.WithTimeout(context.Background(), 3*time.Second)
			name, _ = h.nodes.GetField(nameCtx, nid, "name")
			nameCancel()

			resp, err := h.SendCommand(nid, TypeNetworkPing, NetworkPingPayload{
				Target: payload.Target,
				Count:  4,
			})
			results[idx] = nodeResult{nodeID: nid, nodeName: name, resp: resp, err: err}
		}(i, nid)
	}

	wg.Wait()

	entries := make([]PingResultEntry, 0, len(results))
	for _, r := range results {
		entry := PingResultEntry{
			NodeID:   r.nodeID,
			NodeName: r.nodeName,
		}
		if r.err != nil {
			entry.Success = false
			entry.Error = r.err.Error()
		} else if r.resp != nil && r.resp.Success != nil && *r.resp.Success {
			entry.Success = true
			if data, err := json.Marshal(r.resp.Payload); err == nil {
				var stats struct {
					Sent     int       `json:"sent"`
					Received int       `json:"received"`
					Loss     float64   `json:"loss_percent"`
					MinRTT   float64   `json:"min_rtt_ms"`
					AvgRTT   float64   `json:"avg_rtt_ms"`
					MaxRTT   float64   `json:"max_rtt_ms"`
					RTTs     []float64 `json:"rtts"`
				}
				json.Unmarshal(data, &stats)
				entry.Sent = stats.Sent
				entry.Received = stats.Received
				entry.Loss = stats.Loss
				entry.MinRTT = stats.MinRTT
				entry.AvgRTT = stats.AvgRTT
				entry.MaxRTT = stats.MaxRTT
				entry.RTTs = stats.RTTs
			}
		} else {
			entry.Success = false
			if r.resp != nil {
				entry.Error = r.resp.Error
			}
		}
		entries = append(entries, entry)
	}

	h.sendToBot(bc, TypeBotPingResult, msg.ID, BotPingResult{
		Target:  payload.Target,
		Results: entries,
	})
}

func (h *Hub) handleBotTrace(bc *BotConn, msg Message) {
	var payload BotCommandPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	if payload.Target == "" {
		h.sendToBot(bc, TypeBotTraceResult, msg.ID, BotTraceResult{
			Target:  "",
			Results: []TraceResultEntry{{Success: false, Error: "missing target"}},
		})
		return
	}
	if err := validateBotTarget(payload.Target); err != nil {
		h.sendToBot(bc, TypeBotTraceResult, msg.ID, BotTraceResult{
			Target:  payload.Target,
			Results: []TraceResultEntry{{Success: false, Error: err.Error()}},
		})
		return
	}

	nodeIDs := h.getOnlineNodes(payload.NodeID)
	if len(nodeIDs) == 0 {
		h.sendToBot(bc, TypeBotTraceResult, msg.ID, BotTraceResult{
			Target:  payload.Target,
			Results: []TraceResultEntry{{Success: false, Error: "no online nodes available"}},
		})
		return
	}

	type nodeResult struct {
		nodeID   string
		nodeName string
		resp     *Message
		err      error
	}

	results := make([]nodeResult, len(nodeIDs))
	var wg sync.WaitGroup
	nodeSem := make(chan struct{}, 20)

	for i, nid := range nodeIDs {
		wg.Add(1)
		go func(idx int, nid string) {
			defer wg.Done()
			nodeSem <- struct{}{}
			defer func() { <-nodeSem }()
			var name string
			nameCtx, nameCancel := context.WithTimeout(context.Background(), 3*time.Second)
			name, _ = h.nodes.GetField(nameCtx, nid, "name")
			nameCancel()

			resp, err := h.SendCommand(nid, TypeNetworkTrace, NetworkTracePayload{
				Target:  payload.Target,
				MaxHops: 30,
			})
			results[idx] = nodeResult{nodeID: nid, nodeName: name, resp: resp, err: err}
		}(i, nid)
	}

	wg.Wait()

	entries := make([]TraceResultEntry, 0, len(results))
	for _, r := range results {
		entry := TraceResultEntry{
			NodeID:   r.nodeID,
			NodeName: r.nodeName,
		}
		if r.err != nil {
			entry.Success = false
			entry.Error = r.err.Error()
		} else if r.resp != nil && r.resp.Success != nil && *r.resp.Success {
			entry.Success = true
			if data, err := json.Marshal(r.resp.Payload); err == nil {
				var traceData struct {
					Hops []TraceHopEntry `json:"hops"`
				}
				json.Unmarshal(data, &traceData)
				entry.Hops = traceData.Hops
			}
		} else {
			entry.Success = false
			if r.resp != nil {
				entry.Error = r.resp.Error
			}
		}
		entries = append(entries, entry)
	}

	h.sendToBot(bc, TypeBotTraceResult, msg.ID, BotTraceResult{
		Target:  payload.Target,
		Results: entries,
	})
}

func (h *Hub) handleBotStatus(bc *BotConn, msg Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	adminStats, err := h.stats.Admin(ctx)
	var status BotStatusResult
	if err != nil {
		log.WithError(err).Error("failed to get admin stats for bot status")
		h.sendToBot(bc, TypeBotStatusResult, msg.ID, status)
		return
	}

	status.NodesTotal = adminStats.TotalNodes
	status.NodesOnline = adminStats.OnlineNodes
	status.PeersActive = adminStats.ActivePeers
	status.PeersPending = adminStats.PendingPeers

	h.sendToBot(bc, TypeBotStatusResult, msg.ID, status)
}

func (h *Hub) handleBotNodes(bc *BotConn, msg Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	nodes, err := h.nodes.ListEnabled(ctx)
	if err != nil {
		h.sendToBot(bc, TypeBotNodesResult, msg.ID, BotNodesResult{})
		return
	}

	botNodes := make([]BotNodeInfo, 0, len(nodes))
	for _, n := range nodes {
		botNodes = append(botNodes, BotNodeInfo{
			ID:       n.ID,
			Name:     n.Name,
			Location: n.Location,
			Online:   n.Online,
			ASN:      n.OurASN,
		})
	}

	h.sendToBot(bc, TypeBotNodesResult, msg.ID, BotNodesResult{Nodes: botNodes})
}

func (h *Hub) handleBotCommandStats(msg Message) {
	var payload BotCommandStatsPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}
	if payload.Command == "" {
		return
	}

	validCommands := map[string]bool{
		"ping": true, "trace": true, "status": true, "nodes": true, "start": true, "help": true,
	}
	if !validCommands[payload.Command] {
		return
	}
	if len(payload.Username) > 64 {
		payload.Username = payload.Username[:64]
	}
	payload.Username = strings.TrimSpace(payload.Username)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	h.bot.InsertCommandStat(ctx, &model.BotCommandStat{
		Command:  payload.Command,
		TgUserID: payload.TgUserID,
		Username: payload.Username,
		ChatID:   payload.ChatID,
	})
}

func (h *Hub) getOnlineNodes(specificNodeID string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if specificNodeID != "" {
		if _, ok := h.conns[specificNodeID]; ok {
			return []string{specificNodeID}
		}
		return nil
	}

	ids := make([]string, 0, len(h.conns))
	for id := range h.conns {
		ids = append(ids, id)
	}
	return ids
}

func (h *Hub) sendToBot(bc *BotConn, msgType string, msgID string, payload any) {
	msg := Message{Type: msgType, ID: msgID, Payload: payload}
	data, err := json.Marshal(msg)
	if err != nil {
		log.WithError(err).Error("marshal bot message")
		return
	}
	select {
	case bc.sendCh <- data:
	default:
		log.WithField("bot_id", bc.ID).Warn("bot send buffer full")
	}
}
