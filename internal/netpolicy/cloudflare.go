package netpolicy

import "net/netip"

// Cloudflare's published edge ranges. They are relay addresses for an Agent
// connection, not proof of the Agent's own public egress. Keep this policy
// separate from IsPublicAddress: a Cloudflare address can be globally routable
// while still being the wrong source for an authenticated Agent.
var cloudflarePrefixes = mustParsePrefixes([]string{
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"108.162.192.0/18",
	"131.0.72.0/22",
	"141.101.64.0/18",
	"162.158.0.0/15",
	"172.64.0.0/13",
	"173.245.48.0/20",
	"188.114.96.0/20",
	"190.93.240.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
})

// IsCloudflareAddress identifies a known Cloudflare edge address. Mapped
// IPv4-in-IPv6 values are unmapped before matching so both wire spellings have
// identical relay semantics.
func IsCloudflareAddress(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	for _, prefix := range cloudflarePrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
