package serverconfig

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
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
	seen := map[string]bool{outbound: true}
	for _, tag := range fallbacks {
		if seen[tag] || available[tag] != 1 {
			return "", fmt.Errorf("备用出口无效或重复，请重新选择")
		}
		seen[tag] = true
	}
	retired := map[string]bool{}
	target := outbound
	if len(fallbacks) > 0 {
		if engine != core.EngineMihomo && engine != core.EngineXray {
			return "", fmt.Errorf("此内核没有原生故障回退出站机制")
		}
		members := append([]string{outbound}, fallbacks...)
		var exists bool
		target, exists = selectFallbackGroup(root, engine, inbound, port, members)
		if engine == core.EngineMihomo && !exists {
			group := map[string]any{"name": target, "type": "fallback", "proxies": members}
			fragment, err := yaml.Marshal(map[string]any{"proxy-groups": []any{group}})
			if err != nil {
				return "", err
			}
			source, err = configschema.MutateListItem(engine, source, string(fragment), "proxy-groups", "name", target, "add")
			if err != nil {
				return "", err
			}
		}
		if engine == core.EngineXray {
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
			if !exists {
				routing := mapValue(root["routing"])
				if routing == nil {
					routing = map[string]any{}
					root["routing"] = routing
				}
				balancers, _ := routing["balancers"].([]any)
				routing["balancers"] = append(balancers, map[string]any{"tag": target, "selector": []string{outbound}, "fallbackTag": fallbacks[0]})
			}
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
				if parts[2] != target && managedFallbackTagPattern.MatchString(parts[2]) {
					retired[parts[2]] = true
				}
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
		source, err = configschema.MergeFragment(engine, source, "rules", string(encoded), false)
		if err != nil {
			return "", err
		}
		return pruneFallbackGroups(engine, source, retired)
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
			if old := stringValue(rule["balancerTag"]); old != target && managedFallbackTagPattern.MatchString(old) {
				retired[old] = true
			}
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
	source, err = configschema.MergeFragment(engine, source, routeKey, string(encoded), false)
	if err != nil {
		return "", err
	}
	return pruneFallbackGroups(engine, source, retired)
}

func selectFallbackGroup(root map[string]any, engine core.Engine, inbound string, port int, members []string) (string, bool) {
	// A renamed listener can still use the group originally created for
	// this name and port. Never replace another route's group in place.
	previous := inboundRouteTargets(engine, root)[inbound]
	groups := fallbackGroupMembers(engine, root)
	if managedFallbackTagPattern.MatchString(previous) && slices.Equal(groups[previous], members) {
		return previous, true
	}
	target := managedBindingFallbackTag(inbound, port)
	for i := 1; ; i++ {
		existing, found := groups[target]
		if !found || slices.Equal(existing, members) {
			return target, found
		}
		target = managedBindingFallbackTag(inbound+"\x00"+strconv.Itoa(i), port)
	}
}

// Drop only groups whose binding was removed and whose final consumer is gone.
func pruneFallbackGroups(engine core.Engine, source string, retired map[string]bool) (string, error) {
	if len(retired) == 0 {
		return source, nil
	}
	root, err := managedDocument(engine, source)
	if err != nil {
		return "", err
	}
	if engine == core.EngineXray {
		routing := mapValue(root["routing"])
		rules, _ := routing["rules"].([]any)
		for _, raw := range rules {
			delete(retired, stringValue(mapValue(raw)["balancerTag"]))
		}
		groups, _ := routing["balancers"].([]any)
		kept := slices.DeleteFunc(groups, func(raw any) bool { return retired[stringValue(mapValue(raw)["tag"])] })
		if len(kept) == len(groups) {
			return source, nil
		}
		routing["balancers"] = kept
		fragment, err := json.Marshal(routing)
		if err != nil {
			return "", err
		}
		return configschema.MergeFragment(engine, source, "routing", string(fragment), false)
	}
	groups, _ := root["proxy-groups"].([]any)
	for _, raw := range groups {
		tag := stringValue(mapValue(raw)["name"])
		if !retired[tag] || managedReferences(root, tag) {
			continue
		}
		fragment, err := yaml.Marshal(map[string]any{"proxy-groups": []any{map[string]any{"name": tag}}})
		if err != nil {
			return "", err
		}
		source, err = configschema.MutateListItem(engine, source, string(fragment), "proxy-groups", "name", tag, "delete")
		if err != nil {
			return "", err
		}
	}
	return source, nil
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
	primary := inboundRouteTargets(engine, root)
	backups := map[string][]string{}
	groups := fallbackGroupMembers(engine, root)
	for inbound, target := range primary {
		if exits := groups[target]; managedFallbackTagPattern.MatchString(target) && len(exits) > 1 {
			primary[inbound], backups[inbound] = exits[0], exits[1:]
		}
	}
	return primary, backups
}

func fallbackGroupMembers(engine core.Engine, root map[string]any) map[string][]string {
	groups := map[string][]string{}
	switch engine {
	case core.EngineMihomo:
		items, _ := root["proxy-groups"].([]any)
		for _, raw := range items {
			group := mapValue(raw)
			name := stringValue(group["name"])
			groups[name] = nil
			if group["type"] == "fallback" {
				groups[name] = stringSliceValue(group["proxies"])
			}
		}
	case core.EngineXray:
		items, _ := mapValue(root["routing"])["balancers"].([]any)
		for _, raw := range items {
			group := mapValue(raw)
			groups[stringValue(group["tag"])] = append(stringSliceValue(group["selector"]), stringValue(group["fallbackTag"]))
		}
	}
	return groups
}

func inboundRouteTargets(engine core.Engine, root map[string]any) map[string]string {
	result := map[string]string{}
	record := func(inbound, target string) {
		if target != "" && !strings.HasPrefix(target, accountingPrefix) && result[inbound] == "" {
			result[inbound] = target
		}
	}
	if engine == core.EngineMihomo {
		for _, rule := range stringSliceValue(root["rules"]) {
			parts := strings.Split(rule, ",")
			if len(parts) == 3 && parts[0] == "IN-NAME" {
				record(parts[1], parts[2])
			}
		}
		return result
	}
	xray := engine == core.EngineXray
	routeKey, targetKey := "route", "outbound"
	if xray {
		routeKey, targetKey = "routing", "outboundTag"
	}
	rules, _ := mapValue(root[routeKey])["rules"].([]any)
	for _, raw := range rules {
		rule := mapValue(raw)
		if inbound := accountingBoundInbound(rule, xray); inbound != "" {
			target := stringValue(rule[targetKey])
			if group := stringValue(rule["balancerTag"]); xray && managedFallbackTagPattern.MatchString(group) {
				target = group
			}
			record(inbound, target)
		}
	}
	return result
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
