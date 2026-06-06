package handler

import (
	"net/http"
	"strings"

	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

var botLog = logrus.WithField("pkg", "handler.bot")

type BotHandler struct {
	hub            *ws.Hub
	allowedOrigins map[string]bool
	upgrader       websocket.Upgrader
}

func NewBotHandler(hub *ws.Hub, allowedOrigins string) *BotHandler {
	originSet := make(map[string]bool)
	for _, o := range strings.Split(allowedOrigins, ",") {
		o = strings.TrimSpace(o)
		o = strings.TrimRight(o, "/")
		if o != "" {
			originSet[o] = true
		}
	}

	return &BotHandler{
		hub:            hub,
		allowedOrigins: originSet,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := strings.TrimRight(r.Header.Get("Origin"), "/")
				if origin == "" {
					return false
				}
				return originSet[origin]
			},
		},
	}
}

func (h *BotHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		botLog.WithError(err).Error("bot websocket upgrade failed")
		return
	}

	bc := ws.NewBotConn(conn)

	botLog.WithField("bot_id", bc.ID).Info("bot connected, waiting for auth")

	go bc.WritePump()
	bc.ReadPump(h.hub)
}
