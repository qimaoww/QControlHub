package authn

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

func ParseTrustedProxies(values []string) ([]*net.IPNet, error) {
	result := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if address := net.ParseIP(value); address != nil {
			bits := 128
			if address.To4() != nil {
				address = address.To4()
				bits = 32
			}
			result = append(result, &net.IPNet{IP: address, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q", value)
		}
		result = append(result, network)
	}
	return result, nil
}

func ClientIP(request *http.Request, trustedProxies []*net.IPNet) string {
	remoteHost, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		remoteHost = request.RemoteAddr
	}
	remote := net.ParseIP(strings.Trim(remoteHost, "[]"))
	if remote == nil || !ipTrusted(remote, trustedProxies) {
		return remoteHost
	}
	forwarded := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
	chain := make([]net.IP, 0, len(forwarded)+1)
	for _, value := range forwarded {
		address := net.ParseIP(strings.TrimSpace(value))
		if address == nil {
			return remoteHost
		}
		chain = append(chain, address)
	}
	chain = append(chain, remote)
	for index := len(chain) - 1; index >= 0; index-- {
		if !ipTrusted(chain[index], trustedProxies) {
			return chain[index].String()
		}
	}
	if len(chain) > 0 {
		return chain[0].String()
	}
	return remoteHost
}

// PublicClientIP applies the same trusted-proxy chain used for authentication
// rate limits, then returns only a canonical, publicly routable source IP.
func PublicClientIP(request *http.Request, trustedProxies []*net.IPNet) string {
	return NormalizePublicIP(ClientIP(request, trustedProxies))
}

// VerifiedAgentPublicIP resolves the authenticated Agent's source address only
// when the forwarding chain unambiguously proves a single client. It is
// deliberately stricter than PublicClientIP, which is reserved for rate-limit
// identity and tolerates attacker-controlled left-edge forgery:
//   - an untrusted direct peer is accepted (and normalized) only when it is a
//     public unicast address; any X-Forwarded-For it sets is ignored;
//   - a trusted direct peer is accepted only when, after stripping every
//     configured trusted proxy from the right edge, exactly one untrusted
//     address remains with no further untrusted forwarding entry to its left.
//
// A chain that still contains an untrusted hop to the left of the rightmost
// untrusted entry (for example a CDN edge followed by another proxy) is proxy
// ambiguity and returns "" so the caller never stores a relay as the Agent
// address. PublicClientIP is unchanged for authentication/rate-limit use.
func VerifiedAgentPublicIP(request *http.Request, trustedProxies []*net.IPNet) string {
	remoteHost, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		remoteHost = request.RemoteAddr
	}
	remote := net.ParseIP(strings.Trim(remoteHost, "[]"))
	if remote == nil {
		return ""
	}
	if !ipTrusted(remote, trustedProxies) {
		// The direct peer is an untrusted public client. A forwarding header it
		// sends is attacker-controlled and must never be honored.
		return NormalizePublicIP(remote.String())
	}
	forwarded := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
	chain := make([]net.IP, 0, len(forwarded)+1)
	for _, value := range forwarded {
		address := net.ParseIP(strings.TrimSpace(value))
		if address == nil {
			return ""
		}
		chain = append(chain, address)
	}
	chain = append(chain, remote)
	// Strip trusted proxies from the right edge. Accept a single untrusted entry
	// only when no untrusted entry appears to its left; otherwise the candidate
	// itself forwarded the request and is a relay, not the Agent.
	var candidate net.IP
	for index := len(chain) - 1; index >= 0; index-- {
		if ipTrusted(chain[index], trustedProxies) {
			continue
		}
		candidate = chain[index]
		for left := index - 1; left >= 0; left-- {
			if !ipTrusted(chain[left], trustedProxies) {
				return ""
			}
		}
		break
	}
	if candidate == nil {
		return ""
	}
	return NormalizePublicIP(candidate.String())
}

// NormalizePublicIP canonicalizes publicly routable IPv4 and IPv6 addresses.
// Private and IANA special-purpose values are rejected.
func NormalizePublicIP(value string) string {
	address, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(value), "[]"))
	if err != nil {
		return ""
	}
	address = address.Unmap()
	if !netpolicy.IsPublicAddress(address) {
		return ""
	}
	return address.String()
}

func ipTrusted(address net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network.Contains(address) {
			return true
		}
	}
	return false
}
