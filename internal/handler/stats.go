package handler

import (
	"net/http"

	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/sirupsen/logrus"
)

var statsLog = logrus.WithField("pkg", "handler.stats")

type StatsHandler struct {
	stats repository.StatsRepository
}

func NewStatsHandler(stats repository.StatsRepository) *StatsHandler {
	return &StatsHandler{stats: stats}
}

func (h *StatsHandler) Public(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pub, err := h.stats.Public(ctx)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch stats")
		return
	}

	type nodeStats struct {
		NodeID      string  `json:"node_id"`
		Name        string  `json:"name"`
		PeersActive int     `json:"peers_active"`
		AvgRttMs    float64 `json:"avg_rtt_ms"`
		RxBytes24h  int64   `json:"rx_bytes_24h"`
		TxBytes24h  int64   `json:"tx_bytes_24h"`
	}

	nodeStatsList, err := h.stats.PublicNodeStats(ctx)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch node stats")
		return
	}

	nodes := make([]nodeStats, 0, len(nodeStatsList))
	for _, ns := range nodeStatsList {
		var avgRtt float64
		if ns.AvgRtt != nil {
			avgRtt = *ns.AvgRtt
		}
		nodes = append(nodes, nodeStats{
			NodeID:      ns.ID,
			Name:        ns.Name,
			PeersActive: ns.ActivePeers,
			AvgRttMs:    avgRtt,
			RxBytes24h:  ns.RxBytes24h,
			TxBytes24h:  ns.TxBytes24h,
		})
	}

	statsLog.WithFields(logrus.Fields{
		"nodes_online":  pub.OnlineNodes,
		"peers_active":  pub.ActivePeers,
		"avg_rtt_ms":    pub.AvgRtt,
		"node_count":    len(nodes),
	}).Debug("Public stats fetched")

	JSON(w, http.StatusOK, map[string]any{
		"nodes_online":   pub.OnlineNodes,
		"peers_active":   pub.ActivePeers,
		"total_rx_bytes": pub.RxBytes24h,
		"total_tx_bytes": pub.TxBytes24h,
		"avg_rtt_ms":     pub.AvgRtt,
		"nodes":          nodes,
	})
}

func (h *StatsHandler) Admin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	adm, err := h.stats.Admin(ctx)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch admin stats")
		return
	}

	statsLog.WithFields(logrus.Fields{
		"peers_active":    adm.ActivePeers,
		"peers_pending":   adm.PendingPeers,
		"peers_suspended": adm.SuspendedPeers,
		"peers_rejected":  adm.RejectedPeers,
		"nodes_online":    adm.OnlineNodes,
		"nodes_total":     adm.TotalNodes,
		"new_today":       adm.PeersLast24h,
	}).Debug("Admin stats fetched")

	JSON(w, http.StatusOK, map[string]any{
		"peers_active":    adm.ActivePeers,
		"peers_pending":   adm.PendingPeers,
		"peers_suspended": adm.SuspendedPeers,
		"peers_rejected":  adm.RejectedPeers,
		"nodes_online":    adm.OnlineNodes,
		"nodes_total":     adm.TotalNodes,
		"new_today":       adm.PeersLast24h,
	})
}
