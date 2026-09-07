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
	var data []byte
	if engine == core.EngineMihomo {
		data, err = yaml.Marshal(root)
	} else {
		data, err = json.Marshal(root)
	}
	return string(data), err
}
