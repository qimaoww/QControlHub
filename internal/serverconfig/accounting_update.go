package serverconfig

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// AccountingUpdateSource removes only unchanged generated artifacts from a
// previously validated on-disk configuration. The editable originals can then
// be recompiled. Edited or newly introduced reserved artifacts fail closed.
func AccountingUpdateSource(engine core.Engine, content, previous string) (string, error) {
	plan, err := PrepareAccounting(engine, previous)
	if err != nil && engine == core.EngineSingBox {
		plan, err = PrepareMarkedSingBoxAccounting(previous)
	}
	if err != nil {
		// A full managed snapshot can repair a broken saved source. Rebuild
		// and verify its complete plan before trusting any reserved artifacts.
		// Do not use this path for edits against a valid previous plan: its
		// generated objects remain read-only, even if edited together.
		plan, candidateErr := PrepareAccounting(engine, content)
		if candidateErr != nil && engine == core.EngineSingBox {
			plan, candidateErr = PrepareMarkedSingBoxAccounting(content)
		}
		if candidateErr == nil {
			return stripVerifiedAccounting(engine, content, plan.Content)
		}
		return "", fmt.Errorf("cannot verify submitted accounting configuration: %w", candidateErr)
	}
	// A verified historical runtime can precede a compiler migration (for
	// example, socket marks added to protocol exits). Accept its exact original
	// artifacts as well as the current reproducible plan. Editable revisions
	// contain no artifacts, so runtime round trips use the compiled plan below.
	if source, err := stripVerifiedAccounting(engine, content, previous); err == nil {
		return source, nil
	}
	return stripVerifiedAccounting(engine, content, plan.Content)
}

// AccountingSourceFromRevision recovers stale generated references only when
// the complete old plan can still be reproduced from a saved revision. Merely
// using a reserved tag never establishes ownership of an outbound or rule.
func AccountingSourceFromRevision(engine core.Engine, content, revision string) (string, error) {
	plan, err := PrepareAccounting(engine, revision)
	if err == nil {
		var source string
		source, err = stripVerifiedAccounting(engine, content, plan.Content)
		if err == nil {
			return source, nil
		}
	}
	if engine == core.EngineSingBox {
		// A saved core-API plan can have a marked runtime on official builds.
		// Try that verified conversion even when the archive's primary plan
		// compiled successfully but did not match the submitted artifacts.
		marked, markedErr := PrepareMarkedSingBoxAccounting(revision)
		if markedErr == nil {
			return stripVerifiedAccounting(engine, content, marked.Content)
		}
	}
	return "", err
}

func stripVerifiedAccounting(engine core.Engine, content, previous string) (string, error) {
	var err error
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
		data, err := json.MarshalIndent(root, "", "  ")
		return string(data) + "\n", err
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
	if engine == core.EngineMihomo && root["proxy-groups"] != nil {
		currentGroups, _ := root["proxy-groups"].([]any)
		oldGroups, _ := old["proxy-groups"].([]any)
		root["proxy-groups"], err = strip(currentGroups, oldGroups, func(v any) string { return stringValue(mapValue(v)["name"]) })
		if err != nil {
			return "", err
		}
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
		// A verified, unchanged generated API belongs to the accounting plan
		// too. Removing only its outbound allow-list leaves an unsupported
		// v2ray_api in the source when the final inbound has no next plan.
		if reflect.DeepEqual(mapValue(root["experimental"])["v2ray_api"], mapValue(old["experimental"])["v2ray_api"]) {
			removeManagedSingBoxStatsAPI(root)
		}
		// If the API was edited, retain that edit but remove verified generated
		// references from its allow-list before the next accounting plan.
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
		data, err = json.MarshalIndent(root, "", "  ")
		data = append(data, '\n')
	}
	return string(data), err
}
