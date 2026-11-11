package core

// ProxyIngress grants one shared configuration owner access to a managed
// provider/port on its allocation's node. It never grants a backend listener.
type ProxyIngress struct {
	Mode       string `json:"mode"`
	Port       int    `json:"port"`
	Generation int64  `json:"generation,omitempty"`
}

func (g ProxyIngress) Valid() bool {
	return (g.Mode == "nginx" || g.Mode == "caddy") && g.Port > 0 && g.Port <= 65535 && g.Port != 10085 && g.Port != 10086
}

func (g ProxyIngress) Matches(f TLSFrontend) bool {
	return !f.LocalOnly && g.Port == f.PublicPort && g.Mode == f.Mode
}

func ProxyIngressAuthorized(grants []ProxyIngress, f TLSFrontend) bool {
	if f.SharedIngressGeneration == 0 {
		return true
	}
	for _, g := range grants {
		if g.Matches(f) && g.Generation == f.SharedIngressGeneration {
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
	seen := map[int]bool{}
	for _, g := range grants {
		if !g.Valid() || g.Generation < 1 || seen[g.Port] {
			return false
		}
		seen[g.Port] = true
	}
	return true
}
