package serverconfig

import (
	"strconv"
	"strings"
)

// buildSnellSurgeConfig emits the single-line Surge profile used by the
// Snell installer and by Surge's native configuration format. Snell does not
// have a portable URI, but Sub-Store parses Surge lines in local subscriptions.
func buildSnellSurgeConfig(input Input, address, fragment string) string {
	name := strings.NewReplacer("=", "", ",", "").Replace(fragment)
	if name == "" {
		name = "Snell"
	}
	parts := []string{
		name + " = snell",
		address,
		strconv.Itoa(input.Port),
		"psk = " + input.Credential,
		"version = " + strconv.Itoa(input.SnellVersion),
	}
	if input.SnellReuse {
		parts = append(parts, "reuse = true")
	}
	parts = append(parts, "tfo = true")
	if input.SnellUDP {
		parts = append(parts, "udp-relay = true")
	}
	if input.SnellObfsMode == SnellObfsShadowTLS {
		if input.SnellShadowTLSPassword != "" {
			parts = append(parts, "shadow-tls-password = "+input.SnellShadowTLSPassword)
		}
		if input.SnellObfsHost != "" {
			parts = append(parts, "shadow-tls-sni = "+input.SnellObfsHost)
		}
		if input.SnellShadowTLSVersion > 0 {
			parts = append(parts, "shadow-tls-version = "+strconv.Itoa(input.SnellShadowTLSVersion))
		}
	}
	return strings.Join(parts, ", ")
}
