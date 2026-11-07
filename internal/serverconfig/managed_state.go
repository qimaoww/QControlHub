package serverconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// ManagedState belongs to a configuration revision, outside kernel source.
// Source always retains the complete objects. CompileManagedConfig produces a
// separate runtime artifact without passing panel-only switches to the core.
type ManagedState struct {
	Disabled []ManagedResource `json:"disabled"`
}

type ManagedResource struct {
	Section string `json:"section"`
	Tag     string `json:"tag"`
}

func managedDocument(engine core.Engine, content string) (map[string]any, error) {
	var root map[string]any
	if engine == core.EngineMihomo {
		if err := yaml.Unmarshal([]byte(content), &root); err != nil {
			return nil, err
		}
	} else {
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&root); err != nil {
			return nil, err
		}
	}
	if root == nil {
		return nil, errors.New("配置必须为对象")
	}
	return root, nil
}
func managedTag(engine core.Engine, value any) string {
	object, _ := value.(map[string]any)
	if engine == core.EngineShadowsocksRust {
		return shadowsocksRustSingleTag(object)
	}
	key := "tag"
	if engine == core.EngineMihomo {
		key = "name"
	}
	result, _ := object[key].(string)
	return result
}
func managedSections(engine core.Engine) map[string]bool {
	switch engine {
	case core.EngineXray:
		return map[string]bool{"inbounds": true, "outbounds": false}
	case core.EngineSingBox:
		return map[string]bool{"inbounds": true, "endpoints": true, "outbounds": false}
	case core.EngineMihomo:
		return map[string]bool{"listeners": true, "proxies": false, "proxy-groups": false}
	case core.EngineShadowsocksRust:
		return map[string]bool{"servers": true, "shadowsocks": true}
	default:
		return nil
	}
}

// Fail closed when an outbound is still selected anywhere. Removing a route or
// a default selector would silently change its traffic to another exit.
func managedReferences(value any, target string) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			switch key {
			case "outbound", "outboundTag", "detour", "final", "proxy", "dialer-proxy", "proxy-server-nameserver", "dialerProxy", "fallbackTag", "default-dialer-proxy":
				if item == target {
					return true
				}
			case "proxySettings":
				if stringValue(mapValue(item)["tag"]) == target {
					return true
				}
			case "selector":
				// Xray balancer selectors match outbound tag prefixes. Removing
				// a member would silently redistribute traffic among other exits.
				if selectors, ok := item.([]any); ok {
					for _, selector := range selectors {
						if prefix, ok := selector.(string); ok && strings.HasPrefix(target, prefix) {
							return true
						}
					}
				}
			case "outbounds", "proxies":
				if list, ok := item.([]any); ok {
					for _, entry := range list {
						if entry == target {
							return true
						}
					}
				}
			}
			if managedReferences(item, target) {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if managedReferences(item, target) {
				return true
			}
		}
	case string:
		// Mihomo rule/sub-rule strings select an outbound in their last component.
		parts := strings.Split(value, ",")
		if len(parts) > 1 && (strings.TrimSpace(parts[len(parts)-1]) == target || len(parts) > 2 && strings.TrimSpace(parts[len(parts)-1]) == "no-resolve" && strings.TrimSpace(parts[len(parts)-2]) == target) {
			return true
		}
	}
	return false
}

func CompileManagedConfig(engine core.Engine, source string, state ManagedState) (string, error) {
	if len(state.Disabled) == 0 {
		return source, nil
	}
	if len(state.Disabled) > 4096 {
		return "", errors.New("停用对象过多")
	}
	// Generated accounting copies refer to the complete enabled source. Verify
	// and remove them before compiling a different listener set; task creation
	// replans only the remaining listeners using the established accounting rules.
	if strings.Contains(source, accountingPrefix) || engine == core.EngineShadowsocksRust && strings.Contains(source, "outbound_fwmark") {
		var err error
		source, err = PresetAccountingSource(engine, source)
		if err != nil {
			return "", err
		}
	}
	if engine == core.EngineShadowsocksRust {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(source), &raw); err != nil {
			return "", err
		}
		if raw["server_port"] != nil {
			parsed, ok := parseShadowsocksRust(source)
			if !ok {
				return "", errors.New("无法识别 SS Rust 监听")
			}
			root, entry, commit, err := ssRustFieldTarget(source, parsed.Tag, "server")
			if err != nil {
				return "", err
			}
			if err := commit(entry); err != nil {
				return "", err
			}
			encoded, err := json.Marshal(root)
			if err != nil {
				return "", err
			}
			source = string(encoded)
		}
	}
	root, err := managedDocument(engine, source)
	if err != nil {
		return "", err
	}
	if engine == core.EngineShadowsocksRust {
		if root["servers"] != nil && root["shadowsocks"] != nil {
			return "", errors.New("SS Rust 服务端列表格式冲突")
		}
		for _, section := range []string{"servers", "shadowsocks"} {
			values, _ := root[section].([]any)
			for index, value := range values {
				object := mapValue(value)
				if object != nil {
					object["id"] = shadowsocksRustEntryTag(object, index)
				}
			}
		}
	}
	sections := managedSections(engine)
	if sections == nil {
		return "", errors.New("此内核尚未支持配置对象独立停用")
	}
	seen := map[ManagedResource]bool{}
	// Resolve every identity before changing anything, so duplicate tags and
	// stale selections cannot accidentally target another listener.
	for _, ref := range state.Disabled {
		if _, ok := sections[ref.Section]; !ok || ref.Tag == "" || seen[ref] {
			return "", errors.New("停用对象标识无效或重复")
		}
		seen[ref] = true
		values, ok := root[ref.Section].([]any)
		if !ok {
			return "", errors.New("停用对象所在配置已变化")
		}
		count := 0
		for _, value := range values {
			if managedTag(engine, value) == ref.Tag {
				if engine == core.EngineXray && ref.Section == "inbounds" && xrayInternalAPIInbound(root, mapValue(value)) {
					return "", errors.New("内核内部 API 监听不能独立停用")
				}
				count++
			}
		}
		if count != 1 {
			return "", fmt.Errorf("停用对象 %s 的标识已变化或不唯一", ref.Tag)
		}
	}
	// Strip only incoming objects first, then reconcile their selectors without
	// widening any rule to traffic from other incoming listeners.
	for _, ref := range state.Disabled {
		if !sections[ref.Section] {
			continue
		}
		values := root[ref.Section].([]any)
		kept := make([]any, 0, len(values))
		for _, value := range values {
			if managedTag(engine, value) != ref.Tag {
				kept = append(kept, value)
			}
		}
		root[ref.Section] = kept
	}
	marshal := func() (string, error) {
		if engine == core.EngineMihomo {
			value, err := yaml.Marshal(root)
			return string(value), err
		}
		value, err := json.Marshal(root)
		return string(value), err
	}
	for _, ref := range state.Disabled {
		if !sections[ref.Section] {
			continue
		}
		value, err := marshal()
		if err != nil {
			return "", err
		}
		value, err = ReconcilePresetInboundReferences(engine, value, ref.Tag, "")
		if err != nil {
			return "", err
		}
		root, err = managedDocument(engine, value)
		if err != nil {
			return "", err
		}
	}
	for _, ref := range state.Disabled {
		if sections[ref.Section] {
			continue
		}
		values := root[ref.Section].([]any)
		// The first Xray outbound is an implicit default. sing-box may also use
		// its first outbound when route.final is absent. No silent reassignment.
		implicitDefault := engine == core.EngineXray || engine == core.EngineSingBox && stringValue(mapValue(root["route"])["final"]) == ""
		if implicitDefault && ref.Section == "outbounds" && len(values) > 0 && managedTag(engine, values[0]) == ref.Tag {
			return "", errors.New("默认出站仍被使用，请先明确调整默认出口")
		}
		kept := make([]any, 0, len(values))
		for _, value := range values {
			if managedTag(engine, value) != ref.Tag {
				kept = append(kept, value)
			}
		}
		root[ref.Section] = kept
	}
	for _, ref := range state.Disabled {
		if sections[ref.Section] {
			continue
		}
		if managedReferences(root, ref.Tag) {
			return "", fmt.Errorf("出站 %s 仍被引用，请先调整关联", ref.Tag)
		}
	}
	result, err := marshal()
	if err != nil {
		return "", err
	}
	// json.Number is retained above, so unrelated integer tokens never pass
	// through float64. Avoid reformatting source at all when no state applies.
	if strings.TrimSpace(result) == "" {
		return "", errors.New("运行配置为空")
	}
	return result, nil
}

// ManagedObject is a projection of one source object. A tag is scoped to its
// section and configuration revision; a port is presentation, never identity.
type ManagedObject struct {
	Section string `json:"section"`
	Tag     string `json:"tag"`
	Kind    string `json:"kind"`
	Listen  string `json:"listen,omitempty"`
	Port    int    `json:"port,omitempty"`
	Network string `json:"network,omitempty"`
	Inbound bool   `json:"inbound"`
	Enabled bool   `json:"enabled"`
}

func ManagedObjects(engine core.Engine, content string, state ManagedState) ([]ManagedObject, error) {
	root, err := managedDocument(engine, content)
	if err != nil {
		return nil, err
	}
	disabled := map[ManagedResource]bool{}
	for _, ref := range state.Disabled {
		disabled[ref] = true
	}
	result := []ManagedObject{}
	for _, section := range []string{"inbounds", "endpoints", "listeners", "servers", "shadowsocks", "outbounds", "proxies", "proxy-groups"} {
		inbound, ok := managedSections(engine)[section]
		if !ok {
			continue
		}
		values, _ := root[section].([]any)
		if engine == core.EngineShadowsocksRust && section == "servers" && root["server_port"] != nil {
			values = []any{root}
		}
		for index, value := range values {
			object := mapValue(value)
			tag := managedTag(engine, value)
			if engine == core.EngineShadowsocksRust && root["server_port"] == nil {
				tag = shadowsocksRustEntryTag(object, index)
			}
			if tag == "" || strings.HasPrefix(tag, accountingPrefix) || strings.HasPrefix(tag, "qch-stat-") {
				continue
			}
			if engine == core.EngineXray && inbound && xrayInternalAPIInbound(root, object) {
				continue
			}
			kind, listen, port, network := stringValue(object["type"]), stringValue(object["listen"]), trafficPortNumber(object["listen_port"]), ""
			switch engine {
			case core.EngineXray:
				kind = stringValue(object["protocol"])
				port = trafficPortNumber(object["port"])
			case core.EngineMihomo:
				port = trafficPortNumber(object["port"])
			case core.EngineShadowsocksRust:
				kind = "shadowsocks"
				listen = stringValue(object["server"])
				port = trafficPortNumber(object["server_port"])
				network = stringValue(object["mode"])
				if network == "" {
					network = stringValue(root["mode"])
				}
				if network == "" {
					network = "tcp_only"
				}
			}
			if inbound && engine != core.EngineShadowsocksRust {
				// Unknown protocols remain unspecified instead of claiming TCP/UDP.
				network = string(trafficEntryProtocol(engine, object, kind, ""))
			}
			switch network {
			case "tcp", "tcp_only":
				network = "TCP"
			case "udp", "udp_only":
				network = "UDP"
			case "both", "tcp_and_udp":
				network = "TCP / UDP"
			}
			if !inbound {
				listen = ""
				port = 0
				network = ""
			}
			result = append(result, ManagedObject{Section: section, Tag: tag, Kind: kind, Listen: listen, Port: port, Network: network, Inbound: inbound, Enabled: !disabled[ManagedResource{section, tag}]})
		}
	}
	return result, nil
}

// Legacy SS Rust lists can name entries by position. An unlabelled disabled
// entry must not silently move to a different server after a source edit.
// Explicitly retaining its projected id makes subsequent edits unambiguous.
func ValidateManagedSourceUpdate(engine core.Engine, previous, next string, state ManagedState) error {
	if engine != core.EngineShadowsocksRust || len(state.Disabled) == 0 {
		return nil
	}
	before, err := managedDocument(engine, previous)
	if err != nil {
		return err
	}
	after, err := managedDocument(engine, next)
	if err != nil {
		return err
	}
	for _, ref := range state.Disabled {
		oldValues, _ := before[ref.Section].([]any)
		newValues, _ := after[ref.Section].([]any)
		for index, oldValue := range oldValues {
			old := mapValue(oldValue)
			if shadowsocksRustEntryTag(old, index) != ref.Tag || managedExplicitSSRustTag(old) {
				continue
			}
			pinned := false
			for _, value := range newValues {
				entry := mapValue(value)
				if managedExplicitSSRustTag(entry) && shadowsocksRustSingleTag(entry) == ref.Tag {
					pinned = true
					break
				}
			}
			if pinned {
				continue
			}
			if index < len(newValues) {
				oldJSON, _ := json.Marshal(oldValue)
				newJSON, _ := json.Marshal(newValues[index])
				if string(oldJSON) == string(newJSON) {
					continue
				}
			}
			return fmt.Errorf("停用入站 %s 使用位置标识，请保留同名 id 后再调整源码", ref.Tag)
		}
	}
	return nil
}

func managedExplicitSSRustTag(object map[string]any) bool {
	for _, key := range []string{"id", "remarks", "name"} {
		if strings.TrimSpace(stringValue(object[key])) != "" {
			return true
		}
	}
	return false
}
