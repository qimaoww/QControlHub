package serverconfig

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func prepareMihomoAccounting(root map[string]any) (AccountingPlan, error) {
	plan := AccountingPlan{Source: "nft-dual"}
	if tun := mapValue(root["tun"]); tun != nil && tun["enable"] != false {
		return plan, fmt.Errorf("TUN and tunnel listeners require explicit independent exit mapping")
	}
	if tunnels, ok := root["tunnels"].([]any); root["tunnels"] != nil && (!ok || len(tunnels) != 0) {
		return plan, fmt.Errorf("TUN and tunnel listeners require explicit independent exit mapping")
	}
	if mode := stringValue(root["mode"]); mode != "" && !strings.EqualFold(mode, "rule") {
		return plan, fmt.Errorf("Mihomo independent exits require rule mode")
	}
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
	// Mihomo otherwise falls through to the process-wide DIRECT adapter,
	// bypassing every per-listener clone when no user rule matches.
	terminal := false
	for _, rule := range originalRules {
		if strings.HasPrefix(rule, "MATCH,") {
			terminal = true
		}
	}
	if !terminal {
		originalRules = append(originalRules, "MATCH,DIRECT")
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
