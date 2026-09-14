package serverconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func prepareTaggedAccounting(engine core.Engine, root map[string]any, plan *AccountingPlan, forceMarks bool) error {
	inbounds, _ := root["inbounds"].([]any)
	outbounds, _ := root["outbounds"].([]any)
	if len(outbounds) == 0 {
		return fmt.Errorf("no explicit default outbound")
	}
	xray := engine == core.EngineXray
	if xray {
		address, err := xrayAccountingAPIAddress(root)
		if err != nil {
			return err
		}
		plan.API = address
	}
	apiTag := "qch-stat-api"
	if xray && stringValue(mapValue(root["api"])["tag"]) != "" {
		apiTag = stringValue(mapValue(root["api"])["tag"])
	}
	routeKey, targetKey, inKey, kindKey, portKey := "route", "outbound", "inbound", "type", "listen_port"
	if xray {
		routeKey, targetKey, inKey, kindKey, portKey = "routing", "outboundTag", "inboundTag", "protocol", "port"
	}
	route := mapValue(root[routeKey])
	if route == nil {
		route = map[string]any{}
	}
	if route["balancers"] != nil || root["endpoints"] != nil {
		return fmt.Errorf("balancer/endpoint routing requires explicit accounting mapping")
	}
	if root["reverse"] != nil {
		return fmt.Errorf("reverse routing requires explicit independent exit mapping")
	}
	// Loopback is reachable through direct proxy outbounds. Deny these
	// destination ports before any user route, including domain-based routes,
	// so DNS aliases and IP encodings cannot expose the reset-capable API.
	guard := map[string]any{"port": []int{10085, 10086}, "action": "reject"}
	if xray {
		guard = map[string]any{"type": "field", "port": "10085,10086", "outboundTag": "qch-stat-block"}
		if api := mapValue(root["api"]); stringValue(api["listen"]) != "" {
			_, port, _ := net.SplitHostPort(plan.API)
			if port != "10085" && port != "10086" {
				guard["port"] = fmt.Sprintf("%s,%s", guard["port"], port)
			}
		}
		for _, raw := range inbounds {
			in := mapValue(raw)
			if xrayInternalAPIInbound(root, in) {
				listen := stringValue(in["listen"])
				if listen != "127.0.0.1" && listen != "::1" {
					return fmt.Errorf("existing Xray API inbound must listen on loopback before enabling statistics")
				}
				port := trafficPortNumber(in["port"])
				if port != 0 && port != 10085 && port != 10086 {
					guard["port"] = fmt.Sprintf("%s,%d", guard["port"], port)
				}
			}
		}
		found := false
		for _, raw := range outbounds {
			out := mapValue(raw)
			if stringValue(out["tag"]) == "qch-stat-block" {
				if !accountingGeneratedSubset([]any{out}, []any{map[string]any{"tag": "qch-stat-block", "protocol": "blackhole"}}) {
					return fmt.Errorf("reserved statistics protection outbound conflicts with custom configuration")
				}
				found = true
			}
		}
		if !found {
			outbounds = append(outbounds, map[string]any{"tag": "qch-stat-block", "protocol": "blackhole"})
		}
	}
	var originals []any
	var priorClones []any
	byTag := map[string]map[string]any{}
	// Untagged outbounds are legal defaults. Give them stable, collision-free
	// identities without changing their order or any existing route targets.
	usedTags := map[string]bool{apiTag: true}
	usedTags[stringValue(route["final"])] = true
	routeRules, _ := route["rules"].([]any)
	for _, raw := range routeRules {
		usedTags[stringValue(mapValue(raw)[targetKey])] = true
	}
	for _, raw := range outbounds {
		if out := mapValue(raw); out != nil {
			usedTags[stringValue(out["tag"])] = true
		}
	}
	for index, raw := range outbounds {
		out := mapValue(raw)
		if out == nil {
			return fmt.Errorf("outbounds[%d] must be an object", index)
		}
		tag := stringValue(out["tag"])
		if tag == "" {
			if value, exists := out["tag"]; exists && value != "" {
				return fmt.Errorf("outbounds[%d].tag must be a string", index)
			}
			for suffix := index + 1; ; suffix++ {
				tag = fmt.Sprintf("qch-outbound-%d", suffix)
				if !usedTags[tag] {
					break
				}
			}
			out["tag"] = tag
			usedTags[tag] = true
		}
		if strings.HasPrefix(tag, accountingPrefix) {
			priorClones = append(priorClones, raw)
			continue
		}
		if byTag[tag] != nil {
			return fmt.Errorf("outbounds[%d].tag duplicates an earlier outbound; assign distinct tags and update the intended route targets", index)
		}
		if out["detour"] != nil || out["proxySettings"] != nil || !accountingMultiplexDisabled(out["mux"]) || !accountingMultiplexDisabled(out["multiplex"]) || out[kindKey] == "selector" || out[kindKey] == "urltest" || mapValue(mapValue(out["streamSettings"])["sockopt"])["dialerProxy"] != nil {
			return fmt.Errorf("chained, balanced or multiplexed outbound %s requires explicit accounting mapping", tag)
		}
		byTag[tag] = out
		originals = append(originals, raw)
	}
	if len(originals) == 0 || len(originals) > 64 {
		return fmt.Errorf("no explicit default outbound")
	}
	if xray && (!tagPattern.MatchString(apiTag) || byTag[apiTag] != nil || strings.HasPrefix(apiTag, accountingPrefix)) {
		return fmt.Errorf("Xray API tag conflicts with proxy outbound tags")
	}
	defaultTag := stringValue(mapValue(originals[0])["tag"])
	if !xray && stringValue(route["final"]) != "" {
		defaultTag = stringValue(route["final"])
	}
	if byTag[defaultTag] == nil {
		return fmt.Errorf("unknown default outbound")
	}
	// Xray protocol writers can bypass the core's outbound byte wrapper (the
	// native VLESS regression reproduces an uplink of zero). Use socket marks
	// for the whole configuration when protocol exits are present, never mix
	// listener bytes with an incomplete API counter or estimate missing bytes.
	// An explicit inbound-to-outbound binding always uses per-port marks,
	// including direct exits and sing-box builds with a native statistics API.
	// Generated fallback rules are not user bindings and must not change the
	// accounting source merely because an existing plan is compiled again.
	marked := forceMarks
	for _, raw := range routeRules {
		rule := mapValue(raw)
		target := byTag[stringValue(rule[targetKey])]
		if target == nil || target[kindKey] == "block" || target[kindKey] == "blackhole" || target[kindKey] == "dns" {
			continue
		}
		if tag := accountingBoundInbound(rule, xray); tag != "" {
			for _, rawIn := range inbounds {
				in := mapValue(rawIn)
				if in["tag"] == tag && trafficPortNumber(in[portKey]) != 0 && !(xray && xrayInternalAPIInbound(root, in)) {
					marked = true
				}
			}
		}
	}
	if xray {
		for _, raw := range originals {
			switch stringValue(mapValue(raw)[kindKey]) {
			case "freedom", "blackhole", "dns":
			case "vless", "vmess", "trojan", "shadowsocks", "socks", "http":
				marked = true
			default:
				return fmt.Errorf("outbound protocol requires explicit accounting mapping")
			}
		}
		if marked {
			for _, raw := range originals {
				mark := mapValue(mapValue(mapValue(raw)["streamSettings"])["sockopt"])["mark"]
				if mark != nil && mark != json.Number("0") {
					return fmt.Errorf("custom outbound socket mark requires manual review")
				}
			}
		}
	} else if marked {
		for _, raw := range originals {
			if mapValue(raw)["routing_mark"] != nil {
				return fmt.Errorf("custom routing_mark requires manual review")
			}
		}
	}
	if marked {
		plan.Source = "nft-dual"
	}
	var originalRules []any
	var priorRules []any
	rules, _ := route["rules"].([]any)
	for _, raw := range rules {
		if accountingGeneratedSubset([]any{raw}, []any{guard}) {
			continue
		}
		rule := mapValue(raw)
		if rule == nil {
			return fmt.Errorf("invalid routing rule")
		}
		if strings.HasPrefix(stringValue(rule[targetKey]), accountingPrefix) {
			priorRules = append(priorRules, raw)
			continue
		}
		if rule["balancerTag"] != nil || rule["rules"] != nil || rule["invert"] == true {
			return fmt.Errorf("logical/balancer rules require explicit accounting mapping")
		}
		if !xray {
			switch stringValue(rule["action"]) {
			case "", "route":
				if stringValue(rule[targetKey]) == "" {
					return fmt.Errorf("routing rules require an explicit outbound")
				}
			case "reject", "hijack-dns", "sniff", "resolve", "route-options":
				if rule[targetKey] != nil {
					return fmt.Errorf("non-routing actions cannot select an outbound")
				}
			default:
				return fmt.Errorf("routing action requires explicit independent exit mapping")
			}
		}
		originalRules = append(originalRules, raw)
	}
	var clones, legacyClones []any
	seenPorts, seenTags := map[int]bool{}, map[string]bool{}
	for _, raw := range inbounds {
		in := mapValue(raw)
		tag := stringValue(in["tag"])
		port := trafficPortNumber(in[portKey])
		if xray && xrayInternalAPIInbound(root, in) {
			continue
		}
		if !tagPattern.MatchString(tag) || port == 0 || seenPorts[port] || seenTags[tag] {
			return fmt.Errorf("accounting requires unique tagged single-port inbounds")
		}
		if settings := mapValue(in["settings"]); settings["fallbacks"] != nil {
			return fmt.Errorf("inbound fallbacks require explicit independent exit mapping")
		}
		for _, key := range []string{"allocate", "detour", "listen_ports"} {
			if in[key] != nil {
				return fmt.Errorf("dynamic or redirected listeners require explicit independent exit mapping")
			}
		}
		seenPorts[port], seenTags[tag] = true, true
		entry := AccountingPort{Port: port, Inbound: tag}
		if marked {
			entry.Mark = uint32(0x51430000) | uint32(port)
		}
		for _, rawOut := range originals {
			out := mapValue(rawOut)
			original := stringValue(out["tag"])
			if out[kindKey] == "block" || out[kindKey] == "blackhole" || out[kindKey] == "dns" {
				continue
			}
			clone := cloneAccountingObject(out)
			clone["tag"] = accountingTag(port, original)
			if marked {
				legacyClones = append(legacyClones, cloneAccountingObject(clone))
			}
			if marked && xray {
				stream := mapValue(clone["streamSettings"])
				if stream == nil {
					stream = map[string]any{}
				}
				sockopt := mapValue(stream["sockopt"])
				if sockopt == nil {
					sockopt = map[string]any{}
				}
				sockopt["mark"] = entry.Mark
				stream["sockopt"], clone["streamSettings"] = sockopt, stream
			} else if marked {
				clone["routing_mark"] = entry.Mark
			}
			clones = append(clones, clone)
			entry.Outbounds = append(entry.Outbounds, stringValue(clone["tag"]))
		}
		plan.Ports = append(plan.Ports, entry)
	}
	compiled := []any{guard}
	var apiRules []any
	for _, raw := range originalRules {
		rule := mapValue(raw)
		target := stringValue(rule[targetKey])
		if target != "" && byTag[target] == nil && !(xray && target == apiTag) {
			return fmt.Errorf("unknown route target %s", target)
		}
		if xray && target == apiTag {
			// Only dedicated loopback API routes may precede the API port guard.
			tags, ok := rule[inKey].([]any)
			if !ok || len(tags) == 0 {
				return fmt.Errorf("Xray API route requires explicit internal inbounds")
			}
			for _, tag := range tags {
				valid := false
				for _, rawIn := range inbounds {
					in := mapValue(rawIn)
					if in["tag"] == tag && xrayInternalAPIInbound(root, in) {
						valid = true
					}
				}
				if !valid {
					return fmt.Errorf("Xray API route includes a non-internal inbound")
				}
			}
			apiRules = append(apiRules, raw)
			continue
		}
		for _, entry := range plan.Ports {
			if !accountingRuleMatches(rule[inKey], entry.Inbound) {
				continue
			}
			out := byTag[target]
			if out == nil || out[kindKey] == "block" || out[kindKey] == "blackhole" || out[kindKey] == "dns" {
				continue
			}
			clone := cloneAccountingObject(rule)
			clone[inKey] = []string{entry.Inbound}
			clone[targetKey] = accountingTag(entry.Port, target)
			compiled = append(compiled, clone)
		}
		compiled = append(compiled, raw)
	}
	for _, entry := range plan.Ports {
		out := byTag[defaultTag]
		if out[kindKey] == "block" || out[kindKey] == "blackhole" || out[kindKey] == "dns" {
			continue
		}
		rule := map[string]any{inKey: []string{entry.Inbound}, targetKey: accountingTag(entry.Port, defaultTag)}
		if xray {
			rule["type"] = "field"
		} else {
			rule["action"] = "route"
		}
		compiled = append(compiled, rule)
	}
	compiled = append(apiRules, compiled...)
	route["rules"] = compiled
	if !accountingGeneratedSubset(priorClones, append(legacyClones, clones...)) || !accountingGeneratedSubset(priorRules, compiled) {
		return fmt.Errorf("reserved accounting tags conflict with custom or edited routing; review configuration before migration")
	}
	root[routeKey] = route
	root["outbounds"] = append(originals, clones...)
	if xray {
		api := mapValue(root["api"])
		if api == nil {
			api = map[string]any{"tag": apiTag, "listen": plan.API}
		}
		services, _ := api["services"].([]any)
		foundStats := false
		for _, service := range services {
			if service == "StatsService" {
				foundStats = true
			}
		}
		if !foundStats {
			services = append(services, "StatsService")
		}
		api["services"] = services
		root["api"] = api
		root["stats"] = map[string]any{}
		policy := mapValue(root["policy"])
		if policy == nil {
			policy = map[string]any{}
		}
		system := mapValue(policy["system"])
		if system == nil {
			system = map[string]any{}
		}
		for _, key := range []string{"statsInboundUplink", "statsInboundDownlink", "statsOutboundUplink", "statsOutboundDownlink"} {
			system[key] = true
		}
		policy["system"] = system
		root["policy"] = policy
	} else {
		plan.API = "127.0.0.1:10086"
		experimental := mapValue(root["experimental"])
		if experimental == nil {
			experimental = map[string]any{}
		}
		if api := mapValue(experimental["v2ray_api"]); api != nil && stringValue(api["listen"]) != plan.API {
			return fmt.Errorf("existing sing-box stats API requires manual integration")
		}
		var ins, outs []string
		for _, entry := range plan.Ports {
			ins = append(ins, entry.Inbound)
			outs = append(outs, entry.Outbounds...)
		}
		experimental["v2ray_api"] = map[string]any{"listen": plan.API, "stats": map[string]any{"enabled": true, "inbounds": ins, "outbounds": outs}}
		root["experimental"] = experimental
		if marked {
			delete(experimental, "v2ray_api")
			if len(experimental) == 0 {
				delete(root, "experimental")
			}
			plan.API = ""
		}
	}
	for _, entry := range plan.Ports {
		if entry.Port == 10085 || entry.Port == 10086 {
			return fmt.Errorf("listener conflicts with local accounting API")
		}
	}
	return nil
}

// Only a plain, single-inbound fallback is an editable outbound binding.
// Conditional policy routes and internal/generated exits keep their meaning.
func accountingBoundInbound(rule map[string]any, xray bool) string {
	inKey, targetKey, actionKey := "inbound", "outbound", "action"
	if xray {
		inKey, targetKey, actionKey = "inboundTag", "outboundTag", "type"
		if rule[actionKey] != "field" {
			return ""
		}
	} else if action := stringValue(rule[actionKey]); action != "" && action != "route" {
		return ""
	}
	for key := range rule {
		if key != inKey && key != targetKey && key != actionKey {
			return ""
		}
	}
	if tag, ok := rule[inKey].(string); ok && !xray {
		return tag
	}
	if tags, ok := rule[inKey].([]any); ok && len(tags) == 1 {
		return stringValue(tags[0])
	}
	return ""
}

func accountingMultiplexDisabled(value any) bool {
	// An explicitly disabled option is common in imported protocol outbounds.
	// Preserve it verbatim, but do not infer disabled from an unknown/default mode.
	return value == nil || mapValue(value)["enabled"] == false
}

func accountingGeneratedSubset(previous, current []any) bool {
	allowed := map[string]bool{}
	for _, value := range current {
		data, _ := json.Marshal(value)
		allowed[string(data)] = true
	}
	for _, value := range previous {
		data, _ := json.Marshal(value)
		if !allowed[string(data)] {
			return false
		}
	}
	return true
}

func accountingRuleMatches(raw any, tag string) bool {
	if raw == nil {
		return true
	}
	if value, ok := raw.(string); ok {
		return value == tag
	}
	if values, ok := raw.([]any); ok {
		for _, v := range values {
			if v == tag {
				return true
			}
		}
		return len(values) == 0
	}
	return false
}

func cloneAccountingObject(value map[string]any) map[string]any {
	data, _ := json.Marshal(value)
	var clone map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	_ = decoder.Decode(&clone)
	return clone
}
