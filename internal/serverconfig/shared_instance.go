package serverconfig

import (
	"encoding/json"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// SharedInstanceRuntimeContent removes fixed local statistics APIs from a
// private process. Shared quotas use authenticated per-port ingress counters;
// keeping the generated 10085/10086 listeners would make two processes of the
// same engine fight for the same loopback port. The independent exits remain.
func SharedInstanceRuntimeContent(engine core.Engine, content string) (string, error) {
	if engine != core.EngineXray && engine != core.EngineSingBox {
		return content, nil
	}
	if err := ValidateIndependentEgress(engine, content); err != nil {
		return "", err
	}
	root, err := decodeAccountingRoot(engine, content)
	if err != nil {
		return "", err
	}
	changed := false
	if engine == core.EngineXray {
		if api := mapValue(root["api"]); api != nil {
			address, err := xrayAccountingAPIAddress(root)
			if err != nil || address != "127.0.0.1:10085" {
				return "", fmt.Errorf("shared Xray instance requires the managed statistics API")
			}
			apiTag := stringValue(api["tag"])
			if apiTag == "" {
				apiTag = "qch-stat-api"
			}
			entries, _ := root["inbounds"].([]any)
			kept := make([]any, 0, len(entries))
			for _, raw := range entries {
				entry := mapValue(raw)
				if stringValue(entry["tag"]) == apiTag && xrayInternalAPIInbound(root, entry) {
					changed = true
					continue
				}
				kept = append(kept, raw)
			}
			root["inbounds"] = kept
			if routing := mapValue(root["routing"]); routing != nil {
				rules, _ := routing["rules"].([]any)
				keptRules := make([]any, 0, len(rules))
				for _, raw := range rules {
					rule := mapValue(raw)
					if stringValue(rule["outboundTag"]) == apiTag || listContainsString(rule["inboundTag"], apiTag) {
						changed = true
						continue
					}
					keptRules = append(keptRules, raw)
				}
				routing["rules"] = keptRules
			}
			delete(root, "api")
			delete(root, "stats")
			changed = true
		}
	} else if experimental := mapValue(root["experimental"]); experimental != nil {
		if api := mapValue(experimental["v2ray_api"]); api != nil {
			if stringValue(api["listen"]) != "127.0.0.1:10086" {
				return "", fmt.Errorf("shared sing-box instance requires the managed statistics API")
			}
			delete(experimental, "v2ray_api")
			if len(experimental) == 0 {
				delete(root, "experimental")
			}
			changed = true
		}
	}
	if !changed {
		return content, nil
	}
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	result := string(encoded) + "\n"
	if _, err := SharedTrafficEndpoints(engine, result); err != nil {
		return "", err
	}
	return result, nil
}

func listContainsString(value any, wanted string) bool {
	if value == wanted {
		return true
	}
	items, _ := value.([]any)
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}
