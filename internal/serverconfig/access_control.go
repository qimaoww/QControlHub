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
	mainlandMihomoProviderTag     = "qch-chnroutes2-cn-ipv4"
	mainlandMihomoIPv6ProviderTag = "qch-china-cn-ipv6"
	mainlandSingBoxRuleSetTag     = "qch-chnroutes2-cn"
)

// MainlandAccessPolicy is intentionally scoped to one named inbound. The
// policy is rendered into the proxy core configuration, never into a node-wide
// firewall, so qagent and unrelated listening ports remain reachable.
type MainlandAccessPolicy struct {
	Tag                      string      `json:"tag"`
	Port                     int         `json:"port"`
	Kind                     string      `json:"kind"`
	Engine                   core.Engine `json:"engine"`
	BlockMainlandDestination bool        `json:"block_mainland_destination"`
	BlockMainlandSource      bool        `json:"block_mainland_source"`
}

// DiscoverMainlandAccessPolicies returns only named inbounds that can be
// safely targeted by the core's routing engine.
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
	default:
		return nil
	}
	result := make([]MainlandAccessPolicy, 0, len(entries))
	for _, value := range entries {
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

// ApplyMainlandAccessPolicyWithPrefixes embeds validated mainland CIDRs in
// cores that do not natively consume the upstream text feeds. It replaces
// QControlHub-owned rules for exactly one inbound while retaining operator-
// authored routing rules and their order.
func ApplyMainlandAccessPolicyWithPrefixes(engine core.Engine, content string, policy MainlandAccessPolicy, prefixes []string) (string, error) {
	if engine != core.EngineMihomo && engine != core.EngineXray && engine != core.EngineSingBox {
		return "", errors.New("该内核暂不支持按入站限制大陆访问")
	}
	policy.Tag = strings.TrimSpace(policy.Tag)
	if !tagPattern.MatchString(policy.Tag) {
		return "", errors.New("入站标签只能包含字母、数字、点、下划线和短横线，最长 64 位")
	}
	if policy.Port < 1 || policy.Port > 65535 {
		return "", errors.New("监听端口必须在 1 到 65535 之间")
	}
	if engine != core.EngineMihomo && (policy.BlockMainlandDestination || policy.BlockMainlandSource) {
		if err := validateChinaRoutePrefixes(prefixes); err != nil {
			return "", err
		}
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
	rules := mainlandMihomoRules(tag, destination, source)
	for _, rule := range current {
		trimmed := strings.TrimSpace(rule)
		if trimmed != destinationRule && trimmed != sourceRule {
			rules = append(rules, rule)
		}
	}
	root["rules"] = rules
	providers := mapValue(root["rule-providers"])
	if providers == nil {
		providers = map[string]any{}
	}
	hasManagedRules := false
	for _, rule := range rules {
		if strings.Contains(rule, "RULE-SET,"+mainlandMihomoProviderTag) || strings.Contains(rule, "RULE-SET,"+mainlandMihomoIPv6ProviderTag) {
			hasManagedRules = true
			break
		}
	}
	if existing := mapValue(providers[mainlandMihomoProviderTag]); existing != nil && !mainlandMihomoManagedProvider(existing) {
		return errors.New("配置中的 qch-chnroutes2-cn 规则集标签已被其他资源占用")
	}
	if existing := mapValue(providers[mainlandMihomoIPv6ProviderTag]); existing != nil && !mainlandMihomoManagedIPv6Provider(existing) {
		return errors.New("配置中的 qch-china-cn-ipv6 规则集标签已被其他资源占用")
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

func mainlandMihomoManagedIPv6Provider(provider map[string]any) bool {
	return stringValue(provider["type"]) == "http" && stringValue(provider["behavior"]) == "ipcidr" &&
		stringValue(provider["format"]) == "text" && stringValue(provider["url"]) == ChinaRoutesIPv6URL
}

func mainlandMihomoManagedProvider(provider map[string]any) bool {
	return stringValue(provider["type"]) == "http" && stringValue(provider["behavior"]) == "ipcidr" &&
		stringValue(provider["format"]) == "text" && stringValue(provider["url"]) == ChinaRoutesURL
}

func mainlandXrayRouting(tag string, destination, source bool) map[string]any {
	rules := mainlandXrayRules(tag, destination, source, nil)
	if len(rules) == 0 {
		return nil
	}
	return map[string]any{"domainStrategy": "IPIfNonMatch", "rules": rules}
}

func mainlandXrayRules(tag string, destination, source bool, prefixes []string) []any {
	ipValues := stringAnySlice(prefixes)
	if len(ipValues) == 0 {
		ipValues = []any{"geoip:cn"}
	}
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
	for _, value := range current {
		rule, _ := value.(map[string]any)
		ruleTag := stringValue(rule["ruleTag"])
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
	routing["rules"] = rules
	if destination && strings.TrimSpace(stringValue(routing["domainStrategy"])) == "" {
		routing["domainStrategy"] = "IPIfNonMatch"
	}
	if len(rules) == 0 && len(routing) == 1 {
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
		if !hadManagedRules {
			if hasManagedRules {
				return errors.New("配置中的 qch-mainland-block 出站标签已被其他出站占用")
			}
			filtered = append(filtered, value)
			continue
		}
		if stringValue(outbound["protocol"]) != "blackhole" {
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

func mainlandXrayManagedRule(rule map[string]any, tag string, source bool) bool {
	if stringValue(rule["type"]) != "field" || stringValue(rule["outboundTag"]) != mainlandXrayBlockTag ||
		!stringSliceEqual(rule["inboundTag"], tag) {
		return false
	}
	if source {
		return len(stringSliceValue(rule["source"])) > 0 && len(stringSliceValue(rule["ip"])) == 0
	}
	return len(stringSliceValue(rule["ip"])) > 0 && len(stringSliceValue(rule["source"])) == 0
}

func mainlandSingBoxRules(tag string, destination, source bool) []any {
	rules := make([]any, 0, 2)
	if source {
		rules = append(rules, map[string]any{
			"inbound": []any{tag}, "rule_set": []any{mainlandSingBoxRuleSetTag},
			"rule_set_ip_cidr_match_source": true, "action": "reject",
		})
	}
	if destination {
		rules = append(rules, map[string]any{
			"inbound": []any{tag}, "rule_set": []any{mainlandSingBoxRuleSetTag}, "action": "reject",
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
		if !stringSliceEqual(rule["rule_set"], mainlandSingBoxRuleSetTag) {
			continue
		}
		if !mainlandSingBoxManagedRule(rule) || len(stringSliceValue(rule["inbound"])) != 1 {
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
	filtered := make([]any, 0, len(ruleSets)+1)
	foundManagedRuleSet := false
	for _, value := range ruleSets {
		ruleSet, _ := value.(map[string]any)
		if stringValue(ruleSet["tag"]) != mainlandSingBoxRuleSetTag {
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
		foundManagedRuleSet = true
		if hasManagedRules && len(prefixes) == 0 {
			filtered = append(filtered, value)
		}
	}
	if hasManagedRules && len(prefixes) > 0 {
		filtered = append(filtered, mainlandSingBoxRuleSet(prefixes))
	} else if hasManagedRules && !foundManagedRuleSet {
		return errors.New("Sing-box 大陆访问限制需要有效的 chnroutes2 路由表")
	}
	route["rule_set"] = filtered
	if len(rules) == 0 && len(filtered) == 0 && len(route) == 2 {
		delete(root, "route")
	} else {
		root["route"] = route
	}
	return nil
}

func mainlandSingBoxManagedRuleSet(ruleSet map[string]any) bool {
	if stringValue(ruleSet["type"]) != "inline" {
		return false
	}
	rules, _ := ruleSet["rules"].([]any)
	if len(rules) != 1 {
		return false
	}
	rule, _ := rules[0].(map[string]any)
	return rule != nil && len(stringSliceValue(rule["ip_cidr"])) > 0
}

func mainlandSingBoxSourceRule(rule map[string]any, tag string) bool {
	return stringValue(rule["action"]) == "reject" && stringSliceEqual(rule["inbound"], tag) &&
		stringSliceEqual(rule["rule_set"], mainlandSingBoxRuleSetTag) && boolValue(rule["rule_set_ip_cidr_match_source"])
}

func mainlandSingBoxDestinationRule(rule map[string]any, tag string) bool {
	return stringValue(rule["action"]) == "reject" && stringSliceEqual(rule["inbound"], tag) &&
		stringSliceEqual(rule["rule_set"], mainlandSingBoxRuleSetTag) && !boolValue(rule["rule_set_ip_cidr_match_source"])
}

func mainlandSingBoxManagedRule(rule map[string]any) bool {
	return stringValue(rule["action"]) == "reject" && stringSliceEqual(rule["rule_set"], mainlandSingBoxRuleSetTag)
}

func mainlandSingBoxRuleSet(prefixes []string) map[string]any {
	return map[string]any{
		"type": "inline", "tag": mainlandSingBoxRuleSetTag,
		"rules": []any{map[string]any{"ip_cidr": stringAnySlice(prefixes)}},
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

func stringAnySlice(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func mainlandInboundExists(engine core.Engine, root map[string]any, tag string, port int) bool {
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
