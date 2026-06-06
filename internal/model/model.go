package model

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

type Admin struct {
	bun.BaseModel `bun:"table:admins,alias:a"`

	ID           string    `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	Email        string    `json:"email" bun:"email,unique"`
	PasswordHash string    `json:"-" bun:"password_hash"`
	CreatedAt    time.Time `json:"created_at" bun:"created_at"`
}

type Node struct {
	bun.BaseModel `bun:"table:nodes,alias:n"`

	ID                  string    `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	Name                string    `json:"name" bun:"name"`
	Location            string    `json:"location" bun:"location"`
	AgentToken          string    `json:"-" bun:"agent_token"`
	AgentPubkey         string    `json:"-" bun:"agent_pubkey"`
	PublicIP            string    `json:"public_ip" bun:"public_ip"`
	OurASN              int64     `json:"our_asn" bun:"our_asn"`
	OurLLA              string    `json:"our_lla" bun:"our_lla"`
	OurWgPubkey         string    `json:"our_wg_pubkey" bun:"our_wg_pubkey"`
	BirdPeerDir         string    `json:"bird_peer_dir" bun:"bird_peer_dir"`
	WgDir               string    `json:"wg_dir" bun:"wg_dir"`
	WgPortPrefix        int       `json:"wg_port_prefix" bun:"wg_port_prefix"`
	Online              bool      `json:"online" bun:"online"`
	Enabled             bool      `json:"enabled" bun:"enabled"`
	AgentVersion        string    `json:"agent_version" bun:"agent_version"`
	AgentState          string    `json:"agent_state" bun:"agent_state"`
	AgentStateChangedAt time.Time `json:"agent_state_changed_at" bun:"agent_state_changed_at"`
	CreatedAt           time.Time `json:"created_at" bun:"created_at"`
}

type NodePublic struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Location    string `json:"location"`
	PublicIP    string `json:"public_ip"`
	OurASN      int64  `json:"our_asn"`
	OurLLA      string `json:"our_lla"`
	OurWgPubkey string `json:"our_wg_pubkey"`
	Online      bool   `json:"online"`
	Enabled     bool   `json:"enabled"`
}

type Peer struct {
	bun.BaseModel `bun:"table:peers,alias:p"`

	ID                 string    `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	NodeID             string    `json:"node_id" bun:"node_id"`
	RemoteASN          int64     `json:"remote_asn" bun:"remote_asn"`
	RemotePubkey       string    `json:"remote_pubkey" bun:"remote_pubkey"`
	RemoteEndpoint     string    `json:"remote_endpoint" bun:"remote_endpoint"`
	RemoteLLA          string    `json:"remote_lla" bun:"remote_lla"`
	ContactEmail       string    `json:"contact_email" bun:"contact_email"`
	WgListenPort       int       `json:"wg_listen_port" bun:"wg_listen_port"`
	WgInterfaceName    string    `json:"wg_interface_name" bun:"wg_interface_name"`
	BgpProtoName       string    `json:"bgp_proto_name" bun:"bgp_proto_name"`
	BirdConfigFilename string    `json:"bird_config_filename" bun:"bird_config_filename"`
	WgManaged          bool      `json:"wg_managed" bun:"wg_managed,default:true"`
	MTU                *int      `json:"mtu,omitempty" bun:"mtu"`
	WgPreSharedKey     *string   `json:"wg_preshared_key,omitempty" bun:"wg_preshared_key"`
	Status             string    `json:"status" bun:"status"`
	RejectReason            *string   `json:"reject_reason,omitempty" bun:"reject_reason"`
	EndpointMismatchSince   *time.Time `json:"endpoint_mismatch_since,omitempty" bun:"endpoint_mismatch_since"`
	BGPSuspendedByEndpoint  bool      `json:"bgp_suspended_by_endpoint" bun:"bgp_suspended_by_endpoint,default:false"`
	CreatedAt               time.Time `json:"created_at" bun:"created_at,nullzero,default:now()"`
	UpdatedAt          time.Time `json:"updated_at" bun:"updated_at,nullzero,default:now()"`
}

type PeerWithNode struct {
	bun.BaseModel `bun:"table:peers,alias:p"`

	Peer
	NodeName     string `json:"node_name" bun:"node_name"`
	NodeLocation string `json:"node_location" bun:"node_location"`
}

type AuditLog struct {
	bun.BaseModel `bun:"table:audit_logs,alias:al"`

	ID        string                 `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	Action    string                 `json:"action" bun:"action"`
	Operator  string                 `json:"operator" bun:"operator"`
	TargetID  *string                `json:"target_id,omitempty" bun:"target_id"`
	Detail    map[string]interface{} `json:"detail,omitempty" bun:"detail,type:jsonb"`
	CreatedAt time.Time              `json:"created_at" bun:"created_at,nullzero,default:now()"`
}

type PeerMetric struct {
	bun.BaseModel `bun:"table:peer_metrics,alias:pm"`

	Time            time.Time  `json:"time" bun:"time,pk"`
	PeerID          string     `json:"peer_id" bun:"peer_id"`
	RxBytes         int64      `json:"rx_bytes" bun:"rx_bytes"`
	TxBytes         int64      `json:"tx_bytes" bun:"tx_bytes"`
	UptimeSeconds   int        `json:"uptime_seconds" bun:"uptime_seconds"`
	BGPState        string     `json:"bgp_state" bun:"bgp_state"`
	RttMs           *float64   `json:"rtt_ms,omitempty" bun:"rtt_ms"`
	LastHandshake   *time.Time `json:"last_handshake,omitempty" bun:"last_handshake"`
	RoutesImported  *int       `json:"routes_imported,omitempty" bun:"routes_imported"`
	RoutesExported  *int       `json:"routes_exported,omitempty" bun:"routes_exported"`
	BGPUptimeSecs   *int       `json:"bgp_uptime_secs,omitempty" bun:"bgp_uptime_secs"`
	RoutesPreferred  *int       `json:"routes_preferred,omitempty" bun:"routes_preferred"`
	WgActualEndpoint string     `json:"wg_actual_endpoint,omitempty" bun:"wg_actual_endpoint"`
}

type NodeMetric struct {
	bun.BaseModel `bun:"table:node_metrics,alias:nm"`

	Time         time.Time `json:"time" bun:"time,pk"`
	NodeID       string    `json:"node_id" bun:"node_id"`
	MemAllocMB   *int64    `json:"mem_alloc_mb,omitempty" bun:"mem_alloc_mb"`
	MemSysMB     *int64    `json:"mem_sys_mb,omitempty" bun:"mem_sys_mb"`
	NumGoroutine *int      `json:"num_goroutine,omitempty" bun:"num_goroutine"`
	UptimeSecs   *int64    `json:"uptime_secs,omitempty" bun:"uptime_secs"`
}

type UserLoginCode struct {
	bun.BaseModel `bun:"table:user_login_codes,alias:ulc"`

	ID             string    `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	ASN            int64     `json:"asn" bun:"asn"`
	Email          string    `json:"email" bun:"email"`
	CodeHash       string    `json:"-" bun:"code"`
	ExpiresAt      time.Time `json:"expires_at" bun:"expires_at"`
	Used           bool      `json:"used" bun:"used"`
	FailedAttempts int       `json:"failed_attempts" bun:"failed_attempts"`
	CreatedAt      time.Time `json:"created_at" bun:"created_at"`
}

type GPGChallenge struct {
	bun.BaseModel `bun:"table:gpg_challenges,alias:gc"`

	ID           string    `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	ASN          int64     `json:"asn" bun:"asn"`
	Fingerprints []string  `json:"fingerprints" bun:"fingerprints,array"`
	Challenge    string    `json:"-" bun:"challenge"`
	ExpiresAt    time.Time `json:"expires_at" bun:"expires_at"`
	Used         bool      `json:"used" bun:"used"`
	CreatedAt    time.Time `json:"created_at" bun:"created_at"`
}

type AuthSession struct {
	bun.BaseModel `bun:"table:auth_sessions,alias:as"`

	ID                             string     `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	SubjectType                    string     `json:"subject_type" bun:"subject_type"`
	ASN                            *int64     `json:"asn,omitempty" bun:"asn"`
	AdminID                        *string    `json:"admin_id,omitempty" bun:"admin_id"`
	Email                          string     `json:"email,omitempty" bun:"email"`
	ParentSessionID                *string    `json:"parent_session_id,omitempty" bun:"parent_session_id"`
	RefreshTokenHash               string     `json:"-" bun:"refresh_token_hash,nullzero"`
	PreviousRefreshTokenHash       string     `json:"-" bun:"previous_refresh_token_hash,nullzero"`
	PreviousRefreshTokenValidUntil *time.Time `json:"-" bun:"previous_refresh_token_valid_until"`
	UserAgent                      string     `json:"user_agent,omitempty" bun:"user_agent"`
	IPAddress                      string     `json:"ip_address,omitempty" bun:"ip"`
	DeviceName                     string     `json:"device_name,omitempty" bun:"device_name"`
	LoginMethod                    string     `json:"login_method,omitempty" bun:"login_method"`
	CreatedAt                      time.Time  `json:"created_at" bun:"created_at,nullzero,default:now()"`
	LastUsedAt                     *time.Time `json:"last_used_at,omitempty" bun:"last_used_at"`
	ExpiresAt                      time.Time  `json:"expires_at" bun:"expires_at"`
	RevokedAt                      *time.Time `json:"revoked_at,omitempty" bun:"revoked_at"`
	RevokedReason                  string     `json:"revoked_reason,omitempty" bun:"revoked_reason"`
}

type AuthDeviceGrant struct {
	bun.BaseModel `bun:"table:auth_device_grants,alias:adg"`

	ID                  string     `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	DeviceCodeHash      string     `json:"-" bun:"device_code_hash"`
	UserCodeHash        string     `json:"-" bun:"user_code_hash"`
	ClientName          string     `json:"client_name" bun:"client_name"`
	DeviceName          string     `json:"device_name,omitempty" bun:"device_name"`
	Version             string     `json:"version,omitempty" bun:"version"`
	Scopes              []string   `json:"scopes" bun:"scopes,array"`
	Status              string     `json:"status" bun:"status"`
	ApprovedASN         *int64     `json:"approved_asn,omitempty" bun:"approved_asn"`
	ApprovedEmail       string     `json:"approved_email,omitempty" bun:"approved_email"`
	ApprovedBySessionID *string    `json:"approved_by_session_id,omitempty" bun:"approved_by_session_id"`
	ExchangedSessionID  *string    `json:"exchanged_session_id,omitempty" bun:"exchanged_session_id"`
	RequestIP           string     `json:"request_ip,omitempty" bun:"request_ip"`
	RequestUserAgent    string     `json:"request_user_agent,omitempty" bun:"request_user_agent"`
	ApproveIP           *string    `json:"approve_ip,omitempty" bun:"approve_ip"`
	ApproveUserAgent    string     `json:"approve_user_agent,omitempty" bun:"approve_user_agent"`
	PollCount           int        `json:"poll_count" bun:"poll_count"`
	LastPolledAt        *time.Time `json:"last_polled_at,omitempty" bun:"last_polled_at"`
	NextPollAfter       *time.Time `json:"next_poll_after,omitempty" bun:"next_poll_after"`
	CreatedAt           time.Time  `json:"created_at" bun:"created_at,nullzero,default:now()"`
	ExpiresAt           time.Time  `json:"expires_at" bun:"expires_at"`
	ApprovedAt          *time.Time `json:"approved_at,omitempty" bun:"approved_at"`
	DeniedAt            *time.Time `json:"denied_at,omitempty" bun:"denied_at"`
	ExchangedAt         *time.Time `json:"exchanged_at,omitempty" bun:"exchanged_at"`
}

type MCPKey struct {
	bun.BaseModel `bun:"table:user_mcp_keys,alias:umk"`

	ID           string     `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	ASN          int64      `json:"asn" bun:"asn"`
	Name         string     `json:"name" bun:"name"`
	KeyHash      string     `json:"-" bun:"key_hash,unique"`
	KeyPrefix    string     `json:"key_prefix" bun:"key_prefix"`
	Capabilities []string   `json:"capabilities" bun:"capabilities,array"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty" bun:"expires_at"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty" bun:"last_used_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty" bun:"revoked_at"`
	ScopeVersion int        `json:"scope_version" bun:"scope_version"`
	CreatedAt    time.Time  `json:"created_at" bun:"created_at"`
}

type AdminMCPKey struct {
	bun.BaseModel `bun:"table:admin_mcp_keys,alias:amk"`

	ID           string     `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	AdminID      string     `json:"admin_id" bun:"admin_id"`
	Name         string     `json:"name" bun:"name"`
	KeyHash      string     `json:"-" bun:"key_hash,unique"`
	KeyPrefix    string     `json:"key_prefix" bun:"key_prefix"`
	Capabilities []string   `json:"capabilities" bun:"capabilities,array"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty" bun:"expires_at"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty" bun:"last_used_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty" bun:"revoked_at"`
	ScopeVersion int        `json:"scope_version" bun:"scope_version"`
	CreatedAt    time.Time  `json:"created_at" bun:"created_at"`
}

type MCPSession struct {
	bun.BaseModel `bun:"table:mcp_sessions,alias:ms"`

	ID             string     `json:"id" bun:"id,pk"`
	ASN            *int64     `json:"asn,omitempty" bun:"asn"`
	KeyID          *string    `json:"key_id,omitempty" bun:"key_id"`
	IsAdmin        bool       `json:"is_admin" bun:"is_admin"`
	AdminKeyID     *string    `json:"admin_key_id,omitempty" bun:"admin_key_id"`
	ConnectedAt    time.Time  `json:"connected_at" bun:"connected_at"`
	DisconnectedAt *time.Time `json:"disconnected_at,omitempty" bun:"disconnected_at"`
	LastPingAt     *time.Time `json:"last_ping_at,omitempty" bun:"last_ping_at"`
}

type MCPAuditLog struct {
	bun.BaseModel `bun:"table:mcp_audit_logs,alias:mal"`

	ID             string    `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	SessionID      *string   `json:"session_id,omitempty" bun:"session_id"`
	ASN            *int64    `json:"asn,omitempty" bun:"asn"`
	AdminID        *string   `json:"admin_id,omitempty" bun:"admin_id"`
	KeyID          *string   `json:"key_id,omitempty" bun:"key_id"`
	AdminKeyID     *string   `json:"admin_key_id,omitempty" bun:"admin_key_id"`
	ToolName       string    `json:"tool_name" bun:"tool_name"`
	Args           json.RawMessage `json:"args,omitempty" bun:"args,type:jsonb"`
	Capability     *string   `json:"capability,omitempty" bun:"capability"`
	Mode           string    `json:"mode" bun:"mode"`
	ResourceType   *string   `json:"resource_type,omitempty" bun:"resource_type"`
	ResourceID     *string   `json:"resource_id,omitempty" bun:"resource_id"`
	IdempotencyKey *string   `json:"idempotency_key,omitempty" bun:"idempotency_key"`
	RequestHash    *string   `json:"request_hash,omitempty" bun:"request_hash"`
	DurationMs     *int64    `json:"duration_ms,omitempty" bun:"duration_ms"`
	ClientIP       string    `json:"client_ip,omitempty" bun:"client_ip"`
	UserAgent      string    `json:"user_agent,omitempty" bun:"user_agent"`
	ResultOK       bool      `json:"result_ok" bun:"result_ok"`
	ErrorMsg       *string   `json:"error_msg,omitempty" bun:"error_msg"`
	CalledAt       time.Time `json:"called_at" bun:"called_at"`
}

type MCPIdempotencyKey struct {
	bun.BaseModel `bun:"table:mcp_idempotency_keys,alias:mik"`

	ID             string    `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	ActorType      string    `json:"actor_type" bun:"actor_type"`
	KeyID          *string   `json:"key_id,omitempty" bun:"key_id"`
	AdminKeyID     *string   `json:"admin_key_id,omitempty" bun:"admin_key_id"`
	ASN            *int64    `json:"asn,omitempty" bun:"asn"`
	AdminID        *string   `json:"admin_id,omitempty" bun:"admin_id"`
	ToolName       string    `json:"tool_name" bun:"tool_name"`
	IdempotencyKey string    `json:"idempotency_key" bun:"idempotency_key"`
	RequestHash    string    `json:"request_hash" bun:"request_hash"`
	Status         string    `json:"status" bun:"status"`
	ResourceType   *string   `json:"resource_type,omitempty" bun:"resource_type"`
	ResourceID     *string   `json:"resource_id,omitempty" bun:"resource_id"`
	ResponseJSON   any       `json:"response_json,omitempty" bun:"response_json,type:jsonb"`
	ErrorCode      string    `json:"error_code,omitempty" bun:"error_code"`
	ErrorMessage   string    `json:"error_message,omitempty" bun:"error_message"`
	CreatedAt      time.Time `json:"created_at" bun:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" bun:"updated_at"`
}

type MCPOperation struct {
	bun.BaseModel `bun:"table:mcp_operations,alias:mo"`

	ID             string    `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	ActorType      string    `json:"actor_type" bun:"actor_type"`
	KeyID          *string   `json:"key_id,omitempty" bun:"key_id"`
	AdminKeyID     *string   `json:"admin_key_id,omitempty" bun:"admin_key_id"`
	ASN            *int64    `json:"asn,omitempty" bun:"asn"`
	AdminID        *string   `json:"admin_id,omitempty" bun:"admin_id"`
	OperationType  string    `json:"operation_type" bun:"operation_type"`
	ResourceType   string    `json:"resource_type" bun:"resource_type"`
	ResourceID     string    `json:"resource_id" bun:"resource_id"`
	RequestedArgs  any       `json:"requested_args" bun:"requested_args,type:jsonb"`
	Status         string    `json:"status" bun:"status"`
	IdempotencyKey string    `json:"idempotency_key" bun:"idempotency_key"`
	RequestHash    string    `json:"request_hash" bun:"request_hash"`
	CreatedAt      time.Time `json:"created_at" bun:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" bun:"updated_at"`
}

type RequestLog struct {
	bun.BaseModel `bun:"table:request_logs,alias:rl"`

	ID         string    `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	Method     string    `json:"method" bun:"method"`
	Path       string    `json:"path" bun:"path"`
	StatusCode int       `json:"status_code" bun:"status_code"`
	IP         string    `json:"ip" bun:"ip"`
	DurationMs int64     `json:"duration_ms" bun:"duration_ms"`
	CreatedAt  time.Time `json:"created_at" bun:"created_at,nullzero"`
}

type PeerEmailPreference struct {
	bun.BaseModel `bun:"table:peer_email_preferences,alias:pep"`

	ASN        int64     `json:"asn" bun:"asn,pk"`
	EmailLevel int       `json:"email_level" bun:"email_level"`
	UpdatedAt  time.Time `json:"updated_at" bun:"updated_at"`
}

type PeerNotificationPreference struct {
	bun.BaseModel `bun:"table:peer_notification_preferences,alias:pnp"`

	ASN             int64     `json:"asn" bun:"asn,pk"`
	Channel         string    `json:"channel" bun:"channel,pk"`
	NotificationKey string    `json:"notification_key" bun:"notification_key,pk"`
	Enabled         bool      `json:"enabled" bun:"enabled"`
	UpdatedAt       time.Time `json:"updated_at" bun:"updated_at"`
}

type PeerNotificationPreferenceState struct {
	bun.BaseModel `bun:"table:peer_notification_preference_states,alias:pnps"`

	ASN                int64     `json:"asn" bun:"asn,pk"`
	SeenCatalogVersion int       `json:"seen_catalog_version" bun:"seen_catalog_version"`
	WizardCompletedAt  time.Time `json:"wizard_completed_at,omitempty" bun:"wizard_completed_at,nullzero"`
	UpdatedAt          time.Time `json:"updated_at" bun:"updated_at"`
}

type SiteSetting struct {
	bun.BaseModel `bun:"table:site_settings,alias:ss"`

	ID           int       `json:"id" bun:"id,pk,autoincrement"`
	SettingKey   string    `json:"setting_key" bun:"setting_key,unique"`
	SettingValue string    `json:"setting_value" bun:"setting_value"`
	Description  string    `json:"description" bun:"description"`
	UpdatedAt    time.Time `json:"updated_at" bun:"updated_at"`
}

type NotificationSetting struct {
	bun.BaseModel `bun:"table:notification_settings,alias:ns"`

	ID             int       `json:"id" bun:"id,pk,autoincrement"`
	SettingKey     string    `json:"setting_key" bun:"setting_key,unique"`
	SettingValue   string    `json:"setting_value" bun:"setting_value"`
	ThresholdValue string    `json:"threshold_value" bun:"threshold_value"`
	Description    string    `json:"description" bun:"description"`
	UpdatedAt      time.Time `json:"updated_at" bun:"updated_at"`
}

type BotSetting struct {
	bun.BaseModel `bun:"table:bot_settings,alias:bs"`

	ID               int       `json:"id" bun:"id,pk,autoincrement"`
	SettingKey       string    `json:"setting_key" bun:"setting_key,unique"`
	SettingValue     string    `json:"setting_value" bun:"setting_value"`
	Description      string    `json:"description" bun:"description"`
	UpdatedAt        time.Time `json:"updated_at" bun:"updated_at"`
	SettingValueHash *string   `json:"-" bun:"setting_value_hash"`
}

type BotCommandStat struct {
	bun.BaseModel `bun:"table:bot_command_stats,alias:bcs"`

	ID        int       `json:"id" bun:"id,pk,autoincrement"`
	Command   string    `json:"command" bun:"command"`
	TgUserID  int64     `json:"tg_user_id" bun:"tg_user_id"`
	Username  string    `json:"username" bun:"username"`
	ChatID    int64     `json:"chat_id" bun:"chat_id"`
	CreatedAt time.Time `json:"created_at" bun:"created_at"`
}

type BotBlockedUser struct {
	bun.BaseModel `bun:"table:bot_blocked_users,alias:bbu"`

	ID        int       `json:"id" bun:"id,pk,autoincrement"`
	TgUserID  int64     `json:"tg_user_id" bun:"tg_user_id,unique"`
	Username  string    `json:"username" bun:"username"`
	Reason    string    `json:"reason" bun:"reason"`
	BlockedBy string    `json:"blocked_by" bun:"blocked_by"`
	BlockedAt time.Time `json:"blocked_at" bun:"blocked_at"`
}

type AgentRelease struct {
	bun.BaseModel `bun:"table:agent_releases,alias:ar"`

	Version    string    `json:"version" bun:"version,pk"`
	OS         string    `json:"os" bun:"os,pk"`
	Arch       string    `json:"arch" bun:"arch,pk"`
	SHA256     string    `json:"sha256" bun:"sha256"`
	Size       int64     `json:"size" bun:"size"`
	Path       string    `json:"path" bun:"path"`
	UploadedBy *string   `json:"uploaded_by,omitempty" bun:"uploaded_by"`
	UploadedAt time.Time `json:"uploaded_at" bun:"uploaded_at"`
}

type BotUserBinding struct {
	bun.BaseModel `bun:"table:bot_user_bindings,alias:bub"`

	ID         int       `json:"id" bun:"id,pk,autoincrement"`
	TgUserID   int64     `json:"tg_user_id" bun:"tg_user_id,unique"`
	TgUsername string    `json:"tg_username" bun:"tg_username"`
	ASN        int64     `json:"asn" bun:"asn"`
	IsAdmin    bool      `json:"is_admin" bun:"is_admin"`
	BoundAt    time.Time `json:"bound_at" bun:"bound_at"`
	BoundVia   string    `json:"bound_via" bun:"bound_via"`
}

type BotWebBindToken struct {
	bun.BaseModel `bun:"table:bot_web_bind_tokens,alias:bwbt"`

	ID        int       `json:"id" bun:"id,pk,autoincrement"`
	Token     string    `json:"token" bun:"token,unique"`
	ASN       int64     `json:"asn" bun:"asn"`
	CreatedAt time.Time `json:"created_at" bun:"created_at"`
	ExpiresAt time.Time `json:"expires_at" bun:"expires_at"`
	Used      bool      `json:"used" bun:"used"`
}

type PasskeyCredential struct {
	bun.BaseModel `bun:"table:passkey_credentials,alias:pc"`

	ID           string     `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	ASN          int64      `json:"asn" bun:"asn"`
	CredentialID string     `json:"credential_id" bun:"credential_id"`
	PublicKey    []byte     `json:"-" bun:"public_key"`
	AAGUID       string     `json:"aaguid" bun:"aaguid"`
	SignCount    int64      `json:"sign_count" bun:"sign_count"`
	Name         string     `json:"name" bun:"name"`
	CreatedAt    time.Time  `json:"created_at" bun:"created_at,nullzero,default:now()"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty" bun:"last_used_at"`
}

type WebAuthnSession struct {
	bun.BaseModel `bun:"table:webauthn_sessions,alias:ws"`

	ID          string    `json:"id" bun:"id,pk,nullzero,default:gen_random_uuid()"`
	ASN         int64     `json:"asn" bun:"asn"`
	Type        string    `json:"type" bun:"type"`
	SessionData string    `json:"session_data" bun:"session_data"`
	CreatedAt   time.Time `json:"created_at" bun:"created_at,nullzero,default:now()"`
	ExpiresAt   time.Time `json:"expires_at" bun:"expires_at"`
}
