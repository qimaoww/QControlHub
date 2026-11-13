package core

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ProxyIngress grants a managed provider on an allocation's node. Scope=type
// never reserves a listener. Port is accepted only for legacy wire policies.
type ProxyIngress struct {
	Mode       string `json:"mode"`
	Scope      string `json:"scope,omitempty"`
	Port       int    `json:"port,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	// Legacy leases preserve existing deployments during upgrade, and are
	// discarded on withdrawal. Only the server may create these leases.
	Legacy []ProxyIngressLease `json:"legacy,omitempty"`
}

type ProxyIngressLease struct {
	Port       int   `json:"port"`
	Generation int64 `json:"generation"`
}

const ProxyIngressTypeScope = "type"

func (g *ProxyIngress) UnmarshalJSON(data []byte) error {
	type wire ProxyIngress
	var value struct {
		*wire
		Port *int `json:"port"`
	}
	next := wire{}
	value.wire = &next
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if value.Port != nil {
		if !ValidProxyPort(*value.Port) {
			return fmt.Errorf("无效的旧共享端口")
		}
		next.Port = *value.Port
	}
	*g = ProxyIngress(next)
	return nil
}

func ValidProxyMode(mode string) bool { return mode == "nginx" || mode == "caddy" }
func ValidProxyPort(port int) bool {
	return port > 0 && port <= 65535 && port != 10085 && port != 10086
}

func (g ProxyIngress) Valid() bool {
	if !ValidProxyMode(g.Mode) || len(g.Legacy) > 32 {
		return false
	}
	for _, lease := range g.Legacy {
		if !ValidProxyPort(lease.Port) || lease.Generation < 1 {
			return false
		}
	}
	return g.Scope == ProxyIngressTypeScope && g.Port == 0 || g.Scope == "" && ValidProxyPort(g.Port) && len(g.Legacy) == 0
}

func (g ProxyIngress) Matches(f TLSFrontend) bool {
	return g.Valid() && !f.LocalOnly && ValidProxyPort(f.PublicPort) && g.Mode == f.Mode && (g.Scope == ProxyIngressTypeScope || g.Port == f.PublicPort)
}

func (g ProxyIngress) Authorizes(f TLSFrontend) bool {
	if !g.Matches(f) {
		return false
	}
	if g.Generation == f.SharedIngressGeneration {
		return true
	}
	for _, lease := range g.Legacy {
		if lease.Port == f.PublicPort && lease.Generation == f.SharedIngressGeneration {
			return true
		}
	}
	return false
}

func ProxyIngressAuthorized(grants []ProxyIngress, f TLSFrontend) bool {
	if f.SharedIngressGeneration == 0 {
		return true
	}
	for _, g := range grants {
		if g.Authorizes(f) {
			return true
		}
	}
	return false
}

type ProxyIngressReport struct {
	ShareID  string `json:"share_id"`
	Revision int64  `json:"revision"`
	Revoked  bool   `json:"revoked"`
	Error    string `json:"error,omitempty"`
}

func proxyIngressPolicyValid(grants []ProxyIngress, revision int64) bool {
	if len(grants) > 32 || revision < 0 {
		return false
	}
	seen := map[string]bool{}
	ports := map[int]bool{}
	scope := ""
	if len(grants) > 0 {
		scope = grants[0].Scope
	}
	for _, g := range grants {
		if !g.Valid() || g.Scope != scope || g.Generation < 1 || g.Scope == ProxyIngressTypeScope && seen[g.Mode] || g.Scope == "" && ports[g.Port] {
			return false
		}
		seen[g.Mode] = true
		ports[g.Port] = true
	}
	return true
}
