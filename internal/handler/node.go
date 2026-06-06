package handler

import (
	"net/http"

	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/sirupsen/logrus"
)

type NodeHandler struct {
	nodes repository.NodeRepository
}

func NewNodeHandler(nodes repository.NodeRepository) *NodeHandler {
	return &NodeHandler{nodes: nodes}
}

func (h *NodeHandler) ListPublic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	nodeList, err := h.nodes.ListEnabled(ctx)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch nodes")
		return
	}

	type nodePublic struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Location     string `json:"location"`
		PublicIP     string `json:"public_ip"`
		OurASN       int64  `json:"our_asn"`
		OurLLA       string `json:"our_lla"`
		OurWgPubkey  string `json:"our_wg_pubkey"`
		Online       bool   `json:"online"`
		Enabled      bool   `json:"enabled"`
		WgPortPrefix int    `json:"wg_port_prefix"`
	}

	nodes := make([]nodePublic, 0, len(nodeList))
	for _, n := range nodeList {
		nodes = append(nodes, nodePublic{
			ID:           n.ID,
			Name:         n.Name,
			Location:     n.Location,
			PublicIP:     n.PublicIP,
			OurASN:       n.OurASN,
			OurLLA:       n.OurLLA,
			OurWgPubkey:  n.OurWgPubkey,
			Online:       n.Online,
			Enabled:      n.Enabled,
			WgPortPrefix: n.WgPortPrefix,
		})
	}

	logrus.WithField("pkg", "handler.node").WithField("count", len(nodes)).Debug("ListPublic query result")

	JSON(w, http.StatusOK, nodes)
}
