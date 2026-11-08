package serverconfig

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v4"
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
	if root["proxy-providers"] != nil || root["sub-rules"] != nil {
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
	groups := map[string]map[string]any{}
	var originalGroups, priorGroups []any
	if values, exists := root["proxy-groups"]; exists && values != nil {
		items, ok := values.([]any)
		if !ok {
			return plan, fmt.Errorf("Mihomo proxy-groups must be a list")
		}
		for _, raw := range items {
			group := mapValue(raw)
			name := stringValue(group["name"])
			if strings.HasPrefix(name, accountingPrefix) {
				priorGroups = append(priorGroups, raw)
				continue
			}
			if !tagPattern.MatchString(name) || byName[name] != nil || groups[name] != nil {
				return plan, fmt.Errorf("Mihomo group names must be unique and explicit")
			}
			kind := stringValue(group["type"])
			if kind != "select" && kind != "url-test" && kind != "fallback" && kind != "load-balance" {
				return plan, fmt.Errorf("Mihomo group %s has unsupported selection semantics", name)
			}
			if group["use"] != nil || group["include-all"] == true || group["include-all-proxies"] == true || group["include-all-providers"] == true || len(stringSliceValue(group["proxies"])) == 0 {
				return plan, fmt.Errorf("Mihomo group %s requires explicit proxy members", name)
			}
			groups[name] = group
			originalGroups = append(originalGroups, raw)
		}
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
	var clones, groupClones []any
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
		visiting, cloned := map[string]bool{}, map[string]bool{}
		var cloneTarget func(string) (string, error)
		cloneTarget = func(target string) (string, error) {
			if strings.HasPrefix(target, "REJECT") {
				return target, nil
			}
			cloneName := accountingTag(port, target)
			if cloned[target] {
				return cloneName, nil
			}
			if visiting[target] {
				return "", fmt.Errorf("Mihomo group dependency cycle at %s", target)
			}
			visiting[target] = true
			p := byName[target]
			group := groups[target]
			if p == nil {
				p = group
			}
			if p == nil {
				return "", fmt.Errorf("unknown Mihomo outbound %s", target)
			}
			data, err := yaml.Marshal(p)
			if err != nil {
				return "", err
			}
			var clone map[string]any
			if err = yaml.Unmarshal(data, &clone); err != nil {
				return "", err
			}
			clone["name"] = cloneName
			if group != nil {
				members := []string{}
				for _, member := range stringSliceValue(group["proxies"]) {
					name, err := cloneTarget(member)
					if err != nil {
						return "", err
					}
					members = append(members, name)
				}
				clone["proxies"] = members
				groupClones = append(groupClones, clone)
			} else {
				clone["routing-mark"] = mark
				clones = append(clones, clone)
				entry.Outbounds = append(entry.Outbounds, cloneName)
			}
			delete(visiting, target)
			cloned[target] = true
			return cloneName, nil
		}
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
			if _, err := cloneTarget(target); err != nil {
				return plan, err
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
		if byName[parts[index]] != nil || groups[parts[index]] != nil {
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
	if root["proxy-groups"] != nil {
		root["proxy-groups"] = append(originalGroups, groupClones...)
	}
	compiledRules := make([]any, len(rules))
	for i, rule := range rules {
		compiledRules[i] = rule
	}
	if !accountingGeneratedSubset(priorGroups, groupClones) || !accountingGeneratedSubset(priorProxies, clones) || !accountingGeneratedSubset(priorRules, compiledRules) {
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
