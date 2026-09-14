package serverconfig

import (
	"errors"
)

func mainlandSingBoxRules(tag string, destination, source bool) []any {
	rules := make([]any, 0, 2)
	if source {
		rules = append(rules, map[string]any{
			"inbound": []any{tag}, "rule_set": []any{mainlandSingBoxRuleSetTag, mainlandSingBoxIPv6RuleSetTag},
			"rule_set_ip_cidr_match_source": true, "action": "reject",
		})
	}
	if destination {
		rules = append(rules, map[string]any{
			"inbound": []any{tag}, "rule_set": []any{mainlandSingBoxRuleSetTag, mainlandSingBoxIPv6RuleSetTag}, "action": "reject",
		})
	}
	return rules
}

func mainlandSingBoxFlags(root map[string]any, tag string) (bool, bool) {
	route := mapValue(root["route"])
	rules, _ := route["rules"].([]any)
	var destination, source bool
	for _, value := range rules {
		rule, _ := value.(map[string]any)
		if mainlandSingBoxSourceRule(rule, tag) {
			source = true
		}
		if mainlandSingBoxDestinationRule(rule, tag) {
			destination = true
		}
	}
	return destination, source
}

func applyMainlandSingBox(root map[string]any, tag string, destination, source bool, prefixes []string) error {
	route := mapValue(root["route"])
	if route == nil {
		route = map[string]any{}
	}
	current, _ := route["rules"].([]any)
	hadManagedRules := false
	for _, value := range current {
		rule, _ := value.(map[string]any)
		if !stringSliceContains(rule["rule_set"], mainlandSingBoxRuleSetTag) && !stringSliceContains(rule["rule_set"], mainlandSingBoxIPv6RuleSetTag) {
			continue
		}
		if !mainlandSingBoxManagedRule(rule) {
			return errors.New("配置中的 qch-chnroutes2-cn 规则集已被其他路由规则引用")
		}
		hadManagedRules = true
	}
	rules := mainlandSingBoxRules(tag, destination, source)
	for _, value := range current {
		rule, _ := value.(map[string]any)
		if !mainlandSingBoxSourceRule(rule, tag) && !mainlandSingBoxDestinationRule(rule, tag) {
			rules = append(rules, value)
		}
	}
	route["rules"] = rules
	hasManagedRules := false
	for _, value := range rules {
		rule, _ := value.(map[string]any)
		if mainlandSingBoxManagedRule(rule) {
			hasManagedRules = true
			break
		}
	}
	ruleSets, _ := route["rule_set"].([]any)
	filtered := make([]any, 0, len(ruleSets)+2)
	for _, value := range ruleSets {
		ruleSet, _ := value.(map[string]any)
		ruleSetTag := stringValue(ruleSet["tag"])
		if ruleSetTag != mainlandSingBoxRuleSetTag && ruleSetTag != mainlandSingBoxIPv6RuleSetTag {
			filtered = append(filtered, value)
			continue
		}
		if !hadManagedRules {
			if hasManagedRules {
				return errors.New("配置中的 qch-chnroutes2-cn 规则集标签已被其他资源占用")
			}
			filtered = append(filtered, value)
			continue
		}
		if !mainlandSingBoxManagedRuleSet(ruleSet) {
			return errors.New("配置中的 qch-chnroutes2-cn 规则集标签已被其他资源占用")
		}
	}
	if hasManagedRules {
		filtered = append(filtered, mainlandSingBoxRuleSets()...)
	}
	if len(filtered) == 0 {
		delete(route, "rule_set")
	} else {
		route["rule_set"] = filtered
	}
	if len(rules) == 0 {
		delete(route, "rules")
	}
	if len(route) == 0 {
		delete(root, "route")
	} else {
		root["route"] = route
	}
	return nil
}

func mainlandSingBoxManagedRuleSet(ruleSet map[string]any) bool {
	tag := stringValue(ruleSet["tag"])
	if tag != mainlandSingBoxRuleSetTag && tag != mainlandSingBoxIPv6RuleSetTag {
		return false
	}
	// sing-box consumes CN CIDRs through a downloaded rule-set file. Accept
	// both the new remote binary form and the legacy inline form so an upgrade
	// can remove the old representation safely.
	if stringValue(ruleSet["type"]) == "remote" {
		expectedURL := mainlandSingBoxRuleSetURL
		if tag == mainlandSingBoxIPv6RuleSetTag {
			expectedURL = mainlandSingBoxIPv6RuleSetURL
		}
		return stringValue(ruleSet["format"]) == "source" && stringValue(ruleSet["url"]) == expectedURL
	}
	if stringValue(ruleSet["type"]) != "inline" {
		return false
	}
	rules, _ := ruleSet["rules"].([]any)
	if len(rules) != 1 {
		return false
	}
	rule, _ := rules[0].(map[string]any)
	return rule != nil && len(rule) == 1 && len(stringSliceValue(rule["ip_cidr"])) > 0
}

func mainlandSingBoxSourceRule(rule map[string]any, tag string) bool {
	return len(rule) == 4 && stringValue(rule["action"]) == "reject" && stringSliceEqual(rule["inbound"], tag) &&
		mainlandSingBoxRuleSetReferences(rule["rule_set"]) && boolValue(rule["rule_set_ip_cidr_match_source"])
}

func mainlandSingBoxDestinationRule(rule map[string]any, tag string) bool {
	return len(rule) == 3 && stringValue(rule["action"]) == "reject" && stringSliceEqual(rule["inbound"], tag) &&
		mainlandSingBoxRuleSetReferences(rule["rule_set"]) && !boolValue(rule["rule_set_ip_cidr_match_source"])
}

func mainlandSingBoxManagedRule(rule map[string]any) bool {
	inbounds := stringSliceValue(rule["inbound"])
	if len(inbounds) != 1 || !tagPattern.MatchString(inbounds[0]) {
		return false
	}
	return mainlandSingBoxSourceRule(rule, inbounds[0]) || mainlandSingBoxDestinationRule(rule, inbounds[0])
}

func mainlandSingBoxRuleSetReferences(value any) bool {
	return stringSliceEqual(value, mainlandSingBoxRuleSetTag) ||
		stringSliceEqual(value, mainlandSingBoxRuleSetTag, mainlandSingBoxIPv6RuleSetTag)
}

func mainlandSingBoxRuleSets() []any {
	return []any{
		map[string]any{"type": "remote", "tag": mainlandSingBoxRuleSetTag, "format": "source", "url": mainlandSingBoxRuleSetURL, "download_detour": "direct"},
		map[string]any{"type": "remote", "tag": mainlandSingBoxIPv6RuleSetTag, "format": "source", "url": mainlandSingBoxIPv6RuleSetURL, "download_detour": "direct"},
	}
}

func stringSliceEqual(value any, expected ...string) bool {
	actual := stringSliceValue(value)
	if len(actual) != len(expected) {
		return false
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

func stringSliceContains(value any, expected string) bool {
	for _, actual := range stringSliceValue(value) {
		if actual == expected {
			return true
		}
	}
	return false
}

func stringAnySlice(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
