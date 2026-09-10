package serverconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

// AccountingUpdateSource removes only unchanged generated artifacts from a
// previously validated on-disk configuration. The editable originals can then
// be recompiled. Edited or newly introduced reserved artifacts fail closed.
func AccountingUpdateSource(engine core.Engine, content, previous string) (string, error) {
	_, err := PrepareAccounting(engine, previous)
	if err != nil && engine == core.EngineSingBox {
		_, err = PrepareMarkedSingBoxAccounting(previous)
	}
	if err != nil {
		return "", fmt.Errorf("cannot verify previous accounting configuration: %w", err)
	}
	decode := func(value string) (map[string]any, error) {
		var root map[string]any
		if engine == core.EngineMihomo {
			err := yaml.Unmarshal([]byte(value), &root)
			return root, err
		}
		decoder := json.NewDecoder(strings.NewReader(value))
		decoder.UseNumber()
		err := decoder.Decode(&root)
		return root, err
	}
	root, err := decode(content)
	if err != nil {
		return "", err
	}
	old, err := decode(previous)
	if err != nil {
		return "", err
	}
	if root == nil || old == nil {
		return "", fmt.Errorf("invalid accounting configuration")
	}
	if engine == core.EngineShadowsocksRust {
		// Ports and IDs may be edited in source. Only marks present in the
		// verified previous revision may be removed and replanned; arbitrary
		// user socket marks must never be silently overwritten.
		entries := func(document map[string]any) []any {
			result := []any{document}
			servers, _ := document["servers"].([]any)
			return append(result, servers...)
		}
		trusted := make(map[int]bool)
		for _, raw := range entries(old) {
			trusted[intValue(mapValue(raw)["outbound_fwmark"])] = true
		}
		for _, raw := range entries(root) {
			entry := mapValue(raw)
			mark, present := entry["outbound_fwmark"]
			if !present {
				continue
			}
			number, numeric := mark.(json.Number)
			value, numberErr := number.Int64()
			if !numeric || numberErr != nil || value < 0 || value > 0xffffffff || (value != 0 && !trusted[int(value)]) {
				return "", fmt.Errorf("generated outbound marks are read-only; edit the server port or outbound binding")
			}
			delete(entry, "outbound_fwmark")
		}
		data, err := json.Marshal(root)
		return string(data), err
	}
	outsKey, tagKey, routeKey, targetKey := "outbounds", "tag", "route", "outbound"
	if engine == core.EngineXray {
		routeKey, targetKey = "routing", "outboundTag"
	}
	if engine == core.EngineMihomo {
		outsKey, tagKey = "proxies", "name"
	}
	strip := func(current, trusted []any, target func(any) string) ([]any, error) {
		var result []any
		for _, value := range current {
			if !strings.HasPrefix(target(value), accountingPrefix) {
				result = append(result, value)
				continue
			}
			if !accountingGeneratedSubset([]any{value}, trusted) {
				return nil, fmt.Errorf("generated accounting entries are read-only; edit the original outbound or routing rule")
			}
		}
		return result, nil
	}
	currentOuts, _ := root[outsKey].([]any)
	oldOuts, _ := old[outsKey].([]any)
	root[outsKey], err = strip(currentOuts, oldOuts, func(v any) string { return stringValue(mapValue(v)[tagKey]) })
	if err != nil {
		return "", err
	}
	currentRoute, oldRoute := mapValue(root[routeKey]), mapValue(old[routeKey])
	target := func(v any) string { return stringValue(mapValue(v)[targetKey]) }
	if engine == core.EngineMihomo {
		currentRoute, oldRoute = root, old
		target = func(v any) string { return accountingMihomoTarget(stringValue(v)) }
	}
	if currentRoute != nil {
		currentRules, _ := currentRoute["rules"].([]any)
		oldRules, _ := oldRoute["rules"].([]any)
		currentRoute["rules"], err = strip(currentRules, oldRules, target)
		if err != nil {
			return "", err
		}
	}
	if engine == core.EngineSingBox {
		// The stats allow-list also references generated exits. Normally the
		// next plan replaces it, but deleting the final inbound has no next
		// plan and must not leave dangling references in the saved source.
		stats := mapValue(mapValue(mapValue(root["experimental"])["v2ray_api"])["stats"])
		oldStats := mapValue(mapValue(mapValue(old["experimental"])["v2ray_api"])["stats"])
		if stats != nil {
			currentNames, _ := stats["outbounds"].([]any)
			oldNames, _ := oldStats["outbounds"].([]any)
			stats["outbounds"], err = strip(currentNames, oldNames, stringValue)
			if err != nil {
				return "", err
			}
			tags := make(map[string]bool)
			inbounds, _ := root["inbounds"].([]any)
			for _, raw := range inbounds {
				tags[stringValue(mapValue(raw)["tag"])] = true
			}
			names, _ := stats["inbounds"].([]any)
			kept := []any{}
			for _, name := range names {
				if tags[stringValue(name)] {
					kept = append(kept, name)
				}
			}
			stats["inbounds"] = kept
		}
	}
	var data []byte
	if engine == core.EngineMihomo {
		data, err = yaml.Marshal(root)
	} else {
		data, err = json.Marshal(root)
	}
	return string(data), err
}
