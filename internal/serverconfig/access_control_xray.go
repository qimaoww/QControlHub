package serverconfig

import (
	"errors"
	"strings"
)

func mainlandXrayRouting(tag string, destination, source bool) map[string]any {
	rules := mainlandXrayRules(tag, destination, source, nil)
	if len(rules) == 0 {
		return nil
	}
	if destination {
		rules = append(rules, mainlandXrayStrategyMarker())
		return map[string]any{"domainStrategy": "IPIfNonMatch", "rules": rules}
	}
	return map[string]any{"rules": rules}
}

func mainlandXrayRules(tag string, destination, source bool, prefixes []string) []any {
	// Xray resolves geoip:cn from its installed geoip.dat resource. Keeping
	// the rule as a resource reference avoids embedding thousands of CIDRs in
	// every generated configuration and follows Xray's native geodata path.
	// prefixes is retained in the function signature for backwards-compatible
	// callers and tests; it is intentionally ignored for Xray.
	ipValues := []any{"geoip:cn"}
	rules := make([]any, 0, 2)
	if source {
		rules = append(rules, map[string]any{
			"type": "field", "ruleTag": mainlandXraySourcePrefix + tag,
			"inboundTag": []any{tag}, "source": ipValues, "outboundTag": mainlandXrayBlockTag,
		})
	}
	if destination {
		rules = append(rules, map[string]any{
			"type": "field", "ruleTag": mainlandXrayDestinationPrefix + tag,
			"inboundTag": []any{tag}, "ip": ipValues, "outboundTag": mainlandXrayBlockTag,
		})
	}
	return rules
}

func mainlandXrayFlags(root map[string]any, tag string) (bool, bool) {
	routing := mapValue(root["routing"])
	rules, _ := routing["rules"].([]any)
	var destination, source bool
	for _, value := range rules {
		rule, _ := value.(map[string]any)
		switch stringValue(rule["ruleTag"]) {
		case mainlandXrayDestinationPrefix + tag:
			destination = true
		case mainlandXraySourcePrefix + tag:
			source = true
		}
	}
	return destination, source
}

func applyMainlandXray(root map[string]any, tag string, destination, source bool, prefixes []string) error {
	routing := mapValue(root["routing"])
	if routing == nil {
		routing = map[string]any{}
	}
	current, _ := routing["rules"].([]any)
	hadManagedRules := false
	strategyOwned := false
	for _, value := range current {
		rule, _ := value.(map[string]any)
		ruleTag := stringValue(rule["ruleTag"])
		if ruleTag == mainlandXrayStrategyMarkerTag {
			if !mainlandXrayManagedStrategyMarker(rule) {
				return errors.New("配置中的大陆访问域名解析标记已被其他规则占用")
			}
			strategyOwned = true
			continue
		}
		var managed bool
		switch {
		case strings.HasPrefix(ruleTag, mainlandXrayDestinationPrefix):
			managed = mainlandXrayManagedRule(rule, strings.TrimPrefix(ruleTag, mainlandXrayDestinationPrefix), false)
		case strings.HasPrefix(ruleTag, mainlandXraySourcePrefix):
			managed = mainlandXrayManagedRule(rule, strings.TrimPrefix(ruleTag, mainlandXraySourcePrefix), true)
		default:
			if stringValue(rule["outboundTag"]) == mainlandXrayBlockTag {
				return errors.New("配置中的 qch-mainland-block 出站已被其他路由规则引用")
			}
			continue
		}
		if !managed {
			return errors.New("配置中的大陆访问限制规则标签已被其他规则占用")
		}
		hadManagedRules = true
	}
	rules := mainlandXrayRules(tag, destination, source, prefixes)
	for _, value := range current {
		rule, _ := value.(map[string]any)
		ruleTag := stringValue(rule["ruleTag"])
		if ruleTag == mainlandXrayStrategyMarkerTag {
			continue
		}
		if ruleTag == mainlandXrayDestinationPrefix+tag {
			if !mainlandXrayManagedRule(rule, tag, false) {
				return errors.New("配置中的大陆目标限制规则标签已被其他规则占用")
			}
			continue
		}
		if ruleTag == mainlandXraySourcePrefix+tag {
			if !mainlandXrayManagedRule(rule, tag, true) {
				return errors.New("配置中的大陆来源限制规则标签已被其他规则占用")
			}
			continue
		}
		rules = append(rules, value)
	}
	hasDestinationRules := false
	for _, value := range rules {
		rule, _ := value.(map[string]any)
		if strings.HasPrefix(stringValue(rule["ruleTag"]), mainlandXrayDestinationPrefix) {
			hasDestinationRules = true
			break
		}
	}
	strategy := strings.TrimSpace(stringValue(routing["domainStrategy"]))
	keepStrategyMarker := false
	if hasDestinationRules {
		if strategyOwned {
			// Keep ownership only while the value still equals what QControlHub
			// wrote. If an operator changed it, their value wins.
			keepStrategyMarker = strategy == "IPIfNonMatch"
		} else if strategy == "" {
			routing["domainStrategy"] = "IPIfNonMatch"
			keepStrategyMarker = true
		}
	} else if strategyOwned && strategy == "IPIfNonMatch" {
		delete(routing, "domainStrategy")
	}
	routing["rules"] = rules
	if keepStrategyMarker {
		routing["rules"] = append([]any{mainlandXrayStrategyMarker()}, rules...)
	}
	finalRules, _ := routing["rules"].([]any)
	if len(finalRules) == 0 {
		delete(routing, "rules")
	}
	if len(routing) == 0 {
		delete(root, "routing")
	} else {
		root["routing"] = routing
	}

	outbounds, _ := root["outbounds"].([]any)
	hasManagedRules := false
	for _, value := range rules {
		rule, _ := value.(map[string]any)
		ruleTag := stringValue(rule["ruleTag"])
		if strings.HasPrefix(ruleTag, mainlandXrayDestinationPrefix) || strings.HasPrefix(ruleTag, mainlandXraySourcePrefix) {
			hasManagedRules = true
			break
		}
	}
	filtered := make([]any, 0, len(outbounds)+1)
	foundManagedOutbound := false
	for _, value := range outbounds {
		outbound, _ := value.(map[string]any)
		if stringValue(outbound["tag"]) != mainlandXrayBlockTag {
			filtered = append(filtered, value)
			continue
		}
		if !hadManagedRules && !strategyOwned {
			if hasManagedRules {
				return errors.New("配置中的 qch-mainland-block 出站标签已被其他出站占用")
			}
			filtered = append(filtered, value)
			continue
		}
		if !mainlandXrayManagedOutbound(outbound) {
			return errors.New("配置中的 qch-mainland-block 出站标签已被其他协议占用")
		}
		foundManagedOutbound = true
		if hasManagedRules {
			filtered = append(filtered, value)
		}
	}
	if hasManagedRules && !foundManagedOutbound {
		filtered = append(filtered, map[string]any{"protocol": "blackhole", "tag": mainlandXrayBlockTag})
	}
	root["outbounds"] = filtered
	return nil
}

func mainlandXrayManagedOutbound(outbound map[string]any) bool {
	return len(outbound) == 2 && stringValue(outbound["protocol"]) == "blackhole" &&
		stringValue(outbound["tag"]) == mainlandXrayBlockTag
}

func mainlandXrayManagedRule(rule map[string]any, tag string, source bool) bool {
	if !tagPattern.MatchString(tag) || len(rule) != 5 || stringValue(rule["type"]) != "field" || stringValue(rule["outboundTag"]) != mainlandXrayBlockTag ||
		!stringSliceEqual(rule["inboundTag"], tag) {
		return false
	}
	if source {
		return len(stringSliceValue(rule["source"])) > 0 && len(stringSliceValue(rule["ip"])) == 0
	}
	return len(stringSliceValue(rule["ip"])) > 0 && len(stringSliceValue(rule["source"])) == 0
}

func mainlandXrayStrategyMarker() map[string]any {
	return map[string]any{
		"type": "field", "ruleTag": mainlandXrayStrategyMarkerTag,
		"domain": []any{mainlandXrayNeverDomain}, "outboundTag": mainlandXrayBlockTag,
	}
}

func mainlandXrayManagedStrategyMarker(rule map[string]any) bool {
	return len(rule) == 4 && stringValue(rule["type"]) == "field" && stringValue(rule["outboundTag"]) == mainlandXrayBlockTag &&
		stringSliceEqual(rule["domain"], mainlandXrayNeverDomain)
}
