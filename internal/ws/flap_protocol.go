package ws

// Flap protocol message types between center and a flapalerted-agent. Center
// pulls data on demand via the *.query messages; the agent replies with the
// matching reply type echoing the request ID for correlation. The wire shapes
// mirror github.com/akaere/flapalerted-agent (snake_case JSON).
const (
	TypeFlapRegister     = "flap.register"
	TypeFlapQuery        = "flap.query"
	TypeFlapSnapshot     = "flap.snapshot"
	TypeFlapPrefixQuery  = "flap.prefix.query"
	TypeFlapPrefix       = "flap.prefix"
	TypeFlapMetricsQuery = "flap.metrics.query"
	TypeFlapMetrics      = "flap.metrics"
)

// FlapCapabilities advertises a flap agent's static detector configuration.
type FlapCapabilities struct {
	Version              string `json:"version"`
	RouteChangeCounter   int    `json:"route_change_counter"`
	OverThresholdTarget  int    `json:"over_threshold_target"`
	UnderThresholdTarget int    `json:"under_threshold_target"`
	AddPath              bool   `json:"add_path"`
}

// FlapRegisterPayload is sent by the agent on connect.
type FlapRegisterPayload struct {
	AgentID      string           `json:"agent_id"`
	Capabilities FlapCapabilities `json:"capabilities"`
}

// FlapPrefixQueryPayload is the body of a flap.prefix.query.
type FlapPrefixQueryPayload struct {
	Prefix string `json:"prefix"`
}

// FlapSummary is one actively flapping prefix.
type FlapSummary struct {
	Prefix     string `json:"prefix"`
	FirstSeen  int64  `json:"first_seen"`
	RateSec    int    `json:"rate_sec"`
	TotalCount uint64 `json:"total_count"`
}

// FlapPeerSummary is one peer's update rate.
type FlapPeerSummary struct {
	ASN        uint32  `json:"asn"`
	RateSec    int     `json:"rate_sec"`
	RateSecAvg float64 `json:"rate_sec_avg"`
}

// FlapStatistic is one point in the time series.
type FlapStatistic struct {
	Time          int64  `json:"time"`
	Changes       uint64 `json:"changes"`
	ListedChanges uint64 `json:"listed_changes"`
	Active        int    `json:"active"`
	RouteCount    uint32 `json:"route_count"`
}

// FlapSessionInfo describes one established BGP session on the agent.
type FlapSessionInfo struct {
	Remote        string `json:"remote"`
	RouterID      string `json:"router_id"`
	Hostname      string `json:"hostname"`
	EstablishTime int64  `json:"establish_time"`
	ImportCount   uint32 `json:"import_count"`
}

// FlapMetric is the headline gauge data.
type FlapMetric struct {
	ActiveFlapCount                int     `json:"active_flap_count"`
	ActiveFlapTotalPathChangeCount uint64  `json:"active_flap_total_path_change_count"`
	TotalPathChangeCount           uint64  `json:"total_path_change_count"`
	AverageRouteChanges90          float64 `json:"average_route_changes_90"`
	Sessions                       int     `json:"sessions"`
}

// FlapSnapshot is the full flap.snapshot payload returned by an agent.
type FlapSnapshot struct {
	Capabilities FlapCapabilities  `json:"capabilities"`
	Metric       FlapMetric        `json:"metric"`
	Stats        []FlapStatistic   `json:"stats"`
	ActiveFlaps  []FlapSummary     `json:"active_flaps"`
	Peers        []FlapPeerSummary `json:"peers"`
	Sessions     []FlapSessionInfo `json:"sessions"`
	SessionCount int               `json:"session_count"`
	GeneratedAt  int64             `json:"generated_at"`
}

// FlapPathInfo is one AS-path observation for a prefix.
type FlapPathInfo struct {
	Path              []uint32 `json:"path"`
	AnnouncementCount uint64   `json:"ac"`
	WithdrawalCount   uint64   `json:"wc"`
}

// FlapPrefixDetail is the flap.prefix payload.
type FlapPrefixDetail struct {
	Found       bool           `json:"found"`
	Prefix      string         `json:"prefix"`
	FirstSeen   int64          `json:"first_seen"`
	RateSec     int            `json:"rate_sec"`
	TotalCount  uint64         `json:"total_count"`
	PathHistory []FlapPathInfo `json:"path_history"`
}
