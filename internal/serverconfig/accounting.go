package serverconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

const accountingPrefix = "qch-trf-"

type AccountingPort struct {
	Port      int      `json:"port"`
	Inbound   string   `json:"inbound"`
	Outbounds []string `json:"outbounds,omitempty"`
	Mark      uint32   `json:"mark,omitempty"`
}
type AccountingPlan struct {
	Content string           `json:"-"`
	Source  string           `json:"source"`
	API     string           `json:"api,omitempty"`
	Ports   []AccountingPort `json:"ports"`
}

func accountingTag(port int, original string) string {
	sum := sha256.Sum256([]byte(original))
	return fmt.Sprintf("%s%d-%s", accountingPrefix, port, hex.EncodeToString(sum[:6]))
}

// PrepareAccounting retains original routes and inserts equivalent,
// inbound-constrained routes to independent outbound instances. Unknown
// routing shapes fail closed, rather than silently bypassing a custom route.
func PrepareAccounting(engine core.Engine, content string) (AccountingPlan, error) {
	if err := core.ValidateConfig(engine, content); err != nil {
		return AccountingPlan{}, err
	}
	root := decodeTrafficConfiguration(engine, content)
	if engine != core.EngineMihomo {
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&root); err != nil {
			return AccountingPlan{}, err
		}
	}
	if root == nil {
		return AccountingPlan{}, fmt.Errorf("invalid core configuration")
	}
	plan := AccountingPlan{Source: "core-api"}
	if engine == core.EngineShadowsocksRust {
		plan.Source = "nft-dual"
		if root["plugin"] != nil {
			return plan, fmt.Errorf("SS Rust plugin transport requires explicit accounting review")
		}
		entries, _ := root["servers"].([]any)
		if entries == nil {
			entries = []any{root}
		}
		seen := map[int]bool{}
		for _, raw := range entries {
			entry, _ := raw.(map[string]any)
			if entry["plugin"] != nil {
				return plan, fmt.Errorf("SS Rust plugin transport requires explicit accounting review")
			}
			port := trafficPortNumber(entry["server_port"])
			if port == 0 || seen[port] {
				return plan, fmt.Errorf("SS Rust accounting requires explicit server_port")
			}
			seen[port] = true
			mark := uint32(0x51430000) | uint32(port)
			if existing := entry["outbound_fwmark"]; existing != nil && intValue(existing) != 0 && uint32(intValue(existing)) != mark {
				return plan, fmt.Errorf("port %d already has an outbound mark", port)
			}
			if intValue(root["outbound_fwmark"]) != 0 && entry != nil && len(entries) > 1 {
				return plan, fmt.Errorf("global outbound mark must be reviewed before per-port accounting")
			}
			entry["outbound_fwmark"] = mark
			plan.Ports = append(plan.Ports, AccountingPort{Port: port, Mark: mark})
		}
	} else if engine == core.EngineMihomo {
		return prepareMihomoAccounting(root)
	} else if engine == core.EngineXray || engine == core.EngineSingBox {
		if err := prepareTaggedAccounting(engine, root, &plan); err != nil {
			return plan, err
		}
	} else {
		return plan, fmt.Errorf("unsupported accounting engine")
	}
	if len(plan.Ports) == 0 {
		return plan, fmt.Errorf("no attributable listeners")
	}
	value, err := json.MarshalIndent(root, "", "  ")
	plan.Content = string(value) + "\n"
	return plan, err
}

func prepareTaggedAccounting(engine core.Engine, root map[string]any, plan *AccountingPlan) error {
	inbounds, _ := root["inbounds"].([]any)
	outbounds, _ := root["outbounds"].([]any)
	xray := engine == core.EngineXray
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
	// Loopback is reachable through direct proxy outbounds. Deny these
	// destination ports before any user route, including domain-based routes,
	// so DNS aliases and IP encodings cannot expose the reset-capable API.
	guard := map[string]any{"port": []int{10085, 10086}, "action": "reject"}
	if xray {
		guard = map[string]any{"type": "field", "port": "10085,10086", "outboundTag": "qch-stat-block"}
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
	for _, raw := range outbounds {
		out := mapValue(raw)
		tag := stringValue(out["tag"])
		if strings.HasPrefix(tag, accountingPrefix) {
			priorClones = append(priorClones, raw)
			continue
		}
		if tag == "" || byTag[tag] != nil {
			return fmt.Errorf("outbound tags must be present and unique")
		}
		if out["detour"] != nil || out["proxySettings"] != nil || out["mux"] != nil || out["multiplex"] != nil || out[kindKey] == "selector" || out[kindKey] == "urltest" || mapValue(mapValue(out["streamSettings"])["sockopt"])["dialerProxy"] != nil {
			return fmt.Errorf("chained, balanced or multiplexed outbound %s requires explicit accounting mapping", tag)
		}
		byTag[tag] = out
		originals = append(originals, raw)
	}
	if len(originals) == 0 || len(originals) > 64 {
		return fmt.Errorf("no explicit default outbound")
	}
	defaultTag := stringValue(mapValue(originals[0])["tag"])
	if !xray && stringValue(route["final"]) != "" {
		defaultTag = stringValue(route["final"])
	}
	if byTag[defaultTag] == nil {
		return fmt.Errorf("unknown default outbound")
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
		originalRules = append(originalRules, raw)
	}
	var clones []any
	seenPorts, seenTags := map[int]bool{}, map[string]bool{}
	for _, raw := range inbounds {
		in := mapValue(raw)
		tag := stringValue(in["tag"])
		port := trafficPortNumber(in[portKey])
		if tag == "qch-stat-api" {
			continue
		}
		if !tagPattern.MatchString(tag) || port == 0 || seenPorts[port] || seenTags[tag] {
			return fmt.Errorf("accounting requires unique tagged single-port inbounds")
		}
		seenPorts[port], seenTags[tag] = true, true
		entry := AccountingPort{Port: port, Inbound: tag}
		for _, rawOut := range originals {
			out := mapValue(rawOut)
			original := stringValue(out["tag"])
			if out[kindKey] == "block" || out[kindKey] == "blackhole" || out[kindKey] == "dns" {
				continue
			}
			clone := cloneAccountingObject(out)
			clone["tag"] = accountingTag(port, original)
			clones = append(clones, clone)
			entry.Outbounds = append(entry.Outbounds, stringValue(clone["tag"]))
		}
		plan.Ports = append(plan.Ports, entry)
	}
	compiled := []any{guard}
	for _, raw := range originalRules {
		rule := mapValue(raw)
		target := stringValue(rule[targetKey])
		if target != "" && byTag[target] == nil && target != "qch-stat-api" {
			return fmt.Errorf("unknown route target %s", target)
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
	route["rules"] = compiled
	if !accountingGeneratedSubset(priorClones, clones) || !accountingGeneratedSubset(priorRules, compiled) {
		return fmt.Errorf("reserved accounting tags conflict with custom or edited routing; review configuration before migration")
	}
	root[routeKey] = route
	root["outbounds"] = append(originals, clones...)
	if xray {
		plan.API = "127.0.0.1:10085"
		if api := mapValue(root["api"]); api != nil && stringValue(api["tag"]) != "qch-stat-api" {
			return fmt.Errorf("existing Xray API requires manual integration")
		}
		root["api"] = map[string]any{"tag": "qch-stat-api", "listen": plan.API, "services": []string{"StatsService"}}
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
	}
	for _, entry := range plan.Ports {
		if entry.Port == 10085 || entry.Port == 10086 {
			return fmt.Errorf("listener conflicts with local accounting API")
		}
	}
	return nil
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

func prepareMihomoAccounting(root map[string]any) (AccountingPlan, error) {
	plan := AccountingPlan{Source: "nft-dual"}
	if root["proxy-groups"] != nil || root["proxy-providers"] != nil || root["sub-rules"] != nil {
		return plan, fmt.Errorf("Mihomo shared groups/providers/sub-rules require explicit accounting mapping")
	}
	for _, key := range []string{"port", "socks-port", "mixed-port", "redir-port", "tproxy-port", "routing-mark"} {
		if intValue(root[key]) != 0 {
			return plan, fmt.Errorf("Mihomo %s requires migration to explicit listeners", key)
		}
	}
	listeners, _ := root["listeners"].([]any)
	proxies, _ := root["proxies"].([]any)
	byName := map[string]map[string]any{"DIRECT": {"name": "DIRECT", "type": "direct", "udp": true}}
	var originalProxies []any
	var priorProxies []any
	for _, raw := range proxies {
		p := mapValue(raw)
		name := stringValue(p["name"])
		if strings.HasPrefix(name, accountingPrefix) {
			priorProxies = append(priorProxies, raw)
			continue
		}
		if !tagPattern.MatchString(name) || byName[name] != nil {
			return plan, fmt.Errorf("Mihomo proxy names must be unique and explicit")
		}
		if p["dialer-proxy"] != nil || intValue(p["routing-mark"]) != 0 || p["smux"] != nil {
			return plan, fmt.Errorf("Mihomo proxy %s has shared transport or custom mark", name)
		}
		byName[name] = p
		originalProxies = append(originalProxies, raw)
	}
	var originalRules []string
	switch values := root["rules"].(type) {
	case []any:
		for _, v := range values {
			originalRules = append(originalRules, stringValue(v))
		}
	case []string:
		originalRules = values
	}
	if len(originalRules) == 0 {
		originalRules = []string{"MATCH,DIRECT"}
	}
	var rules []string
	var priorRules []any
	for _, rule := range originalRules {
		if strings.HasPrefix(accountingMihomoTarget(rule), accountingPrefix) {
			priorRules = append(priorRules, rule)
		}
	}
	var clones []any
	seenPorts, seenNames := map[int]bool{}, map[string]bool{}
	for _, raw := range listeners {
		in := mapValue(raw)
		name := stringValue(in["name"])
		port := trafficPortNumber(in["port"])
		if !tagPattern.MatchString(name) || port == 0 || seenPorts[port] || seenNames[name] || stringValue(in["rule"]) != "" || stringValue(in["proxy"]) != "" {
			return plan, fmt.Errorf("Mihomo requires tagged listeners using global rules")
		}
		seenPorts[port], seenNames[name] = true, true
		mark := uint32(0x51430000) | uint32(port)
		entry := AccountingPort{Port: port, Inbound: name, Mark: mark}
		for _, original := range originalRules {
			if strings.HasPrefix(accountingMihomoTarget(original), accountingPrefix) {
				continue
			}
			parts := strings.Split(original, ",")
			index := len(parts) - 1
			if index > 0 && parts[index] == "no-resolve" {
				index--
			}
			if index < 1 {
				return plan, fmt.Errorf("unsupported Mihomo routing rule")
			}
			target := parts[index]
			if strings.HasPrefix(target, "REJECT") {
				continue
			}
			p := byName[target]
			if p == nil {
				return plan, fmt.Errorf("unknown Mihomo outbound %s", target)
			}
			cloneName := accountingTag(port, target)
			if !containsAccountingString(entry.Outbounds, cloneName) {
				data, _ := yaml.Marshal(p)
				var clone map[string]any
				if err := yaml.Unmarshal(data, &clone); err != nil {
					return plan, err
				}
				clone["name"], clone["routing-mark"] = cloneName, mark
				clones = append(clones, clone)
				entry.Outbounds = append(entry.Outbounds, cloneName)
			}
		}
		plan.Ports = append(plan.Ports, entry)
	}
	for _, original := range originalRules {
		if strings.HasPrefix(accountingMihomoTarget(original), accountingPrefix) {
			continue
		}
		parts := strings.Split(original, ",")
		index := len(parts) - 1
		suffix := ""
		if index > 0 && parts[index] == "no-resolve" {
			index--
			suffix = ",no-resolve"
		}
		if index < 1 {
			return plan, fmt.Errorf("invalid Mihomo rule")
		}
		if byName[parts[index]] != nil {
			for _, entry := range plan.Ports {
				match := "IN-NAME," + entry.Inbound
				if parts[0] != "MATCH" {
					match = "AND,((" + match + "),(" + strings.Join(parts[:index], ",") + "))"
				}
				rules = append(rules, match+","+accountingTag(entry.Port, parts[index])+suffix)
			}
		}
		rules = append(rules, original)
	}
	root["rules"], root["proxies"] = rules, append(originalProxies, clones...)
	compiledRules := make([]any, len(rules))
	for i, rule := range rules {
		compiledRules[i] = rule
	}
	if !accountingGeneratedSubset(priorProxies, clones) || !accountingGeneratedSubset(priorRules, compiledRules) {
		return plan, fmt.Errorf("reserved accounting tags conflict with custom Mihomo routing")
	}
	if len(plan.Ports) == 0 {
		return plan, fmt.Errorf("no attributable listeners")
	}
	value, err := yaml.Marshal(root)
	plan.Content = string(value)
	return plan, err
}

func accountingMihomoTarget(rule string) string {
	parts := strings.Split(rule, ",")
	index := len(parts) - 1
	if index > 0 && parts[index] == "no-resolve" {
		index--
	}
	if index < 1 {
		return ""
	}
	return parts[index]
}
func containsAccountingString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
