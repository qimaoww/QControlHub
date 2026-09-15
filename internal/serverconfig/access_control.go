package serverconfig

import (
	"errors"
	"sort"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
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
	if (engine == core.EngineShadowsocksRust && !core.ValidSSRustTag(policy.Tag)) || (engine != core.EngineShadowsocksRust && !tagPattern.MatchString(policy.Tag)) {
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
