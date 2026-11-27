package serverconfig

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// Binding is part of the same candidate revision as an added exit. Conditional
// routes stay ahead of the per-inbound fallback, and other listeners stay intact.
func BindManagedOutbound(engine core.Engine, source, inbound string, port int, outbound string, state ManagedState, fallbacks ...string) (string, error) {
	root, err := managedDocument(engine, source)
	if err != nil {
		return "", err
	}
	objects := managedObjects(engine, root, state)
	listeners := 0
	available := map[string]int{}
	if engine == core.EngineMihomo {
		available["DIRECT"] = 1
	}
	for _, object := range objects {
		if object.Inbound && object.Tag == inbound && object.Port == port {
			listeners++
		}
		if !object.Inbound && object.Enabled && object.Kind != "block" && object.Kind != "blackhole" && object.Kind != "dns" {
			available[object.Tag]++
		}
	}
	if listeners != 1 || available[outbound] != 1 {
		return "", fmt.Errorf("入站或出站已变化，请重新选择")
	}
	groupTag := managedBindingFallbackTag(inbound, port)
	seen := map[string]bool{outbound: true}
	for _, tag := range fallbacks {
		if seen[tag] || available[tag] != 1 {
			return "", fmt.Errorf("备用出口无效或重复，请重新选择")
		}
		seen[tag] = true
	}
	target := outbound
	if len(fallbacks) > 0 {
		target = groupTag
	}
	switch engine {
	case core.EngineMihomo:
		groups, _ := root["proxy-groups"].([]any)
		exists := slices.ContainsFunc(groups, func(raw any) bool { return mapValue(raw)["name"] == groupTag })
		if exists || len(fallbacks) > 0 {
			operation := "add"
			if exists {
				operation = "modify"
			}
			if len(fallbacks) == 0 {
				operation = "delete"
			}
			group := map[string]any{"name": groupTag, "type": "fallback", "proxies": append([]string{outbound}, fallbacks...)}
			fragment, err := yaml.Marshal(map[string]any{"proxy-groups": []any{group}})
			if err != nil {
				return "", err
			}
			source, err = configschema.MutateListItem(engine, source, string(fragment), "proxy-groups", "name", groupTag, operation, "proxies", "type")
			if err != nil {
				return "", err
			}
		}
	case core.EngineXray:
		routing := mapValue(root["routing"])
		balancers, _ := routing["balancers"].([]any)
		kept := slices.DeleteFunc(balancers, func(raw any) bool { return mapValue(raw)["tag"] == groupTag })
		if len(fallbacks) > 0 {
			if len(fallbacks) != 1 {
				return "", fmt.Errorf("Xray 原生 fallbackTag 仅支持一个备用出口")
			}
			for _, object := range objects {
				if !object.Inbound && object.Tag != outbound && strings.HasPrefix(object.Tag, outbound) {
					return "", fmt.Errorf("Xray 主出口标签也是其他出站的前缀，请使用唯一前缀")
				}
			}
			if err := observeXrayFallbackPrimary(root, outbound); err != nil {
				return "", err
			}
			if routing == nil {
				routing = map[string]any{}
				root["routing"] = routing
			}
			kept = append(kept, map[string]any{"tag": groupTag, "selector": []string{outbound}, "fallbackTag": fallbacks[0]})
		}
		if len(fallbacks) > 0 || len(kept) != len(balancers) {
			routing["balancers"] = kept
		}
	default:
		if len(fallbacks) > 0 {
			return "", fmt.Errorf("此内核没有原生故障回退出站机制")
		}
	}
	if engine == core.EngineXray && len(fallbacks) > 0 {
		key := xrayObservatoryKey(root)
		fragment, err := json.Marshal(root[key])
		if err != nil {
			return "", err
		}
		source, err = configschema.MergeFragment(engine, source, key, string(fragment), false)
		if err != nil {
			return "", err
		}
	}
	if engine == core.EngineMihomo {
		rules := stringSliceValue(root["rules"])
		next := []string{}
		inserted := false
		for _, rule := range rules {
			parts := strings.Split(rule, ",")
			if len(parts) == 3 && parts[0] == "IN-NAME" && parts[1] == inbound {
				if !inserted {
					next = append(next, "IN-NAME,"+inbound+","+target)
					inserted = true
				}
				continue
			}
			if !inserted && strings.HasPrefix(rule, "MATCH,") {
				next = append(next, "IN-NAME,"+inbound+","+target)
				inserted = true
			}
			next = append(next, rule)
		}
		if !inserted {
			next = append(next, "IN-NAME,"+inbound+","+target)
		}
		encoded, err := yaml.Marshal(next)
		if err != nil {
			return "", err
		}
		return configschema.MergeFragment(engine, source, "rules", string(encoded), false)
	}
	routeKey, matchKey, targetKey := "route", "inbound", "outbound"
	if engine == core.EngineXray {
		routeKey, matchKey, targetKey = "routing", "inboundTag", "outboundTag"
	}
	route := mapValue(root[routeKey])
	if route == nil {
		route = map[string]any{}
	}
	rules, _ := route["rules"].([]any)
	updated := []any{}
	inserted := false
	binding := map[string]any{matchKey: []any{inbound}, targetKey: target}
	if engine == core.EngineXray {
		binding["type"] = "field"
		if len(fallbacks) > 0 {
			delete(binding, targetKey)
			binding["balancerTag"] = target
		}
	}
	for _, raw := range rules {
		rule := mapValue(raw)
		plain := true
		for key := range rule {
			if key != matchKey && key != targetKey && key != "balancerTag" && key != "type" && key != "action" {
				plain = false
			}
		}
		plain = plain && (engine != core.EngineSingBox || rule["action"] == nil || rule["action"] == "route") && (engine != core.EngineXray || rule["type"] == "field")
		tags := stringSliceValue(rule[matchKey])
		if text, ok := rule[matchKey].(string); ok {
			tags = []string{text}
		}
		if plain && len(tags) == 1 && tags[0] == inbound {
			if !inserted {
				updated = append(updated, binding)
				inserted = true
			}
			continue
		}
		if plain && len(tags) > 1 {
			for _, tag := range tags {
				if tag == inbound {
					return "", fmt.Errorf("入站使用多入站共享路由，请先在通用配置中调整绑定")
				}
			}
		}
		rejectAll := engine == core.EngineSingBox && rule["action"] == "reject"
		for key := range rule {
			if key != "action" && key != "method" && key != "no_drop" {
				rejectAll = false
			}
		}
		if !inserted && rule[matchKey] == nil && (plain && stringValue(rule[targetKey]) != "" || rejectAll) {
			if rule["action"] == "reject" {
				return "", fmt.Errorf("已有全局拦截兜底，请先在通用配置中明确调整路由")
			}
			for _, object := range objects {
				if object.Tag == stringValue(rule[targetKey]) && (object.Kind == "dns" || object.Kind == "block" || object.Kind == "blackhole") {
					return "", fmt.Errorf("已有全局拦截或 DNS 兜底，请先在通用配置中明确调整路由")
				}
			}
			updated = append(updated, binding)
			inserted = true
		}
		updated = append(updated, raw)
	}
	if !inserted {
		updated = append(updated, binding)
	}
	route["rules"] = updated
	encoded, err := json.Marshal(route)
	if err != nil {
		return "", err
	}
	return configschema.MergeFragment(engine, source, routeKey, string(encoded), false)
}

// The workspace exposes explicit user bindings from the editable source only.
// Generated accounting rules and conditional routes are never the fallback.
func ManagedOutboundBindings(engine core.Engine, source string) map[string]string {
	primary, _ := ManagedOutboundBindingTargets(engine, source)
	return primary
}

func ManagedOutboundBindingTargets(engine core.Engine, source string) (map[string]string, map[string][]string) {
	root, err := managedDocument(engine, source)
	if err != nil {
		return nil, nil
	}
	primary, backups := map[string]string{}, map[string][]string{}
	members := map[string][]string{}
	record := func(inbound, target string) {
		if target == "" || strings.HasPrefix(target, accountingPrefix) || primary[inbound] != "" {
			return
		}
		primary[inbound] = target
		if exits := members[target]; len(exits) > 1 {
			primary[inbound], backups[inbound] = exits[0], exits[1:]
		}
	}
	if engine == core.EngineMihomo {
		groups, _ := root["proxy-groups"].([]any)
		for _, raw := range groups {
			group := mapValue(raw)
			tag := stringValue(group["name"])
			if managedFallbackTagPattern.MatchString(tag) && group["type"] == "fallback" {
				members[tag] = stringSliceValue(group["proxies"])
			}
		}
		for _, rule := range stringSliceValue(root["rules"]) {
			parts := strings.Split(rule, ",")
			if len(parts) == 3 && parts[0] == "IN-NAME" {
				record(parts[1], parts[2])
			}
		}
		return primary, backups
	}
	routeKey, targetKey := "route", "outbound"
	if engine == core.EngineXray {
		routeKey, targetKey = "routing", "outboundTag"
		balancers, _ := mapValue(root[routeKey])["balancers"].([]any)
		for _, raw := range balancers {
			group := mapValue(raw)
			tag, backup := stringValue(group["tag"]), stringValue(group["fallbackTag"])
			if exits := stringSliceValue(group["selector"]); managedFallbackTagPattern.MatchString(tag) && len(exits) == 1 && backup != "" {
				members[tag] = []string{exits[0], backup}
			}
		}
	}
	rules, _ := mapValue(root[routeKey])["rules"].([]any)
	for _, raw := range rules {
		rule := mapValue(raw)
		if inbound := accountingBoundInbound(rule, engine == core.EngineXray); inbound != "" {
			target := stringValue(rule[targetKey])
			if group := stringValue(rule["balancerTag"]); engine == core.EngineXray && members[group] != nil {
				target = group
			}
			record(inbound, target)
		}
	}
	return primary, backups
}

const fallbackPrefix = "qch-fallback-"

var managedFallbackTagPattern = regexp.MustCompile("^" + fallbackPrefix + `[1-9][0-9]{0,4}-[0-9a-f]{12}$`)

func managedBindingFallbackTag(inbound string, port int) string {
	digest := sha256.Sum256([]byte(inbound))
	return fmt.Sprintf("%s%d-%x", fallbackPrefix, port, digest[:6])
}

func xrayObservatoryKey(root map[string]any) string {
	if root["observatory"] != nil {
		return "observatory"
	}
	return "burstObservatory"
}

func observeXrayFallbackPrimary(root map[string]any, tag string) error {
	observer := mapValue(root[xrayObservatoryKey(root)])
	if observer == nil {
		return fmt.Errorf("Xray 出站故障回退需要先配置 observatory 或 burstObservatory")
	}
	selectors, ok := observer["subjectSelector"].([]any)
	if !ok && observer["subjectSelector"] != nil {
		return fmt.Errorf("Xray observatory 的 subjectSelector 必须是列表")
	}
	if !slices.ContainsFunc(selectors, func(raw any) bool { return stringValue(raw) == tag }) {
		observer["subjectSelector"] = append(selectors, tag)
	}
	return nil
}
