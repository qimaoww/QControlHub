package serverconfig

import (
	"errors"
	"fmt"
	"strings"
)

func mainlandMihomoDestinationRule(tag string) string {
	return fmt.Sprintf("AND,((IN-NAME,%s),(OR,((RULE-SET,%s),(RULE-SET,%s)))),REJECT", tag, mainlandMihomoProviderTag, mainlandMihomoIPv6ProviderTag)
}

func mainlandMihomoSourceRule(tag string) string {
	return fmt.Sprintf("AND,((IN-NAME,%s),(OR,((RULE-SET,%s,src),(RULE-SET,%s,src)))),REJECT", tag, mainlandMihomoProviderTag, mainlandMihomoIPv6ProviderTag)
}

func mainlandMihomoRules(tag string, destination, source bool) []string {
	rules := make([]string, 0, 2)
	if source {
		rules = append(rules, mainlandMihomoSourceRule(tag))
	}
	if destination {
		rules = append(rules, mainlandMihomoDestinationRule(tag))
	}
	return rules
}

func mainlandMihomoFlags(root map[string]any, tag string) (bool, bool) {
	destinationRule, sourceRule := mainlandMihomoDestinationRule(tag), mainlandMihomoSourceRule(tag)
	var destination, source bool
	for _, rule := range stringSliceValue(root["rules"]) {
		switch strings.TrimSpace(rule) {
		case destinationRule:
			destination = true
		case sourceRule:
			source = true
		}
	}
	return destination, source
}

func applyMainlandMihomo(root map[string]any, tag string, destination, source bool) error {
	destinationRule, sourceRule := mainlandMihomoDestinationRule(tag), mainlandMihomoSourceRule(tag)
	current := stringSliceValue(root["rules"])
	hadManagedRules := false
	hasForeignProviderReference := false
	for _, rule := range current {
		if mainlandMihomoManagedRule(rule) {
			hadManagedRules = true
			continue
		}
		if mainlandMihomoProviderReferenced(rule) {
			hasForeignProviderReference = true
		}
	}
	providers := mapValue(root["rule-providers"])
	if providers == nil {
		providers = map[string]any{}
	}
	_, hasIPv4Provider := providers[mainlandMihomoProviderTag]
	_, hasIPv6Provider := providers[mainlandMihomoIPv6ProviderTag]
	hasReservedProvider := hasIPv4Provider || hasIPv6Provider
	if !hadManagedRules && !destination && !source {
		// No canonical QControlHub rule proves ownership of the reserved
		// providers. A disabled policy therefore leaves them untouched.
		return nil
	}
	if !hadManagedRules && (hasReservedProvider || hasForeignProviderReference) {
		return errors.New("配置中的大陆路由规则集标签已被其他资源占用")
	}
	if hadManagedRules && hasForeignProviderReference {
		return errors.New("配置中的大陆路由规则集已被其他规则引用")
	}
	if hadManagedRules {
		if value, exists := providers[mainlandMihomoProviderTag]; exists {
			existing := mapValue(value)
			if existing == nil || !mainlandMihomoManagedProvider(existing) {
				return errors.New("配置中的 qch-chnroutes2-cn 规则集标签已被其他资源占用")
			}
		}
		if value, exists := providers[mainlandMihomoIPv6ProviderTag]; exists {
			existing := mapValue(value)
			if existing == nil || !mainlandMihomoManagedIPv6Provider(existing) {
				return errors.New("配置中的 qch-china-cn-ipv6 规则集标签已被其他资源占用")
			}
		}
	}
	rules := mainlandMihomoRules(tag, destination, source)
	for _, rule := range current {
		trimmed := strings.TrimSpace(rule)
		if trimmed != destinationRule && trimmed != sourceRule {
			rules = append(rules, rule)
		}
	}
	hasManagedRules := false
	for _, rule := range rules {
		if mainlandMihomoManagedRule(rule) {
			hasManagedRules = true
			break
		}
	}
	if len(rules) == 0 {
		delete(root, "rules")
	} else {
		root["rules"] = rules
	}
	if hasManagedRules {
		providers[mainlandMihomoProviderTag] = map[string]any{
			"type": "http", "behavior": "ipcidr", "format": "text", "url": ChinaRoutesURL,
			"path": "./ruleset/qch-chnroutes2-cn.txt", "interval": 3600,
		}
		providers[mainlandMihomoIPv6ProviderTag] = map[string]any{
			"type": "http", "behavior": "ipcidr", "format": "text", "url": ChinaRoutesIPv6URL,
			"path": "./ruleset/qch-china-cn-ipv6.txt", "interval": 86400,
		}
	} else {
		delete(providers, mainlandMihomoProviderTag)
		delete(providers, mainlandMihomoIPv6ProviderTag)
	}
	if len(providers) == 0 {
		delete(root, "rule-providers")
	} else {
		root["rule-providers"] = providers
	}
	return nil
}

func mainlandMihomoManagedRule(rule string) bool {
	trimmed := strings.TrimSpace(rule)
	const prefix = "AND,((IN-NAME,"
	if !strings.HasPrefix(trimmed, prefix) {
		return false
	}
	rest := strings.TrimPrefix(trimmed, prefix)
	separator := strings.Index(rest, "),")
	if separator <= 0 {
		return false
	}
	tag := rest[:separator]
	return tagPattern.MatchString(tag) && (trimmed == mainlandMihomoDestinationRule(tag) || trimmed == mainlandMihomoSourceRule(tag))
}

func mainlandMihomoProviderReferenced(rule string) bool {
	return strings.Contains(rule, "RULE-SET,"+mainlandMihomoProviderTag) ||
		strings.Contains(rule, "RULE-SET,"+mainlandMihomoIPv6ProviderTag)
}

func mainlandMihomoManagedIPv6Provider(provider map[string]any) bool {
	if mainlandMihomoInlineProvider(provider) {
		return true
	}
	return len(provider) == 6 && stringValue(provider["type"]) == "http" && stringValue(provider["behavior"]) == "ipcidr" &&
		stringValue(provider["format"]) == "text" && stringValue(provider["url"]) == ChinaRoutesIPv6URL &&
		stringValue(provider["path"]) == "./ruleset/qch-china-cn-ipv6.txt" && intValue(provider["interval"]) == 86400
}

func mainlandMihomoInlineProvider(provider map[string]any) bool {
	return len(provider) == 3 && stringValue(provider["type"]) == "inline" && stringValue(provider["behavior"]) == "ipcidr" && len(stringSliceValue(provider["payload"])) > 0
}

func mainlandMihomoManagedProvider(provider map[string]any) bool {
	if mainlandMihomoInlineProvider(provider) {
		return true
	}
	return len(provider) == 6 && stringValue(provider["type"]) == "http" && stringValue(provider["behavior"]) == "ipcidr" &&
		stringValue(provider["format"]) == "text" && stringValue(provider["url"]) == ChinaRoutesURL &&
		stringValue(provider["path"]) == "./ruleset/qch-chnroutes2-cn.txt" && intValue(provider["interval"]) == 3600
}
