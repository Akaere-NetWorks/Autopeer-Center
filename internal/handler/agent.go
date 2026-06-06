package handler

import (
	"database/sql"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/crypto"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("pkg", "handler.agent")

type AgentHandler struct {
	nodes          repository.NodeRepository
	releases       repository.ReleaseRepository
	hub            *ws.Hub
	centerKeyPair  *crypto.KeyPair
	allowedOrigins map[string]bool
	upgrader       websocket.Upgrader
	keyAuthMu      sync.Mutex
	keyAuthLast   map[string]time.Time
	keyAuthIPLast map[string]time.Time
}

func NewAgentHandler(nodes repository.NodeRepository, releases repository.ReleaseRepository, hub *ws.Hub, centerKeyPair *crypto.KeyPair, allowedOrigins string) *AgentHandler {
	originSet := make(map[string]bool)
	for _, o := range strings.Split(allowedOrigins, ",") {
		o = strings.TrimSpace(o)
		o = strings.TrimRight(o, "/")
		if o != "" {
			originSet[o] = true
		}
	}

	hub.SetCenterKeyPair(centerKeyPair)

	return &AgentHandler{
		nodes:          nodes,
		releases:       releases,
		hub:            hub,
		centerKeyPair:  centerKeyPair,
		allowedOrigins: originSet,
		keyAuthLast:    make(map[string]time.Time),
		keyAuthIPLast:  make(map[string]time.Time),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := strings.TrimRight(r.Header.Get("Origin"), "/")
				if origin == "" {
					if r.Header.Get("X-Agent-Token") != "" || r.Header.Get("X-Node-ID") != "" {
						return true
					}
					return false
				}
				return originSet[origin]
			},
		},
	}
}

func (h *AgentHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Agent-Token")
	nodeID := r.Header.Get("X-Node-ID")
	if nodeID == "" {
		nodeID = r.URL.Query().Get("node_id")
	}

	if token != "" {
		log.Debug("looking up agent token")

		node, err := h.nodes.GetByAgentToken(r.Context(), token)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		nodeID = node.ID

		log.WithField("node_id", nodeID).Debug("token validated, upgrading to websocket")
	} else if nodeID != "" {
		if len(nodeID) > 128 {
			nodeID = nodeID[:128]
		}
		if !h.checkKeyAuthRate(nodeID, r) {
			http.Error(w, "too many key-auth attempts", http.StatusTooManyRequests)
			return
		}
		pubkey, err := h.nodes.GetAgentPubkey(r.Context(), nodeID)
		if err != nil || pubkey == "" {
			http.Error(w, "key auth not available for this node", http.StatusUnauthorized)
			return
		}
		log.WithField("node_id", nodeID).Debug("key-auth mode, upgrading to websocket")
	} else {
		http.Error(w, "missing token or node_id", http.StatusUnauthorized)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.WithError(err).Error("websocket upgrade failed")
		return
	}

	log.WithField("node_id", nodeID).Info("agent connected")

	ac := h.hub.Register(nodeID, conn)

	go ac.WritePump()
	ac.ReadPump(h.hub)
}

func (h *AgentHandler) checkKeyAuthRate(nodeID string, r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)

	h.keyAuthMu.Lock()
	defer h.keyAuthMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)

	if last, ok := h.keyAuthIPLast[host]; ok && now.Sub(last) < 10*time.Second {
		return false
	}
	h.keyAuthIPLast[host] = now

	freshIP := make(map[string]time.Time)
	for k, t := range h.keyAuthIPLast {
		if t.After(cutoff) {
			freshIP[k] = t
		}
	}
	h.keyAuthIPLast = freshIP

	key := host + "/" + nodeID
	fresh := make(map[string]time.Time)
	for k, t := range h.keyAuthLast {
		if t.After(cutoff) {
			fresh[k] = t
		}
	}
	if len(fresh) > 10000 {
		fresh = make(map[string]time.Time)
	}
	h.keyAuthLast = fresh
	if last, ok := fresh[key]; ok && now.Sub(last) < 10*time.Second {
		return false
	}
	fresh[key] = now
	return true
}

func (h *AgentHandler) HandleDownload(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Agent-Token")
	nodeIDHeader := r.Header.Get("X-Node-ID")
	version := r.URL.Query().Get("version")
	osName := r.URL.Query().Get("os")
	arch := r.URL.Query().Get("arch")
	if version == "" {
		http.Error(w, "missing version", http.StatusBadRequest)
		return
	}
	if token == "" && nodeIDHeader == "" {
		http.Error(w, "missing token or node_id", http.StatusBadRequest)
		return
	}
	if osName == "" {
		osName = "linux"
	}
	if arch == "" {
		arch = "amd64"
	}

	log.WithFields(logrus.Fields{
		"version": version,
		"os":      osName,
		"arch":    arch,
	}).Debug("agent download request")

	var nodeID string
	if token != "" {
		node, err := h.nodes.GetByAgentToken(r.Context(), token)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		nodeID = node.ID
	} else {
		node, err := h.nodes.GetByID(r.Context(), nodeIDHeader)
		if err != nil || !node.Enabled {
			http.Error(w, "invalid node_id or key-auth not configured", http.StatusUnauthorized)
			return
		}
		if node.AgentPubkey == "" {
			http.Error(w, "invalid node_id or key-auth not configured", http.StatusUnauthorized)
			return
		}
		nodeID = node.ID
	}

	log.WithFields(logrus.Fields{
		"node_id": nodeID,
		"version": version,
		"os":      osName,
		"arch":    arch,
	}).Debug("node validated for download")

	release, err := h.releases.GetByVersion(r.Context(), version, osName, arch)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "release not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	f, err := os.Open(release.Path)
	if err != nil {
		http.Error(w, "release file not found on disk", http.StatusNotFound)
		return
	}
	defer f.Close()

	stat, _ := f.Stat()
	w.Header().Set("X-Checksum-SHA256", release.SHA256)
	w.Header().Set("Content-Disposition", "attachment; filename=autopeer-agent")
	log.WithFields(logrus.Fields{"node_id": nodeID, "version": version}).Info("agent download")
	http.ServeContent(w, r, "autopeer-agent", stat.ModTime(), f)
}
