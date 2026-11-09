package serverconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// Binding is part of the same candidate revision as an added exit. Conditional
// routes stay ahead of the per-inbound fallback, and other listeners stay intact.
func BindManagedOutbound(engine core.Engine, source, inbound string, port int, outbound string, state ManagedState) (string, error) {
	objects, err := ManagedObjects(engine, source, state)
	if err != nil {
		return "", err
	}
	listeners, exits := 0, 0
	for _, object := range objects {
		if object.Inbound && object.Tag == inbound && object.Port == port {
			listeners++
		}
		if !object.Inbound && object.Tag == outbound && object.Enabled {
			if object.Kind == "block" || object.Kind == "blackhole" || object.Kind == "dns" {
				return "", fmt.Errorf("请选择有效流量出站")
			}
			exits++
		}
	}
	if listeners != 1 || exits != 1 {
		return "", fmt.Errorf("入站或出站已变化，请重新选择")
	}
	root, err := managedDocument(engine, source)
	if err != nil {
		return "", err
	}
	if engine == core.EngineMihomo {
		rules := stringSliceValue(root["rules"])
		next := []string{}
		inserted := false
		for _, rule := range rules {
			parts := strings.Split(rule, ",")
			if len(parts) == 3 && parts[0] == "IN-NAME" && parts[1] == inbound {
				if !inserted {
					next = append(next, "IN-NAME,"+inbound+","+outbound)
					inserted = true
				}
				continue
			}
			if !inserted && strings.HasPrefix(rule, "MATCH,") {
				next = append(next, "IN-NAME,"+inbound+","+outbound)
				inserted = true
			}
			next = append(next, rule)
		}
		if !inserted {
			next = append(next, "IN-NAME,"+inbound+","+outbound)
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
	binding := map[string]any{matchKey: []any{inbound}, targetKey: outbound}
	if engine == core.EngineXray {
		binding["type"] = "field"
	}
	for _, raw := range rules {
		rule := mapValue(raw)
		plain := true
		for key := range rule {
			if key != matchKey && key != targetKey && key != "type" && key != "action" {
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
	root, err := managedDocument(engine, source)
	if err != nil {
		return nil
	}
	result := map[string]string{}
	if engine == core.EngineMihomo {
		for _, rule := range stringSliceValue(root["rules"]) {
			parts := strings.Split(rule, ",")
			if len(parts) == 3 && parts[0] == "IN-NAME" && !strings.HasPrefix(parts[2], accountingPrefix) && result[parts[1]] == "" {
				result[parts[1]] = parts[2]
			}
		}
		return result
	}
	routeKey, matchKey, targetKey := "route", "inbound", "outbound"
	if engine == core.EngineXray {
		routeKey, matchKey, targetKey = "routing", "inboundTag", "outboundTag"
	}
	rules, _ := mapValue(root[routeKey])["rules"].([]any)
	for _, raw := range rules {
		rule := mapValue(raw)
		plain := true
		for key := range rule {
			if key != matchKey && key != targetKey && key != "type" && key != "action" {
				plain = false
			}
		}
		tags := stringSliceValue(rule[matchKey])
		if tag, ok := rule[matchKey].(string); ok {
			tags = []string{tag}
		}
		target := stringValue(rule[targetKey])
		if plain && len(tags) == 1 && target != "" && !strings.HasPrefix(target, accountingPrefix) && result[tags[0]] == "" {
			result[tags[0]] = target
		}
	}
	return result
}
