package core

import "time"

const ClientConnectionRetention = 7 * 24 * time.Hour

// These are observations extracted from panel core logs, not session counts.
// Empty metadata and zero local port mean the log did not include the field.
type ClientConnection struct {
	Engine     Engine `json:"engine"`
	Protocol   string `json:"protocol"`
	Inbound    string `json:"inbound"`
	Transport  string `json:"transport"`
	ClientIP   string `json:"client_ip"`
	ClientPort int    `json:"client_port"`
	LocalIP    string `json:"local_ip"`
	LocalPort  int    `json:"local_port"`
}

type ClientIPLocation struct {
	CountryCode string `json:"country_code,omitempty"`
	Country     string `json:"country,omitempty"`
	Province    string `json:"province,omitempty"`
	NonPublic   bool   `json:"non_public,omitempty"`
}

type ClientConnectionEndpoint struct {
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Engine    Engine `json:"engine"`
	LocalPort int    `json:"local_port"`
}

type ClientConnectionRecord struct {
	Endpoints []ClientConnectionEndpoint `json:"endpoints,omitempty"`
	Location  ClientIPLocation           `json:"location"`
	ClientConnection
	ID        int64     `json:"id"`
	AgentID   string    `json:"agent_id"`
	AgentName string    `json:"agent_name"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

type ClientConnectionBucket struct {
	Time  time.Time `json:"time"`
	Flows int64     `json:"flows"`
	IPs   int64     `json:"ips"`
}

type ClientConnectionSource struct {
	AgentID   string     `json:"agent_id"`
	AgentName string     `json:"agent_name"`
	UpdatedAt *time.Time `json:"updated_at"`
	Status    string     `json:"status"`
	Detail    string     `json:"detail"`
	Truncated bool       `json:"truncated"`
}

type ClientConnectionHistory struct {
	Records    []ClientConnectionRecord `json:"records"`
	Timeline   []ClientConnectionBucket `json:"timeline"`
	Sources    []ClientConnectionSource `json:"sources"`
	Flows      int64                    `json:"flows"`
	IPs        int64                    `json:"ips"`
	NextBefore int64                    `json:"next_before,omitempty"`
}
