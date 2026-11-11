package serverconfig

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

const udp443Provider = "qch-block-udp443"

func udp443ProviderValue() map[string]any {
	return map[string]any{"type": "inline", "behavior": "classical", "payload": []string{"AND,((NETWORK,UDP),(DST-PORT,443))"}}
}

func udp443OwnedProvider(root map[string]any) bool {
	provider := mapValue(mapValue(root["rule-providers"])[udp443Provider])
	return len(provider) == 3 && stringValue(provider["type"]) == "inline" && stringValue(provider["behavior"]) == "classical" && reflect.DeepEqual(stringSliceValue(provider["payload"]), stringSliceValue(udp443ProviderValue()["payload"]))
}

func udp443Rule(tag string) string {
	return fmt.Sprintf("AND,((IN-NAME,%s),(RULE-SET,%s)),REJECT", tag, udp443Provider)
}

func udp443RuleTag(rule string) string {
	const prefix = "AND,((IN-NAME,"
	if !strings.HasPrefix(rule, prefix) {
		return ""
	}
	tag, _, ok := strings.Cut(strings.TrimPrefix(rule, prefix), "),")
	if !ok || !tagPattern.MatchString(tag) || rule != udp443Rule(tag) {
		return ""
	}
	return tag
}

func udp443MihomoEnabled(root map[string]any, tag string) bool {
	if !udp443OwnedProvider(root) {
		return false
	}
	for _, rule := range stringSliceValue(root["rules"]) {
		if rule == udp443Rule(tag) {
			return true
		}
	}
	return false
}

// Ownership requires both the reserved, exact inline provider and a canonical
// scoped rule. A handwritten UDP/443 rule without that provider is never removed.
func applyUDP443Mihomo(root map[string]any, tag string, enabled bool) error {
	providers := mapValue(root["rule-providers"])
	owned := udp443OwnedProvider(root)
	if _, exists := providers[udp443Provider]; exists && !owned {
		if enabled {
			return fmt.Errorf("规则集 %s 已被自定义配置占用", udp443Provider)
		}
		return nil
	}
	current := stringSliceValue(root["rules"])
	for _, rule := range current {
		if (strings.Contains(rule, "RULE-SET,"+udp443Provider+",") || strings.Contains(rule, "RULE-SET,"+udp443Provider+")")) && (!owned || udp443RuleTag(rule) == "") {
			if enabled || owned {
				return fmt.Errorf("规则集 %s 被自定义规则引用，请在完整源码中调整", udp443Provider)
			}
		}
	}
	if !enabled && !owned {
		return nil
	}
	if enabled {
		if mode := stringValue(root["mode"]); mode != "" && !strings.EqualFold(mode, "rule") {
			return fmt.Errorf("拦截 UDP 443 需要 Mihomo rule 模式")
		}
		listeners, _ := root["listeners"].([]any)
		for _, raw := range listeners {
			in := mapValue(raw)
			if stringValue(in["name"]) != tag {
				continue
			}
			if stringValue(in["proxy"]) != "" || stringValue(in["rule"]) != "" {
				return fmt.Errorf("入站出口或子规则覆盖全局路由，无法启用拦截 UDP 443")
			}
		}
	}
	rules := []string{}
	if enabled {
		rules = append(rules, udp443Rule(tag))
	}
	for _, rule := range current {
		if rule != udp443Rule(tag) {
			rules = append(rules, rule)
		}
	}
	root["rules"] = rules
	needed := false
	for _, rule := range rules {
		needed = needed || udp443RuleTag(rule) != ""
	}
	if needed {
		if providers == nil {
			providers = map[string]any{}
		}
		providers[udp443Provider] = udp443ProviderValue()
	} else {
		delete(providers, udp443Provider)
	}
	if len(providers) > 0 {
		root["rule-providers"] = providers
	} else {
		delete(root, "rule-providers")
	}
	return nil
}

// Apply the policy after native list mutation, including the delta-edit fast
// path. Only replace the affected root sections so unknown YAML is retained.
func mergeUDP443Mihomo(content, generated, previous, operation string) (string, error) {
	var root map[string]any
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return "", err
	}
	before := map[string]string{}
	for _, key := range []string{"rules", "rule-providers"} {
		if value, exists := root[key]; exists {
			data, err := yaml.Marshal(value)
			if err != nil {
				return "", err
			}
			before[key] = string(data)
		}
	}
	if previous != "" {
		if err := applyUDP443Mihomo(root, previous, false); err != nil {
			return "", err
		}
	}
	if operation != "delete" {
		input, ok := Parse(core.EngineMihomo, generated)
		if !ok {
			return "", fmt.Errorf("无法读取生成的 Mihomo 入站")
		}
		if err := applyUDP443Mihomo(root, input.Tag, input.BlockUDP443 != nil && *input.BlockUDP443); err != nil {
			return "", err
		}
	}
	for _, key := range []string{"rules", "rule-providers"} {
		value, present := root[key]
		data, err := yaml.Marshal(value)
		if err != nil {
			return "", err
		}
		old, existed := before[key]
		if existed == present && (!present || old == string(data)) {
			continue
		}
		content, err = configschema.MergeFragment(core.EngineMihomo, content, key, string(data), !present)
		if err != nil {
			return "", err
		}
	}
	return content, nil
}

// Source edits may move a managed rule behind an early allow, or replace its
// listener. Refuse such configurations before saving/compiling a deployment.
func validateUDP443Mihomo(root map[string]any) error {
	if !udp443OwnedProvider(root) {
		return nil
	}
	bypass := false
	for _, rule := range stringSliceValue(root["rules"]) {
		tag := udp443RuleTag(rule)
		if tag == "" {
			if !strings.HasPrefix(accountingMihomoTarget(rule), "REJECT") && !strings.HasPrefix(accountingMihomoTarget(rule), accountingPrefix) {
				bypass = true
			}
			continue
		}
		if bypass {
			return fmt.Errorf("入站 %s 的 UDP 443 拦截规则位于放行规则之后，请在完整源码中调整顺序", tag)
		}
		found := false
		listeners, _ := root["listeners"].([]any)
		for _, raw := range listeners {
			in := mapValue(raw)
			if stringValue(in["name"]) != tag {
				continue
			}
			found = true
			if !mihomoUDP443Applicable(in) {
				return fmt.Errorf("入站 %s 不再适用 UDP 443 拦截，请在完整源码中移除对应规则", tag)
			}
		}
		if !found {
			return fmt.Errorf("UDP 443 拦截规则引用了不存在的入站 %s", tag)
		}
	}
	return nil
}

func mihomoUDP443Applicable(in map[string]any) bool {
	return stringValue(in["type"]) == "vless" && mapValue(in["reality-config"]) != nil && stringValue(firstMap(in["users"])["flow"]) == "xtls-rprx-vision" && in["xhttp-config"] == nil && stringValue(in["decryption"]) == ""
}
