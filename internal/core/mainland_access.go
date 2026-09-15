package core

// MainlandAccessPolicy describes a per-inbound mainland access restriction.
// Core-native policies are rendered into the proxy configuration; engines
// without routing support (for example Shadowsocks Rust) apply the same
// policy through an Agent-managed native ACL and per-port firewall rules.
type MainlandAccessPolicy struct {
	CNIPPrefixes             []string `json:"cnip_prefixes,omitempty"`
	AgentID                  string   `json:"agent_id,omitempty"`
	ConfigVersion            int      `json:"config_version,omitempty"`
	Tag                      string   `json:"tag"`
	Port                     int      `json:"port"`
	Kind                     string   `json:"kind"`
	Engine                   Engine   `json:"engine"`
	BlockMainlandDestination bool     `json:"block_mainland_destination"`
	BlockMainlandSource      bool     `json:"block_mainland_source"`
}
