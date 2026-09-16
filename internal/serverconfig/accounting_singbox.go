package serverconfig

import (
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Official builds may omit with_v2ray_api. Linux routing_mark supplies the
// same dedicated-outbound attribution without requiring a custom binary.
func PrepareMarkedSingBoxAccounting(content string) (AccountingPlan, error) {
	// Share validation and clone generation with bound exits. In particular,
	// validate generated marks instead of stripping them before verification.
	return prepareAccounting(core.EngineSingBox, content, true)
}

// Only a user-space, server-only WireGuard endpoint has the same attributable
// ingress contract as an inbound. System interfaces, peer dialers and relay
// protocols can bypass those routes and require a separate accounting design.
func singBoxAccountingEntries(root map[string]any) ([]any, error) {
	var entries []any
	for _, key := range []string{"inbounds", "endpoints"} {
		raw, exists := root[key]
		if !exists {
			continue
		}
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("sing-box %s must be an array", key)
		}
		if key == "endpoints" {
			outbounds, _ := root["outbounds"].([]any)
			for _, item := range list {
				endpoint := mapValue(item)
				if _, ok := parseSingBoxWireGuardEndpoint(endpoint); !ok {
					return nil, fmt.Errorf("sing-box endpoint %q requires explicit independent exit mapping; only user-space server WireGuard presets are supported", stringValue(endpoint["tag"]))
				}
				for _, rawOutbound := range outbounds {
					if mapValue(rawOutbound)["tag"] == endpoint["tag"] {
						return nil, fmt.Errorf("sing-box endpoint tag conflicts with an outbound")
					}
				}
			}
		}
		entries = append(entries, list...)
	}
	return entries, nil
}
