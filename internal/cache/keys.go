package cache

import "fmt"

const (
	AlertBGPDown    = "a:bgp_d:"
	AlertBGPFail    = "a:bgp_f:"
	AlertHandshake  = "a:hs:"
	AlertNodeOff    = "a:noff:"
	AlertLatency    = "a:lat:"
	AlertEndpoint   = "a:ep:"
	LatencyActive   = "lat:act:"

	RegistryASN   = "r:asn:"
	RegistryGPG   = "r:gpg:"

	RateLimitASN = "rl:a:"
	RateLimitIP  = "rl:ip:"

	SettingVal = "s:v:"
	SettingThr = "s:t:"

	SiteSettingVal = "ss:v:"
)

func ASNKey(prefix string, asn int64) string {
	return fmt.Sprintf("%s%d", prefix, asn)
}

func PeerKey(prefix, peerID string) string {
	return prefix + peerID
}

func NodeKey(prefix, nodeID string) string {
	return prefix + nodeID
}

func IPKey(prefix, ip string) string {
	return prefix + ip
}

func SettingKey(prefix, key string) string {
	return prefix + key
}
