package core

// KomariNode is the read-only billing and traffic configuration returned by a
// linked Komari monitor. Traffic values are kept in Komari's native byte unit.
type KomariNode struct {
	UUID                           string `json:"uuid"`
	Name                           string `json:"name,omitempty"`
	BillingCycle                   int64  `json:"billing_cycle"`
	TrafficLimit                   int64  `json:"traffic_limit"`
	TrafficLimitType               string `json:"traffic_limit_type,omitempty"`
	EffectiveTrafficLimit          int64  `json:"effective_traffic_limit"`
	EffectiveTrafficType           string `json:"effective_traffic_type,omitempty"`
	EffectiveTrafficLimitAvailable bool   `json:"effective_traffic_limit_available,omitempty"`
	EffectiveTrafficTypeAvailable  bool   `json:"effective_traffic_type_available,omitempty"`
	TrafficResetDay                int64  `json:"traffic_reset_day,omitempty"`
	TrafficUsed                    int64  `json:"traffic_used"`
	TrafficUsedAvailable           bool   `json:"traffic_used_available,omitempty"`
	ExpiredAt                      string `json:"expired_at,omitempty"`
	UpdatedAt                      string `json:"updated_at,omitempty"`
}

// KomariLink is returned by the node settings endpoint. Server is omitted
// when the UUID is not configured or when Komari is unavailable.
type KomariLink struct {
	UUID   string      `json:"uuid,omitempty"`
	Server *KomariNode `json:"server,omitempty"`
}
