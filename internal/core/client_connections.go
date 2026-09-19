package core

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const AgentFeatureClientConnections = "client-connections-v1"
const MaxClientConnections = 512
const ClientConnectionRetention = 7 * 24 * time.Hour

// These are observations of network flows, not authenticated client sessions.
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

type ClientConnectionReport struct {
	Connections []ClientConnection `json:"connections"`
	// Partial coverage must never be interpreted as an empty, healthy sample.
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Truncated bool   `json:"truncated"`
}

var connectionProtocolPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

func (r ClientConnectionReport) Validate() error {
	if len(r.Connections) > MaxClientConnections || (r.Status != "ok" && r.Status != "partial" && r.Status != "unavailable") || len(r.Detail) > 1000 || !utf8.ValidString(r.Detail) || strings.ContainsRune(r.Detail, '\x00') {
		return fmt.Errorf("invalid connection report")
	}
	if r.Status == "unavailable" && len(r.Connections) > 0 {
		return fmt.Errorf("unavailable report has connections")
	}
	for _, c := range r.Connections {
		client, ce := netip.ParseAddr(c.ClientIP)
		local, le := netip.ParseAddr(c.LocalIP)
		if !c.Engine.Valid() || !connectionProtocolPattern.MatchString(c.Protocol) || len(c.Inbound) > 400 || !utf8.ValidString(c.Inbound) || strings.ContainsRune(c.Inbound, '\x00') ||
			(c.Transport != "tcp" && c.Transport != "udp") || ce != nil || le != nil || client.Zone() != "" || local.Zone() != "" || client.IsUnspecified() || client.IsMulticast() || local.IsUnspecified() ||
			c.ClientPort < 1 || c.ClientPort > 65535 || c.LocalPort < 1 || c.LocalPort > 65535 {
			return fmt.Errorf("invalid client connection")
		}
	}
	return nil
}

type ClientConnectionRecord struct {
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
