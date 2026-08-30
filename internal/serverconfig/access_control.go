package serverconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

const (
	mainlandXrayBlockTag          = "qch-mainland-block"
	mainlandXrayDestinationPrefix = "qch-mainland-destination:"
	mainlandXraySourcePrefix      = "qch-mainland-source:"
	mainlandXrayStrategyMarkerTag = "qch-mainland-domain-strategy"
	mainlandXrayNeverDomain       = "regexp:^$"
	mainlandMihomoProviderTag     = "qch-chnroutes2-cn-ipv4"
	mainlandMihomoIPv6ProviderTag = "qch-china-cn-ipv6"
	mainlandSingBoxRuleSetTag     = "qch-chnroutes2-cn"
	mainlandSingBoxIPv6RuleSetTag = "qch-china-cn-ipv6"
	// sing-box expects rule-set files in its binary/source SRS formats. Use the
	// daily-built chnroutes2 IPv4 and APNIC IPv6 source files instead of an inline
	// CIDR array.
	mainlandSingBoxRuleSetURL     = "https://raw.githubusercontent.com/Dreista/sing-box-rule-set-cn/rule-set/chnroutes.txt.json"
	mainlandSingBoxIPv6RuleSetURL = "https://raw.githubusercontent.com/Dreista/sing-box-rule-set-cn/rule-set/apnic-cn-ipv6.json"
)

// MainlandAccessPolicy is intentionally scoped to one named inbound. Keep the
// wire representation in core so Agents can apply out-of-config policies
// for engines that do not expose routing hooks (such as Shadowsocks Rust).
type MainlandAccessPolicy = core.MainlandAccessPolicy

// DiscoverMainlandAccessPolicies returns only named inbounds that can be
// safely targeted by either the core or the Agent's engine-specific policy.
func DiscoverMainlandAccessPolicies(engine core.Engine, content string) []MainlandAccessPolicy {
	root := decodeTrafficConfiguration(engine, content)
	if root == nil {
		return nil
	}
	var entries []any
	var nameField, kindField, portField string
	switch engine {
	case core.EngineMihomo:
		entries, _ = root["listeners"].([]any)
		nameField, kindField, portField = "name", "type", "port"
	case core.EngineXray:
		entries, _ = root["inbounds"].([]any)
		nameField, kindField, portField = "tag", "protocol", "port"
	case core.EngineSingBox:
		entries, _ = root["inbounds"].([]any)
		nameField, kindField, portField = "tag", "type", "listen_port"
	case core.EngineShadowsocksRust:
		// ssserver stores one service at the document root or several services
		// under the official `servers`/`shadowsocks` arrays rather than exposing
		// a routing-capable inbound list.
		for _, input := range ParseAll(core.EngineShadowsocksRust, content) {
			result := MainlandAccessPolicy{Tag: input.Tag, Port: input.Port, Kind: input.Protocol, Engine: engine}
			// The durable ACL/firewall flags are merged by the API layer.
			entries = append(entries, map[string]any{"__policy": result})
		}
	default:
		return nil
	}
	result := make([]MainlandAccessPolicy, 0, len(entries))
	for _, value := range entries {
		if entry, ok := value.(map[string]any); ok {
			if policyValue, ok := entry["__policy"].(MainlandAccessPolicy); ok {
				result = append(result, policyValue)
				continue
			}
		}
		entry, _ := value.(map[string]any)
		if entry == nil {
			continue
		}
		tag := strings.TrimSpace(stringValue(entry[nameField]))
		port := trafficPortNumber(entry[portField])
		if !tagPattern.MatchString(tag) || port == 0 {
			continue
		}
		destination, source := mainlandAccessFlags(engine, root, tag)
		result = append(result, MainlandAccessPolicy{
			Tag: tag, Port: port, Kind: strings.TrimSpace(stringValue(entry[kindField])), Engine: engine,
			BlockMainlandDestination: destination, BlockMainlandSource: source,
		})
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Port != result[right].Port {
			return result[left].Port < result[right].Port
		}
		return result[left].Tag < result[right].Tag
	})
	return result
}

// ApplyMainlandAccessPolicyWithPrefixes applies a mainland policy to one
// inbound. Mihomo keeps its upstream rule-provider files, Xray uses its
// installed geoip.dat, sing-box uses remote rule-set files, and Shadowsocks Rust
// leaves its native JSON untouched for an Agent-managed ACL/firewall policy. The
// prefixes argument is retained for API compatibility and legacy callers.
func ApplyMainlandAccessPolicyWithPrefixes(engine core.Engine, content string, policy MainlandAccessPolicy, prefixes []string) (string, error) {
	if engine != core.EngineMihomo && engine != core.EngineXray && engine != core.EngineSingBox && engine != core.EngineShadowsocksRust {
		return "", errors.New("该内核暂不支持按入站限制大陆访问")
	}
	policy.Tag = strings.TrimSpace(policy.Tag)
	if !tagPattern.MatchString(policy.Tag) {
		return "", errors.New("入站标签只能包含字母、数字、点、下划线和短横线，最长 64 位")
	}
	if policy.Port < 1 || policy.Port > 65535 {
		return "", errors.New("监听端口必须在 1 到 65535 之间")
	}
	if engine == core.EngineShadowsocksRust && policy.BlockMainlandDestination {
		return "", errors.New("Shadowsocks Rust 仅支持按入站端口封禁大陆来源，目标限制请使用支持路由的内核")
	}
	root := decodeTrafficConfiguration(engine, content)
	if root == nil {
		return "", errors.New("无法解析当前内核配置")
	}
	if !mainlandInboundExists(engine, root, policy.Tag, policy.Port) {
		return "", errors.New("当前配置中不存在匹配的入站标签和端口")
	}
	var err error
	switch engine {
	case core.EngineMihomo:
		err = applyMainlandMihomo(root, policy.Tag, policy.BlockMainlandDestination, policy.BlockMainlandSource)
	case core.EngineXray:
		err = applyMainlandXray(root, policy.Tag, policy.BlockMainlandDestination, policy.BlockMainlandSource, prefixes)
	case core.EngineSingBox:
		err = applyMainlandSingBox(root, policy.Tag, policy.BlockMainlandDestination, policy.BlockMainlandSource, prefixes)
	case core.EngineShadowsocksRust:
		// ACL/firewall policy state is persisted separately by the API. The
		// ssserver configuration must stay a native JSON document.
		err = nil
	}
	if err != nil {
		return "", err
	}
	return marshalMainlandConfiguration(engine, root)
}

// RemoveMainlandAccessPolicy removes QControlHub-owned rules for a deleted or
// renamed inbound. It is deliberately tolerant when the tag has no rule.
func RemoveMainlandAccessPolicy(engine core.Engine, content, tag string) (string, error) {
	root := decodeTrafficConfiguration(engine, content)
	if root == nil {
		return "", errors.New("无法解析当前内核配置")
	}
	switch engine {
	case core.EngineMihomo:
		if err := applyMainlandMihomo(root, strings.TrimSpace(tag), false, false); err != nil {
			return "", err
		}
	case core.EngineXray:
		if err := applyMainlandXray(root, strings.TrimSpace(tag), false, false, nil); err != nil {
			return "", err
		}
	case core.EngineSingBox:
		if err := applyMainlandSingBox(root, strings.TrimSpace(tag), false, false, nil); err != nil {
			return "", err
		}
	default:
		return content, nil
	}
	return marshalMainlandConfiguration(engine, root)
}

func mainlandAccessFlags(engine core.Engine, root map[string]any, tag string) (bool, bool) {
	if engine == core.EngineMihomo {
		return mainlandMihomoFlags(root, tag)
	}
	if engine == core.EngineXray {
		return mainlandXrayFlags(root, tag)
	}
	if engine == core.EngineSingBox {
		return mainlandSingBoxFlags(root, tag)
	}
	return false, false
}

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
	return len(provider) == 6 && stringValue(provider["type"]) == "http" && stringValue(provider["behavior"]) == "ipcidr" &&
		stringValue(provider["format"]) == "text" && stringValue(provider["url"]) == ChinaRoutesIPv6URL &&
		stringValue(provider["path"]) == "./ruleset/qch-china-cn-ipv6.txt" && intValue(provider["interval"]) == 86400
}

func mainlandMihomoManagedProvider(provider map[string]any) bool {
	return len(provider) == 6 && stringValue(provider["type"]) == "http" && stringValue(provider["behavior"]) == "ipcidr" &&
		stringValue(provider["format"]) == "text" && stringValue(provider["url"]) == ChinaRoutesURL &&
		stringValue(provider["path"]) == "./ruleset/qch-chnroutes2-cn.txt" && intValue(provider["interval"]) == 3600
}

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
	if tag != mainlandSingBoxRuleSetTag || stringValue(ruleSet["type"]) != "inline" {
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

func mainlandInboundExists(engine core.Engine, root map[string]any, tag string, port int) bool {
	if engine == core.EngineShadowsocksRust {
		entries := ParseAll(engine, mustMarshalJSON(root))
		for _, input := range entries {
			// The single-service ssserver JSON format has no tag field; its
			// canonical synthetic tag is `ss-rust`. Accept the operator's
			// generated tag when there is only one service and keep the durable
			// firewall key stable by port.
			if input.Port == port && (input.Tag == tag || len(entries) == 1) {
				return true
			}
		}
		return false
	}
	field, portField, values := "name", "port", root["listeners"]
	if engine == core.EngineXray {
		field, values = "tag", root["inbounds"]
	} else if engine == core.EngineSingBox {
		field, portField, values = "tag", "listen_port", root["inbounds"]
	}
	entries, _ := values.([]any)
	for _, value := range entries {
		entry, _ := value.(map[string]any)
		if stringValue(entry[field]) == tag && trafficPortNumber(entry[portField]) == port {
			return true
		}
	}
	return false
}

func mustMarshalJSON(root map[string]any) string {
	value, err := json.Marshal(root)
	if err != nil {
		return ""
	}
	return string(value)
}

func marshalMainlandConfiguration(engine core.Engine, root map[string]any) (string, error) {
	if engine == core.EngineMihomo {
		value, err := yaml.Marshal(root)
		return string(value), err
	}
	value, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(value) + "\n", nil
}
