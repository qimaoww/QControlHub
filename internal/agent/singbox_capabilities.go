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
		Endpoints []struct {
			Type   string `json:"type"`
			System *bool  `json:"system,omitempty"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return fmt.Errorf("decode sing-box endpoint capabilities: %w", err)
	}
	if len(root.Endpoints) == 0 {
		return nil
	}
	required := map[string]struct {
		major, minor int
		tag          string
	}{
		"wireguard":      {1, 11, "with_wireguard"},
		"tailscale":      {1, 12, "with_tailscale"},
		"openvpn-server": {1, 14, "with_openvpn"},
	}
	known := false
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
