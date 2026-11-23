package serverconfig

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Surge has no portable snell:// URI. Values are quoted using its INI policy
// syntax; policy names cannot contain the delimiters of a declaration.
func buildSnellSurgeConfig(input Input, address, name string) (string, error) {
	if name == "" {
		name = "Snell"
	}
	if err := ValidateSurgePolicyName(name); err != nil {
		return "", err
	}
	// Surge documents quoted comma-separated values, but does not specify an
	// escape grammar for literal quotes or backslashes in these credentials.
	if strings.ContainsAny(input.Credential+input.SnellShadowTLSPassword, "\"\\") {
		return "", fmt.Errorf("Surge 无法可靠表达包含引号或反斜线的凭据，请使用 Mihomo 格式")
	}
	version := input.SnellVersion
	// Mihomo v5 accepts v4 wire traffic, but does not implement Surge's v5
	// QUIC Proxy (UDP over UDP). Export v4 so all UDP stays on the TCP tunnel.
	if version == 5 {
		version = 4
	}
	parts := []string{name + " = snell", address, strconv.Itoa(input.Port),
		"psk = " + surgeValue(input.Credential), "version = " + strconv.Itoa(version)}
	if version == 6 {
		parts = append(parts, "mode = "+input.SnellMode)
	}
	parts = append(parts, "reuse = "+strconv.FormatBool(input.SnellReuse), "tfo = true", "udp-relay = "+strconv.FormatBool(input.SnellUDP))
	if input.SnellObfsMode == SnellObfsShadowTLS {
		parts = append(parts, "shadow-tls-password = "+surgeValue(input.SnellShadowTLSPassword),
			"shadow-tls-sni = "+input.SnellObfsHost, "shadow-tls-version = 3")
		if alpn, _ := validatedALPN(input.SnellShadowTLSALPN); len(alpn) > 0 {
			parts = append(parts, "alpn = "+surgeValue(strings.Join(alpn, ",")))
		}
	}
	return strings.Join(parts, ", "), nil
}

func ValidateSurgePolicyName(name string) error {
	if strings.TrimSpace(name) != name || name == "" || strings.ContainsAny(name, "=,\"\\;#[]") || strings.ContainsFunc(name, unicode.IsControl) {
		return fmt.Errorf("Surge 节点名称不能包含首尾空白、控制字符或 = , \" \\ ; # [ ]，请使用 Mihomo 格式")
	}
	return nil
}

func surgeValue(value string) string {
	if strings.ContainsAny(value, ",\"\\;#") || strings.ContainsFunc(value, unicode.IsSpace) {
		return strconv.Quote(value)
	}
	return value
}
