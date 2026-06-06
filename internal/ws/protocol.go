package ws

// Message types for center <-> agent communication
const (
	TypePeerAdd       = "peer.add"
	TypePeerRemove    = "peer.remove"
	TypeStatusRequest = "status.request"
	TypeResponse      = "response"
	TypeHeartbeat     = "heartbeat"
	TypeStatusResp    = "status.response"
	TypePeersSync     = "peers.sync"
	TypePeersImport   = "peers.import"
	TypeBirdDetails   = "bird.details"
	TypeAgentUpdate   = "agent.update"
	TypeAgentUpdating = "agent.updating"
	TypeAgentResume   = "agent.resume"
	TypeAgentRollback = "agent.rollback"
	TypeKeyInit       = "key.init"
	TypeKeyInitAck    = "key.init_ack"
	TypeKeyAuth       = "key.auth"
	TypeKeyAuthAck    = "key.auth_ack"

	TypeBotAuth         = "bot.auth"
	TypeBotAuthAck      = "bot.auth_ack"
	TypeBotPing         = "bot.ping"
	TypeBotPingResult   = "bot.ping_result"
	TypeBotTrace        = "bot.trace"
	TypeBotTraceResult  = "bot.trace_result"
	TypeBotStatus       = "bot.status"
	TypeBotStatusResult = "bot.status_result"
	TypeBotNodes        = "bot.nodes"
	TypeBotNodesResult  = "bot.nodes_result"

	TypeNetworkPing     = "network.ping"
	TypeNetworkTrace    = "network.trace"
	TypeNetworkMTR      = "network.mtr"
	TypeNetworkBGPRoute = "network.bgp_route"

	TypeBirdDisable = "bird.disable"
	TypeBirdEnable  = "bird.enable"

	TypeBotSettingsUpdate = "bot.settings_update"
	TypeBotCommandStats   = "bot.command_stats"

	TypeBotBindRequest  = "bot.bind_request"
	TypeBotBindPending  = "bot.bind_pending"
	TypeBotBindVerify   = "bot.bind_verify"
	TypeBotBindResult   = "bot.bind_result"
	TypeBotWebBind      = "bot.web_bind"
	TypeBotUnbind       = "bot.unbind"
	TypeBotUnbindResult = "bot.unbind_result"
	TypeBotWhoami       = "bot.whoami"
	TypeBotWhoamiResult = "bot.whoami_result"

	TypeBotNotify = "bot.notify"

	TypeBotUserPeers             = "bot.user_peers"
	TypeBotUserPeersResult       = "bot.user_peers_result"
	TypeBotUserPeerDetail        = "bot.user_peer_detail"
	TypeBotUserPeerDetailResult  = "bot.user_peer_detail_result"
	TypeBotUserPeerCreate        = "bot.user_peer_create"
	TypeBotUserPeerCreateResult  = "bot.user_peer_create_result"
	TypeBotUserPeerDelete        = "bot.user_peer_delete"
	TypeBotUserPeerDeleteResult  = "bot.user_peer_delete_result"
	TypeBotUserPeerMetrics       = "bot.user_peer_metrics"
	TypeBotUserPeerMetricsResult = "bot.user_peer_metrics_result"

	TypeBotAdminPeers            = "bot.admin_peers"
	TypeBotAdminPeersResult      = "bot.admin_peers_result"
	TypeBotAdminPeerAction       = "bot.admin_peer_action"
	TypeBotAdminPeerActionResult = "bot.admin_peer_action_result"
	TypeBotAdminNodes            = "bot.admin_nodes"
	TypeBotAdminNodesResult      = "bot.admin_nodes_result"
	TypeBotAdminNodeDetail       = "bot.admin_node_detail"
	TypeBotAdminNodeDetailResult = "bot.admin_node_detail_result"
	TypeBotAdminNodeUpdate       = "bot.admin_node_update"
	TypeBotAdminNodeUpdateResult = "bot.admin_node_update_result"
	TypeBotAdminAudit            = "bot.admin_audit"
	TypeBotAdminAuditResult      = "bot.admin_audit_result"
)

type Message struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Payload any    `json:"payload,omitempty"`
	Success *bool  `json:"success,omitempty"`
	Error   string `json:"error,omitempty"`
}

type PeerAddPayload struct {
	PeerID         string  `json:"peer_id"`
	ASN            int64   `json:"asn"`
	RemoteEndpoint string  `json:"remote_endpoint"`
	RemoteWgPubkey string  `json:"remote_wg_pubkey"`
	RemoteLLA      string  `json:"remote_lla"`
	ListenPort     int     `json:"listen_port"`
	WgInterface    string  `json:"wg_interface"`
	MTU            *int    `json:"mtu,omitempty"`
	WgPreSharedKey *string `json:"wg_preshared_key,omitempty"`
}

type PeerRemovePayload struct {
	PeerID string `json:"peer_id"`
	ASN    int64  `json:"asn"`
}

type NodeMetrics struct {
	MemAllocMB   uint64 `json:"mem_alloc_mb,omitempty"`
	MemSysMB     uint64 `json:"mem_sys_mb,omitempty"`
	NumGoroutine int    `json:"num_goroutine,omitempty"`
	UptimeSecs   int64  `json:"uptime_secs,omitempty"`
}

type HeartbeatPayload struct {
	NodeID      string          `json:"node_id"`
	Timestamp   string          `json:"timestamp"`
	Peers       []PeerHeartbeat `json:"peers"`
	Version     string          `json:"version,omitempty"`
	NodeMetrics *NodeMetrics    `json:"node_metrics,omitempty"`
}

type PeerHeartbeat struct {
	PeerID           string   `json:"peer_id"`
	ASN              int64    `json:"asn"`
	WgRxBytes        int64    `json:"wg_rx_bytes"`
	WgTxBytes        int64    `json:"wg_tx_bytes"`
	WgLastHandshake  string   `json:"wg_last_handshake"`
	BGPState         string   `json:"bgp_state"`
	RttMs            *float64 `json:"rtt_ms"`
	RoutesImported   *int     `json:"routes_imported,omitempty"`
	RoutesExported   *int     `json:"routes_exported,omitempty"`
	RoutesPreferred  *int     `json:"routes_preferred,omitempty"`
	BGPUptimeSecs    *int     `json:"bgp_uptime_secs,omitempty"`
	WgActualEndpoint string   `json:"wg_actual_endpoint,omitempty"`
}

type BirdControlPayload struct {
	PeerID    string `json:"peer_id"`
	ProtoName string `json:"proto_name"`
}

type PeerSyncEntry struct {
	PeerID             string  `json:"peer_id"`
	ASN                int64   `json:"asn"`
	RemoteEndpoint     string  `json:"remote_endpoint"`
	RemoteWgPubkey     string  `json:"remote_wg_pubkey"`
	RemoteLLA          string  `json:"remote_lla"`
	ListenPort         int     `json:"listen_port"`
	WgInterface        string  `json:"wg_interface"`
	BgpProtoName       string  `json:"bgp_proto_name"`
	BirdConfigFilename string  `json:"bird_config_filename"`
	MTU                *int    `json:"mtu,omitempty"`
	WgPreSharedKey     *string `json:"wg_preshared_key,omitempty"`
}

type PeersSyncPayload struct {
	Peers []PeerSyncEntry `json:"peers"`
}

type KeyInitPayload struct {
	Pubkey string `json:"pubkey"`
}

type KeyInitAckPayload struct {
	Pubkey string `json:"pubkey"`
	Nonce  []byte `json:"nonce"`
}

type KeyAuthPayload struct {
	NodeID string `json:"node_id"`
	Pubkey string `json:"pubkey"`
	Nonce  []byte `json:"nonce"`
	Proof  []byte `json:"proof"`
}

type KeyAuthAckPayload struct {
	Pubkey      string `json:"pubkey"`
	AuthNonce   []byte `json:"auth_nonce"`
	CenterNonce []byte `json:"center_nonce,omitempty"`
}

type BotAuthPayload struct {
	Token string `json:"token"`
}

type BotAuthAckPayload struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type BotCommandPayload struct {
	Target string `json:"target"`
	NodeID string `json:"node_id,omitempty"`
}

type PingResultEntry struct {
	NodeID   string    `json:"node_id"`
	NodeName string    `json:"node_name"`
	Success  bool      `json:"success"`
	Sent     int       `json:"sent,omitempty"`
	Received int       `json:"received,omitempty"`
	Loss     float64   `json:"loss_percent,omitempty"`
	MinRTT   float64   `json:"min_rtt_ms,omitempty"`
	AvgRTT   float64   `json:"avg_rtt_ms,omitempty"`
	MaxRTT   float64   `json:"max_rtt_ms,omitempty"`
	RTTs     []float64 `json:"rtts,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type BotPingResult struct {
	Target  string            `json:"target"`
	Results []PingResultEntry `json:"results"`
}

type TraceHopEntry struct {
	TTL  int       `json:"ttl"`
	IP   string    `json:"ip"`
	RTTs []float64 `json:"rtts"`
}

type TraceResultEntry struct {
	NodeID   string          `json:"node_id"`
	NodeName string          `json:"node_name"`
	Success  bool            `json:"success"`
	Hops     []TraceHopEntry `json:"hops,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type BotTraceResult struct {
	Target  string             `json:"target"`
	Results []TraceResultEntry `json:"results"`
}

type BotStatusResult struct {
	NodesTotal   int `json:"nodes_total"`
	NodesOnline  int `json:"nodes_online"`
	PeersActive  int `json:"peers_active"`
	PeersPending int `json:"peers_pending"`
}

type BotNodeInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Online   bool   `json:"online"`
	ASN      int64  `json:"asn"`
}

type BotNodesResult struct {
	Nodes []BotNodeInfo `json:"nodes"`
}

type NetworkPingPayload struct {
	Target string `json:"target"`
	Count  int    `json:"count,omitempty"`
}

type NetworkTracePayload struct {
	Target  string `json:"target"`
	MaxHops int    `json:"max_hops,omitempty"`
}

type NetworkMTRPayload struct {
	Target string `json:"target"`
	Cycles int    `json:"cycles,omitempty"`
}

type NetworkBGPRoutePayload struct {
	Target   string `json:"target"`
	Detailed bool   `json:"detailed,omitempty"`
}

type BotSettingsPayload struct {
	Settings map[string]string `json:"settings"`
}

type BotCommandStatsPayload struct {
	Command  string `json:"command"`
	TgUserID int64  `json:"tg_user_id"`
	Username string `json:"username"`
	ChatID   int64  `json:"chat_id"`
}

type BotBindRequestPayload struct {
	TgUserID   int64  `json:"tg_user_id"`
	TgUsername string `json:"tg_username"`
	ASN        int64  `json:"asn"`
}

type BotBindPendingPayload struct {
	MaskedEmail string `json:"masked_email"`
	ExpiresIn   int    `json:"expires_in"`
}

type BotBindVerifyPayload struct {
	TgUserID   int64  `json:"tg_user_id"`
	TgUsername string `json:"tg_username"`
	ASN        int64  `json:"asn"`
	Code       string `json:"code"`
}

type BotBindResultPayload struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

type BotWebBindPayload struct {
	TgUserID   int64  `json:"tg_user_id"`
	TgUsername string `json:"tg_username"`
	Token      string `json:"token"`
}

type BotUnbindPayload struct {
	TgUserID int64 `json:"tg_user_id"`
}

type BotWhoamiPayload struct {
	TgUserID int64 `json:"tg_user_id"`
}

type BotWhoamiResultPayload struct {
	Bound      bool   `json:"bound"`
	ASN        int64  `json:"asn,omitempty"`
	TgUsername string `json:"tg_username,omitempty"`
	IsAdmin    bool   `json:"is_admin,omitempty"`
	BoundVia   string `json:"bound_via,omitempty"`
	BoundAt    string `json:"bound_at,omitempty"`
}

type BotNotifyPayload struct {
	Channel string          `json:"channel"`
	Target  BotNotifyTarget `json:"target"`
	Event   string          `json:"event"`
	Data    interface{}     `json:"data,omitempty"`
}

type BotNotifyTarget struct {
	TgUserID  int64 `json:"tg_user_id,omitempty"`
	AdminChat bool  `json:"admin_chat,omitempty"`
}

type BotUserPeersPayload struct {
	TgUserID int64 `json:"tg_user_id"`
}

type BotUserPeersResultPayload struct {
	Peers []BotPeerInfo `json:"peers"`
}

type BotPeerInfo struct {
	ID           string  `json:"id"`
	NodeID       string  `json:"node_id"`
	NodeName     string  `json:"node_name"`
	NodeLocation string  `json:"node_location"`
	RemoteASN    int64   `json:"remote_asn"`
	Status       string  `json:"status"`
	RejectReason *string `json:"reject_reason,omitempty"`
	CreatedAt    string  `json:"created_at"`
	WgListenPort int     `json:"wg_listen_port"`
}

type BotUserPeerDetailPayload struct {
	TgUserID int64  `json:"tg_user_id"`
	PeerID   string `json:"peer_id"`
}

type BotUserPeerDetailResultPayload struct {
	Found bool           `json:"found"`
	Peer  *BotPeerDetail `json:"peer,omitempty"`
	Error string         `json:"error,omitempty"`
}

type BotPeerDetail struct {
	ID              string   `json:"id"`
	NodeID          string   `json:"node_id"`
	NodeName        string   `json:"node_name"`
	NodeLocation    string   `json:"node_location"`
	NodePublicIP    string   `json:"node_public_ip"`
	NodeOurLLA      string   `json:"node_our_lla"`
	NodeOurWgPubkey string   `json:"node_our_wg_pubkey"`
	RemoteASN       int64    `json:"remote_asn"`
	RemotePubkey    string   `json:"remote_pubkey"`
	RemoteEndpoint  string   `json:"remote_endpoint"`
	RemoteLLA       string   `json:"remote_lla"`
	ContactEmail    string   `json:"contact_email"`
	WgListenPort    int      `json:"wg_listen_port"`
	WgInterfaceName string   `json:"wg_interface_name"`
	Status          string   `json:"status"`
	RejectReason    *string  `json:"reject_reason,omitempty"`
	MTU             *int     `json:"mtu,omitempty"`
	CreatedAt       string   `json:"created_at"`
	LatestRtt       *float64 `json:"latest_rtt_ms,omitempty"`
	LatestBGPState  *string  `json:"latest_bgp_state,omitempty"`
	LatestHandshake *string  `json:"latest_handshake,omitempty"`
	RxBytes24h      int64    `json:"rx_bytes_24h,omitempty"`
	TxBytes24h      int64    `json:"tx_bytes_24h,omitempty"`
}

type BotUserPeerCreatePayload struct {
	TgUserID       int64  `json:"tg_user_id"`
	TgUsername     string `json:"tg_username"`
	NodeID         string `json:"node_id"`
	RemotePubkey   string `json:"remote_pubkey"`
	RemoteEndpoint string `json:"remote_endpoint"`
	RemoteLLA      string `json:"remote_lla"`
	MTU            *int   `json:"mtu,omitempty"`
}

type BotUserPeerCreateResultPayload struct {
	Success bool   `json:"success"`
	PeerID  string `json:"peer_id,omitempty"`
	Error   string `json:"error,omitempty"`
}

type BotUserPeerDeletePayload struct {
	TgUserID int64  `json:"tg_user_id"`
	PeerID   string `json:"peer_id"`
}

type BotUserPeerDeleteResultPayload struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type BotUserPeerMetricsPayload struct {
	TgUserID int64  `json:"tg_user_id"`
	PeerID   string `json:"peer_id"`
}

type BotUserPeerMetricsResultPayload struct {
	Found   bool              `json:"found"`
	Metrics []BotPeerMetricPt `json:"metrics,omitempty"`
	Error   string            `json:"error,omitempty"`
}

type BotPeerMetricPt struct {
	Time     string   `json:"time"`
	RttMs    *float64 `json:"rtt_ms,omitempty"`
	BGPState string   `json:"bgp_state"`
	RxBytes  int64    `json:"rx_bytes"`
	TxBytes  int64    `json:"tx_bytes"`
}

type BotAdminPeersPayload struct {
	Status string `json:"status,omitempty"`
	Page   int    `json:"page,omitempty"`
}

type BotAdminPeersResultPayload struct {
	Peers []BotAdminPeerInfo `json:"peers"`
	Total int                `json:"total"`
	Page  int                `json:"page"`
	Error string             `json:"error,omitempty"`
}

type BotAdminPeerInfo struct {
	ID             string   `json:"id"`
	NodeName       string   `json:"node_name"`
	NodeLocation   string   `json:"node_location"`
	RemoteASN      int64    `json:"remote_asn"`
	Status         string   `json:"status"`
	ContactEmail   string   `json:"contact_email"`
	RejectReason   *string  `json:"reject_reason,omitempty"`
	LatestRtt      *float64 `json:"latest_rtt_ms,omitempty"`
	LatestBGPState *string  `json:"latest_bgp_state,omitempty"`
	CreatedAt      string   `json:"created_at"`
}

type BotAdminPeerActionPayload struct {
	TgUserID int64  `json:"tg_user_id"`
	PeerID   string `json:"peer_id"`
	Action   string `json:"action"`
	Reason   string `json:"reason,omitempty"`
}

type BotAdminPeerActionResultPayload struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

type BotAdminNodesPayload struct{}

type BotAdminNodesResultPayload struct {
	Nodes []BotAdminNodeInfo `json:"nodes"`
	Error string             `json:"error,omitempty"`
}

type BotAdminNodeInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Location     string `json:"location"`
	ASN          int64  `json:"asn"`
	Online       bool   `json:"online"`
	AgentVersion string `json:"agent_version"`
	AgentState   string `json:"agent_state"`
	PeerCount    int    `json:"peer_count"`
}

type BotAdminNodeDetailPayload struct {
	NodeID string `json:"node_id"`
}

type BotAdminNodeDetailResultPayload struct {
	Found bool              `json:"found"`
	Node  *BotAdminNodeInfo `json:"node,omitempty"`
	Error string            `json:"error,omitempty"`
}

type BotAdminNodeUpdatePayload struct {
	TgUserID int64  `json:"tg_user_id"`
	NodeID   string `json:"node_id"`
}

type BotAdminNodeUpdateResultPayload struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

type BotAdminAuditPayload struct {
	Page int `json:"page,omitempty"`
}

type BotAdminAuditResultPayload struct {
	Entries []BotAuditEntry `json:"entries"`
	Total   int             `json:"total"`
	Page    int             `json:"page"`
	Error   string          `json:"error,omitempty"`
}

type BotAuditEntry struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	Operator  string `json:"operator"`
	TargetID  string `json:"target_id,omitempty"`
	CreatedAt string `json:"created_at"`
}
