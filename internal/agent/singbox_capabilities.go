package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var singBoxVersionPattern = regexp.MustCompile(`sing-box version v?([0-9]+)\.([0-9]+)\.([0-9]+)`)

// validateSingBoxCapabilities checks endpoint requirements against the
// running binary's own version and build tags. The subsequent `check` command
// remains authoritative for the complete configuration syntax.
func validateSingBoxCapabilities(ctx context.Context, binary, content string) error {
	var root struct {
		Inbounds  []map[string]any `json:"inbounds"`
		Outbounds []map[string]any `json:"outbounds"`
		Endpoints []struct {
			Type   string `json:"type"`
			System *bool  `json:"system,omitempty"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return fmt.Errorf("decode sing-box endpoint capabilities: %w", err)
	}
	type requirement struct {
		name  string
		minor int
		tag   string
	}
	var inboundRequirements []requirement
	for _, entries := range [][]map[string]any{root.Inbounds, root.Outbounds} {
		for _, entry := range entries {
			kind, _ := entry["type"].(string)
			if kind == "anytls" {
				inboundRequirements = append(inboundRequirements, requirement{"AnyTLS", 12, ""})
			}
			if kind == "tuic" {
				inboundRequirements = append(inboundRequirements, requirement{"TUIC v5", 4, "with_quic"})
			}
			if kind == "hysteria2" {
				inboundRequirements = append(inboundRequirements, requirement{"Hysteria2", 5, "with_quic"})
			}
			transport, _ := entry["transport"].(map[string]any)
			if transport["type"] == "quic" {
				inboundRequirements = append(inboundRequirements, requirement{"QUIC transport", 0, "with_quic"})
			}
			if transport["type"] == "httpupgrade" {
				inboundRequirements = append(inboundRequirements, requirement{"HTTPUpgrade", 7, ""})
			}
			tls, _ := entry["tls"].(map[string]any)
			reality, _ := tls["reality"].(map[string]any)
			if reality["enabled"] == true {
				inboundRequirements = append(inboundRequirements, requirement{"Reality", 2, "with_utls"})
			}
		}
	}
	required := map[string]struct {
		major, minor int
		tag          string
	}{
		"wireguard":      {1, 11, "with_wireguard"},
		"tailscale":      {1, 12, "with_tailscale"},
		"openvpn-server": {1, 14, "with_openvpn"},
	}
	known := len(inboundRequirements) > 0
	for _, endpoint := range root.Endpoints {
		if _, ok := required[endpoint.Type]; ok {
			known = true
		}
	}
	if !known {
		return nil // The native check remains authoritative for future types.
	}
	output, err := run(ctx, binary, "version")
	if err != nil {
		return fmt.Errorf("inspect sing-box capabilities: %w", err)
	}
	match := singBoxVersionPattern.FindStringSubmatch(output)
	if len(match) != 4 {
		return fmt.Errorf("sing-box version output is unrecognized; cannot verify endpoint support")
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	tags := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "Tags:"); ok {
			for _, tag := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
				tags[tag] = true
			}
		}
	}
	needGVisor := false
	for _, req := range inboundRequirements {
		if major < 1 || major == 1 && minor < req.minor {
			return fmt.Errorf("sing-box %s requires sing-box 1.%d or newer (installed %s)", req.name, req.minor, match[0])
		}
		if req.tag != "" && !tags[req.tag] {
			return fmt.Errorf("sing-box %s is unavailable in this build (missing %s)", req.name, req.tag)
		}
	}
	for _, endpoint := range root.Endpoints {
		req, ok := required[endpoint.Type]
		if !ok {
			continue
		}
		if endpoint.Type == "tailscale" {
			// The Tailscale endpoint implementation is gVisor-backed in
			// sing-box, regardless of its system_interface setting.
			needGVisor = true
		}
		if endpoint.System == nil || !*endpoint.System {
			needGVisor = true
		}
		if major < req.major || (major == req.major && minor < req.minor) {
			return fmt.Errorf("sing-box %s endpoint requires sing-box %d.%d or newer (installed %s)", endpoint.Type, req.major, req.minor, match[1]+"."+match[2]+"."+match[3])
		}
		if !tags[req.tag] {
			return fmt.Errorf("sing-box %s endpoint is unavailable in this build (missing %s)", endpoint.Type, req.tag)
		}
	}
	if needGVisor {
		if !tags["with_gvisor"] {
			return fmt.Errorf("selected sing-box endpoints require a build with with_gvisor")
		}
	}
	return nil
}
