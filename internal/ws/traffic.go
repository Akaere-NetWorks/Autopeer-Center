package ws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/akaere/autopeer-center/internal/clickhouse"
	"github.com/sirupsen/logrus"
)

// handleTrafficReport persists an agent traffic.report to ClickHouse. When the
// traffic store is not configured the report is dropped immediately — this is the
// gate that keeps center idle when CLICKHOUSE_URL is unset. nodeID is the
// authenticated connection's node; the payload's own node_id is ignored.
func (h *Hub) handleTrafficReport(nodeID string, data []byte) {
	if h.traffic == nil {
		return
	}

	var msg struct {
		Payload clickhouse.Report `json:"payload"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		log.WithError(err).WithField("node_id", nodeID).Error("traffic report unmarshal error")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := h.traffic.InsertReport(ctx, nodeID, &msg.Payload); err != nil {
		log.WithError(err).WithFields(logrus.Fields{
			"node_id":    nodeID,
			"interfaces": len(msg.Payload.Interfaces),
		}).Error("insert traffic report failed")
		return
	}
	log.WithFields(logrus.Fields{
		"node_id":    nodeID,
		"interfaces": len(msg.Payload.Interfaces),
	}).Debug("traffic report stored")
}
