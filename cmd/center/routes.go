package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/akaere/autopeer-center/internal/handler"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
)

// setupRoutes applies global middleware and registers all application routes onto r.
func setupRoutes(r chi.Router, deps *AppDeps) {
	cfg := deps.Cfg

	// ── Trusted-proxy / RealIP middleware ─────────────────────────────────────
	if len(deps.TrustedNets) > 0 {
		r.Use(chimw.RealIP)
		log.Info("TRUSTED_PROXY_CIDR configured, RealIP middleware enabled")
	} else if cfg.CORSOrigin != "" &&
		!strings.HasPrefix(cfg.CORSOrigin, "http://localhost") &&
		!strings.HasPrefix(cfg.CORSOrigin, "http://127.0.0.1") {
		log.Warn("TRUSTED_PROXY_CIDR not set but CORS_ORIGIN suggests production deployment — if behind a reverse proxy, IP-based rate limiting and Turnstile remoteip will use the proxy IP instead of the client IP. Set TRUSTED_PROXY_CIDR to your proxy's IP range.")
	} else {
		log.Warn("TRUSTED_PROXY_CIDR not set, X-Real-IP/X-Forwarded-For headers will be ignored for rate limiting")
	}

	// ── Global middleware ─────────────────────────────────────────────────────
	r.Use(middleware.RequestID)
	r.Use(middleware.BodyLimit(1 << 20))
	r.Use(middleware.CORS(cfg.CORSOrigin))
	r.Use(middleware.RequestLog(func(method, path string, status int, ip string, durationMs int64) {
		if deps.Q.Available() {
			qctx, cancel := context.WithTimeout(context.Background(), time.Second)
			err := deps.Q.EnqueueRequestLog(qctx, &model.RequestLog{Method: method, Path: path, StatusCode: status, IP: ip, DurationMs: durationMs})
			cancel()
			if err == nil {
				return
			}
			log.WithError(err).Warn("failed to enqueue request log, falling back to sync write")
		}
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := deps.MetricsRepo.LogRequest(syncCtx, &model.RequestLog{Method: method, Path: path, StatusCode: status, IP: ip, DurationMs: durationMs})
		syncCancel()
		if err != nil {
			log.WithError(err).WithFields(logrus.Fields{
				"method": method, "path": path, "status": status,
			}).Warn("failed to log request")
		}
	}, deps.TrustedNets))
	r.Use(middleware.Recover)

	// ── Utility routes ────────────────────────────────────────────────────────
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("OK"))
	})
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not_found","message":"The requested resource does not exist"}`))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"error":"method_not_allowed","message":"Method not allowed"}`))
	})

	// ── /api/v1 ───────────────────────────────────────────────────────────────
	r.Route("/api/v1", func(r chi.Router) {
		// Stripe-style header API versioning (Autopeer-Version). Orthogonal to
		// the URL /v1; resolves/echoes the version for every /api/v1 route and
		// rejects unknown versions with 400. Only handlers that call
		// JSONVersioned actually transform their output.
		r.Use(middleware.APIVersion)

		// Public endpoints
		r.Get("/nodes", deps.NodeHandler.ListPublic)
		r.Get("/registry/asn/{asn}", deps.RegistryHandler.LookupASN)
		r.Get("/stats", deps.StatsHandler.Public)

		r.Post("/auth/user/request-code", deps.AuthHandler.RequestCode)
		r.Post("/auth/user/verify-code", deps.AuthHandler.VerifyCode)
		r.Post("/auth/user/request-gpg-challenge", deps.AuthHandler.RequestGPGChallenge)
		r.Post("/auth/user/verify-gpg", deps.AuthHandler.VerifyGPGSignature)
		r.Post("/auth/user/check-gpg-availability", deps.AuthHandler.CheckGPGAvailability)
		r.Post("/auth/device/code", deps.AuthHandler.DeviceCode)
		r.Post("/auth/device/token", deps.AuthHandler.DeviceToken)
		r.Get("/auth/user/login-status", deps.AuthHandler.LoginStatus)
		r.Get("/auth/turnstile-config", deps.AuthHandler.GetTurnstileConfig)
		r.Get("/user/peers/creation-status", deps.PeerHandler.CreationStatus)

		r.Post("/auth/admin/login", deps.AuthHandler.AdminLogin)
		r.Post("/auth/user/passkey/begin", deps.PasskeyHandler.LoginBegin)
		r.Post("/auth/user/passkey/finish", deps.PasskeyHandler.LoginFinish)
		r.Post("/auth/refresh", deps.AuthHandler.Refresh)

		// Any authenticated (user or admin) — refresh/logout/device flows
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAnyAuthWithSessions(cfg, deps.AuthRepo))
			r.Post("/auth/logout", deps.AuthHandler.Logout)
			r.Get("/auth/device/request", deps.AuthHandler.DeviceRequest)
			r.Post("/auth/device/authorize", deps.AuthHandler.DeviceAuthorize)
		})

		// User endpoints
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireUserWithSessions(cfg, deps.AuthRepo))
			r.Get("/user/peers", deps.PeerHandler.List)
			r.Post("/user/peers", deps.PeerHandler.Create)
			r.Get("/user/peers/summary", deps.PeerHandler.Summary)
			r.Get("/user/peers/{id}", deps.PeerHandler.Get)
			r.Get("/user/peers/{id}/metrics", deps.PeerHandler.Metrics)
			r.Put("/user/peers/{id}", deps.PeerHandler.Update)
			r.Delete("/user/peers/{id}", deps.PeerHandler.Delete)
			r.Post("/user/looking-glass/run", deps.LookingGlassHandler.RunQuery)
			r.Get("/user/audit", deps.AdminHandler.ListUserAuditLogs)
			r.Get("/user/devices", deps.AuthHandler.ListUserDevices)
			r.Delete("/user/devices", deps.AuthHandler.RevokeOtherUserDevices)
			r.Delete("/user/devices/{id}", deps.AuthHandler.RevokeUserDevice)

			// Email notification preferences
			r.Get("/user/email-preferences", deps.EmailPrefHandler.Get)
			r.Put("/user/email-preferences", deps.EmailPrefHandler.Update)
			r.Get("/user/notification-preferences", deps.EmailPrefHandler.GetNotifications)
			r.Put("/user/notification-preferences", deps.EmailPrefHandler.UpdateNotifications)

			// Telegram
			r.Get("/user/telegram/binding", deps.TelegramBindHandler.GetBinding)
			r.Post("/user/telegram/bind-token", deps.TelegramBindHandler.CreateBindToken)
			r.Delete("/user/telegram/binding", deps.TelegramBindHandler.DeleteBinding)
			r.Get("/user/telegram/notification-preferences", deps.TelegramBindHandler.GetTelegramNotifications)
			r.Put("/user/telegram/notification-preferences", deps.TelegramBindHandler.UpdateTelegramNotifications)

			// MCP API key management (JWT only — cannot use MCP keys to manage MCP keys)
			r.Get("/user/mcp-keys", deps.MCPKeyHandler.List)
			r.Post("/user/mcp-keys", deps.MCPKeyHandler.Create)
			r.Delete("/user/mcp-keys/{id}", deps.MCPKeyHandler.Delete)

			// MCP audit log (user view — own ASN only)
			r.Get("/user/mcp-audit-logs", handler.ListMCPAuditLogs(deps.MCPRepo))
			r.Get("/user/assistant/auth", handler.AssistantAuthCheck)
			r.Post("/user/assistant/tools/approval", deps.MCPHandler.HandleAssistantToolApproval)
			r.Post("/user/assistant/tools/call", deps.MCPHandler.HandleAssistantToolCall)

			// Passkey/WebAuthn management
			r.Get("/passkey/status", deps.PasskeyHandler.Status)
			r.Post("/passkey/register/begin", deps.PasskeyHandler.RegisterBegin)
			r.Post("/passkey/register/finish", deps.PasskeyHandler.RegisterFinish)
			r.Get("/user/passkeys", deps.PasskeyHandler.List)
			r.Delete("/user/passkeys/{id}", deps.PasskeyHandler.Delete)
		})

		// MCP endpoints — MCP API key only (ap_mcp_... prefix required)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireMCPKey(deps.MCPRepo))
			r.Get("/mcp", deps.MCPHandler.HandleSSE)
			r.Post("/mcp", deps.MCPHandler.HandleMessage)
		})

		// Admin MCP endpoints — admin MCP API key only (ap_mcp_admin_... prefix required)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAdminMCPKey(deps.MCPRepo))
			r.Get("/admin/mcp", deps.AdminMCPHandler.HandleSSE)
			r.Post("/admin/mcp", deps.AdminMCPHandler.HandleMessage)
		})

		// Admin endpoints
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAdminWithSessions(cfg, deps.AuthRepo))
			r.Get("/admin/peers", deps.AdminHandler.ListPeers)
			r.Get("/admin/peers/export", deps.AdminHandler.ExportPeers)
			r.Post("/admin/peers/import", deps.AdminHandler.ImportPeers)
			r.Get("/admin/peers/{id}", deps.AdminHandler.GetPeer)
			r.Get("/admin/peers/{id}/metrics", deps.AdminHandler.PeerMetrics)
			r.Get("/admin/peers/{id}/traffic", deps.TrafficHandler.PeerTraffic)
			r.Post("/admin/peers/{id}/approve", deps.AdminHandler.ApprovePeer)
			r.Post("/admin/peers/{id}/reject", deps.AdminHandler.RejectPeer)
			r.Post("/admin/peers/{id}/suspend", deps.AdminHandler.SuspendPeer)
			r.Post("/admin/peers/{id}/unsuspend", deps.AdminHandler.UnsuspendPeer)
			r.Delete("/admin/peers/{id}", deps.AdminHandler.DeletePeer)
			r.Put("/admin/peers/{id}", deps.AdminHandler.UpdatePeer)
			r.Put("/admin/peers/{id}/contact-email", deps.AdminHandler.UpdateContactEmail)
			r.Get("/admin/nodes", deps.AdminHandler.ListNodes)
			r.Post("/admin/nodes", deps.AdminHandler.CreateNode)
			r.Put("/admin/nodes/{id}", deps.AdminHandler.UpdateNode)
			r.Delete("/admin/nodes/{id}", deps.AdminHandler.DeleteNode)
			r.Get("/admin/nodes/{id}/traffic", deps.TrafficHandler.NodeTraffic)
			r.Post("/admin/nodes/{id}/import", deps.AdminHandler.ImportNodePeers)
			r.Post("/admin/nodes/{id}/bird-refresh", deps.AdminHandler.RefreshBirdDetails)
			r.Post("/admin/nodes/{id}/update", deps.AdminHandler.UpdateAgent)
			r.Post("/admin/nodes/{id}/rollback", deps.AdminHandler.RollbackAgent)
			r.Post("/admin/nodes/{id}/regenerate-token", deps.AdminHandler.RegenerateToken)
			r.Post("/admin/nodes/{id}/reset-pubkey", deps.AdminHandler.ResetPubkey)
			r.Get("/admin/releases", deps.AdminHandler.ListReleases)
			r.Post("/admin/releases", deps.AdminHandler.UploadRelease)
			r.Delete("/admin/releases/{version}", deps.AdminHandler.DeleteRelease)
			r.Get("/admin/notifications", deps.AdminHandler.GetNotificationSettings)
			r.Put("/admin/notifications", deps.AdminHandler.UpdateNotificationSetting)
			r.Get("/admin/audit", deps.AdminHandler.ListAuditLogs)
			r.Get("/admin/stats", deps.StatsHandler.Admin)

			// Flap agent (flapalerted-agent allowlist) management
			r.Get("/admin/flap/agents", deps.AdminFlapHandler.List)
			r.Post("/admin/flap/agents", deps.AdminFlapHandler.Create)
			r.Put("/admin/flap/agents/{id}", deps.AdminFlapHandler.Update)
			r.Delete("/admin/flap/agents/{id}", deps.AdminFlapHandler.Delete)
			r.Post("/admin/flap/agents/{id}/regenerate-token", deps.AdminFlapHandler.RegenerateToken)
			r.Post("/admin/flap/agents/{id}/reset-pubkey", deps.AdminFlapHandler.ResetPubkey)

			r.Get("/admin/bot/settings", deps.AdminHandler.GetBotSettings)
			r.Put("/admin/bot/settings", deps.AdminHandler.UpdateBotSetting)
			r.Post("/admin/bot/token/reset", deps.AdminHandler.ResetBotToken)
			r.Get("/admin/bot/stats", deps.AdminHandler.GetBotStats)
			r.Get("/admin/bot/commands", deps.AdminHandler.GetBotRecentCommands)
			r.Get("/admin/bot/blocked", deps.AdminHandler.ListBotBlockedUsers)
			r.Post("/admin/bot/blocked", deps.AdminHandler.BlockBotUser)
			r.Delete("/admin/bot/blocked/{id}", deps.AdminHandler.UnblockBotUser)
			r.Get("/admin/bot/export", deps.AdminHandler.ExportBotSettings)

			r.Get("/admin/settings", deps.AdminHandler.GetSiteSettings)
			r.Put("/admin/settings", deps.AdminHandler.UpdateSiteSetting)
			r.Post("/admin/auth/login-as", deps.AuthHandler.AdminLoginAs)
			r.Get("/admin/auth/devices", deps.AuthHandler.ListAdminDevices)
			r.Delete("/admin/auth/devices/{id}", deps.AuthHandler.RevokeAdminDevice)
			r.Post("/admin/test/email", deps.AdminHandler.TestEmail)

			// Admin MCP key management (own admin keys)
			r.Get("/admin/mcp-keys", deps.AdminMCPKeyHandler.ListAdminKeys)
			r.Post("/admin/mcp-keys", deps.AdminMCPKeyHandler.CreateAdminKey)
			r.Delete("/admin/mcp-keys/{id}", deps.AdminMCPKeyHandler.DeleteAdminKey)

			// Admin management of user MCP keys
			r.Get("/admin/user-mcp-keys", deps.AdminMCPKeyHandler.ListUserMCPKeys)
			r.Delete("/admin/user-mcp-keys/{id}", deps.AdminMCPKeyHandler.ForceDeleteUserMCPKey)

			// Admin MCP audit logs
			r.Get("/admin/mcp-audit-logs", handler.AdminListMCPAuditLogs(deps.MCPRepo))

			r.Get("/admin/system-status", deps.SystemStatusHandler.Get)
			r.Get("/admin/system-status/db-tables", deps.SystemStatusHandler.GetDBTables)
			r.Post("/admin/system-status/rotate-tables", deps.SystemStatusHandler.RotateTables)

			// Queue monitor
			r.Route("/admin/queue", func(r chi.Router) {
				r.Get("/overview", deps.QueueMonitorHandler.Overview)
				r.Get("/queues", deps.QueueMonitorHandler.ListQueues)
				r.Get("/queues/{queueName}", deps.QueueMonitorHandler.GetQueue)
				r.Get("/queues/{queueName}/tasks", deps.QueueMonitorHandler.ListTasks)
				r.Get("/queues/{queueName}/tasks/{taskID}", deps.QueueMonitorHandler.GetTask)
				r.Get("/servers", deps.QueueMonitorHandler.ListServers)
				r.Get("/scheduler", deps.QueueMonitorHandler.ListSchedulerEntries)
				r.Get("/scheduler/{entryID}/events", deps.QueueMonitorHandler.ListSchedulerEvents)
				r.Get("/history/{queueName}", deps.QueueMonitorHandler.QueueHistory)

				if !cfg.AsynqReadonlyMonitor {
					r.Delete("/queues/{queueName}/tasks/{taskID}", deps.QueueMonitorHandler.DeleteTask)
					r.Post("/queues/{queueName}/tasks/{taskID}:run", deps.QueueMonitorHandler.RunTask)
					r.Post("/queues/{queueName}/tasks/{taskID}:archive", deps.QueueMonitorHandler.ArchiveTask)
					r.Delete("/queues/{queueName}/tasks:delete_all", deps.QueueMonitorHandler.DeleteAllTasks)
					r.Post("/queues/{queueName}:pause", deps.QueueMonitorHandler.PauseQueue)
					r.Post("/queues/{queueName}:resume", deps.QueueMonitorHandler.ResumeQueue)
					r.Post("/active-tasks/{taskID}:cancel", deps.QueueMonitorHandler.CancelActiveTask)
				}
			})
		})

		// Agent & bot WebSocket
		r.Get("/agent/ws", deps.AgentHandler.HandleWebSocket)
		r.Get("/agent/download", deps.AgentHandler.HandleDownload)
		r.Get("/bot/ws", deps.BotHandler.HandleWebSocket)

		// Flap (BGP route-flap monitor) — public endpoints + dedicated agent WS.
		// Center holds no flap state; it relays to the connected flapalerted-agent.
		r.Get("/flap/agents", deps.FlapHandler.ListAgents)
		r.Get("/flap/snapshot", deps.FlapHandler.Snapshot)
		r.Get("/flap/prefix", deps.FlapHandler.Prefix)
		r.Get("/flap/metrics", deps.FlapHandler.Metrics)
		r.Get("/flap/stream", deps.FlapHandler.Stream)
		r.Get("/flap/agent/ws", deps.FlapHandler.HandleWebSocket)
	})

	log.Debug("router setup complete")
}
