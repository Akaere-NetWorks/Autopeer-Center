package service

const NotificationCatalogVersion = 1

type NotificationKind string

const (
	NotificationKindNormal   NotificationKind = "normal"
	NotificationKindCritical NotificationKind = "critical"
	NotificationKindRequired NotificationKind = "required"
)

type NotificationOption struct {
	Key                         string           `json:"key"`
	Channel                     string           `json:"channel"`
	Group                       string           `json:"group"`
	Name                        string           `json:"name"`
	Description                 string           `json:"description"`
	LegacyLevel                 int              `json:"legacy_level"`
	IntroducedVersion           int              `json:"introduced_version"`
	Kind                        NotificationKind `json:"kind"`
	RequiresDisableConfirmation bool             `json:"requires_disable_confirmation"`
}

type NotificationPreset struct {
	Level       int      `json:"level"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	EnabledKeys []string `json:"enabled_keys"`
}

const (
	NotificationAuthLoginCode        = "auth_login_code"
	NotificationPeerSubmitted        = "peer_submitted"
	NotificationPeerApproved         = "peer_approved"
	NotificationPeerRejected         = "peer_rejected"
	NotificationPeerSuspended        = "peer_suspended"
	NotificationPeerUnsuspended      = "peer_unsuspended"
	NotificationPeerDeleted          = "peer_deleted"
	NotificationPeerBGPDown          = "peer_bgp_down"
	NotificationPeerBGPRecovered     = "peer_bgp_recovered"
	NotificationPeerHandshakeStale   = "peer_handshake_stale"
	NotificationPeerLatencyHigh      = "peer_latency_high"
	NotificationPeerLatencyRecovered = "peer_latency_recovered"
	NotificationPeerMTUUpdated       = "peer_mtu_updated"
	NotificationPeerEndpointMismatch  = "peer_endpoint_mismatch"
	NotificationPeerEndpointRecovered = "peer_endpoint_recovered"
)

var notificationOptions = []NotificationOption{
	{Key: NotificationAuthLoginCode, Channel: "email", Group: "account", Name: "Login verification code", Description: "Email code used to sign in.", LegacyLevel: EmailLevelNone, IntroducedVersion: 1, Kind: NotificationKindRequired, RequiresDisableConfirmation: true},
	{Key: NotificationPeerSubmitted, Channel: "email", Group: "peer_lifecycle", Name: "Peer submitted", Description: "A peering request was submitted.", LegacyLevel: EmailLevelGeneral, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerApproved, Channel: "email", Group: "peer_lifecycle", Name: "Peer approved", Description: "A peering request was approved.", LegacyLevel: EmailLevelUrgent, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerRejected, Channel: "email", Group: "peer_lifecycle", Name: "Peer rejected", Description: "A peering request was rejected.", LegacyLevel: EmailLevelUrgent, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerSuspended, Channel: "email", Group: "peer_lifecycle", Name: "Peer suspended", Description: "An active peering session was suspended.", LegacyLevel: EmailLevelUrgent, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerUnsuspended, Channel: "email", Group: "peer_lifecycle", Name: "Peer unsuspended", Description: "A suspended peering session was restored.", LegacyLevel: EmailLevelUrgent, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerDeleted, Channel: "email", Group: "peer_lifecycle", Name: "Peer deleted", Description: "A peering session was deleted.", LegacyLevel: EmailLevelUrgent, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerBGPDown, Channel: "email", Group: "connectivity", Name: "BGP session down", Description: "A BGP session is down.", LegacyLevel: EmailLevelGeneral, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerBGPRecovered, Channel: "email", Group: "connectivity", Name: "BGP session recovered", Description: "A BGP session recovered.", LegacyLevel: EmailLevelGeneral, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerHandshakeStale, Channel: "email", Group: "connectivity", Name: "WireGuard handshake stale", Description: "WireGuard handshake has not updated for too long.", LegacyLevel: EmailLevelGeneral, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerLatencyHigh, Channel: "email", Group: "monitoring", Name: "High latency", Description: "Peer latency is abnormally high.", LegacyLevel: EmailLevelAll, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerLatencyRecovered, Channel: "email", Group: "monitoring", Name: "Latency recovered", Description: "Peer latency returned to normal.", LegacyLevel: EmailLevelAll, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerMTUUpdated, Channel: "email", Group: "peer_lifecycle", Name: "Peer MTU updated", Description: "Peer MTU was changed by an operator.", LegacyLevel: EmailLevelUrgent, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerSubmitted, Channel: "telegram", Group: "peer_lifecycle", Name: "Peer submitted", Description: "A peering request was submitted.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerApproved, Channel: "telegram", Group: "peer_lifecycle", Name: "Peer approved", Description: "A peering request was approved.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerRejected, Channel: "telegram", Group: "peer_lifecycle", Name: "Peer rejected", Description: "A peering request was rejected.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerSuspended, Channel: "telegram", Group: "peer_lifecycle", Name: "Peer suspended", Description: "An active peering session was suspended.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerUnsuspended, Channel: "telegram", Group: "peer_lifecycle", Name: "Peer unsuspended", Description: "A suspended peering session was restored.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerDeleted, Channel: "telegram", Group: "peer_lifecycle", Name: "Peer deleted", Description: "A peering session was deleted.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerBGPDown, Channel: "telegram", Group: "connectivity", Name: "BGP session down", Description: "A BGP session is down.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerBGPRecovered, Channel: "telegram", Group: "connectivity", Name: "BGP session recovered", Description: "A BGP session recovered.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerHandshakeStale, Channel: "telegram", Group: "connectivity", Name: "WireGuard handshake stale", Description: "WireGuard handshake has not updated for too long.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerLatencyHigh, Channel: "telegram", Group: "monitoring", Name: "High latency", Description: "Peer latency is abnormally high.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerLatencyRecovered, Channel: "telegram", Group: "monitoring", Name: "Latency recovered", Description: "Peer latency returned to normal.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerEndpointMismatch, Channel: "email", Group: "connectivity", Name: "Endpoint mismatch", Description: "BGP suspended due to endpoint mismatch.", LegacyLevel: EmailLevelGeneral, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerEndpointRecovered, Channel: "email", Group: "connectivity", Name: "Endpoint recovered", Description: "BGP restored after endpoint recovery.", LegacyLevel: EmailLevelGeneral, IntroducedVersion: 1, Kind: NotificationKindNormal},
	{Key: NotificationPeerEndpointMismatch, Channel: "telegram", Group: "connectivity", Name: "Endpoint mismatch", Description: "BGP suspended due to endpoint mismatch.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
	{Key: NotificationPeerEndpointRecovered, Channel: "telegram", Group: "connectivity", Name: "Endpoint recovered", Description: "BGP restored after endpoint recovery.", LegacyLevel: 0, IntroducedVersion: 2, Kind: NotificationKindNormal},
}

func NotificationOptions() []NotificationOption {
	options := make([]NotificationOption, len(notificationOptions))
	copy(options, notificationOptions)
	return options
}

func NotificationOptionsForChannel(channel string) []NotificationOption {
	options := make([]NotificationOption, 0)
	for _, option := range notificationOptions {
		if option.Channel == channel {
			options = append(options, option)
		}
	}
	return options
}

func NotificationOptionByKey(key string) (NotificationOption, bool) {
	for _, option := range notificationOptions {
		if option.Key == key {
			return option, true
		}
	}
	return NotificationOption{}, false
}

func LegacyEnabledNotificationKeys(level int) []string {
	keys := make([]string, 0)
	for _, option := range notificationOptions {
		if option.Channel != "email" {
			continue
		}
		if option.Key == NotificationAuthLoginCode || (option.LegacyLevel > 0 && level >= option.LegacyLevel) {
			keys = append(keys, option.Key)
		}
	}
	return keys
}

func telegramDefaultEnabledKeys() []string {
	keys := make([]string, 0)
	for _, option := range notificationOptions {
		if option.Channel == "telegram" {
			keys = append(keys, option.Key)
		}
	}
	return keys
}

func NotificationPresets() []NotificationPreset {
	return []NotificationPreset{
		{Level: EmailLevelNone, Name: "none", Description: "Disable optional business notifications.", EnabledKeys: LegacyEnabledNotificationKeys(EmailLevelNone)},
		{Level: EmailLevelUrgent, Name: "urgent", Description: "Important peer lifecycle changes.", EnabledKeys: LegacyEnabledNotificationKeys(EmailLevelUrgent)},
		{Level: EmailLevelGeneral, Name: "general", Description: "Peer lifecycle and connectivity issues.", EnabledKeys: LegacyEnabledNotificationKeys(EmailLevelGeneral)},
		{Level: EmailLevelAll, Name: "all", Description: "All notification types that exist today.", EnabledKeys: LegacyEnabledNotificationKeys(EmailLevelAll)},
	}
}

func NotificationPresetsForChannel(channel string) []NotificationPreset {
	if channel == "telegram" {
		allKeys := telegramDefaultEnabledKeys()
		return []NotificationPreset{
			{Level: 0, Name: "none", Description: "Disable all Telegram notifications.", EnabledKeys: []string{}},
			{Level: 1, Name: "important", Description: "Peer lifecycle changes.", EnabledKeys: filterKeysByGroup(allKeys, "peer_lifecycle")},
			{Level: 2, Name: "all", Description: "All Telegram notifications.", EnabledKeys: allKeys},
		}
	}
	return NotificationPresets()
}

func filterKeysByGroup(keys []string, group string) []string {
	result := make([]string, 0)
	for _, option := range notificationOptions {
		if option.Group == group && option.Channel == "telegram" {
			for _, k := range keys {
				if k == option.Key {
					result = append(result, k)
					break
				}
			}
		}
	}
	return result
}
