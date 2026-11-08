package serverconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// Rewrite only native outbound selectors. Labels, credentials, domains and
// inbound identities with the same text are never references.
func RewriteOutboundReferences(engine core.Engine, source, previous, next string) (string, error) {
	if previous == "" || next == "" || previous == next {
		return source, nil
	}
	rewriteString := func(value string) string {
		parts := strings.Split(value, ",")
		if len(parts) < 2 || strings.TrimSpace(parts[0]) == "SUB-RULE" {
			return value
		}
		index := len(parts) - 1
		if strings.TrimSpace(parts[index]) == "no-resolve" {
			index--
		}
		if index > 0 && strings.TrimSpace(parts[index]) == previous {
			parts[index] = next
			return strings.Join(parts, ",")
		}
		return value
	}
	reference := func(key string) bool {
		switch key {
		case "outbound", "outboundTag", "detour", "final", "proxy", "dialer-proxy", "proxy-server-nameserver", "dialerProxy", "fallbackTag", "default-dialer-proxy":
			return true
		}
		return false
	}
	// YAML nodes preserve comments, anchors and integer tokens in untouched fields.
	if engine == core.EngineMihomo {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(source), &doc); err != nil {
			return "", err
		}
		var visit func(*yaml.Node, string)
		visit = func(n *yaml.Node, key string) {
			if n.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(n.Content); i += 2 {
					childKey := n.Content[i].Value
					if key == "sub-rules" {
						childKey = "sub-rules"
					}
					visit(n.Content[i+1], childKey)
				}
			} else if n.Kind == yaml.SequenceNode {
				for _, child := range n.Content {
					visit(child, key)
				}
			} else if n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
				if (reference(key) || key == "proxies") && n.Value == previous {
					n.Value = next
				}
				if key == "rules" || key == "sub-rules" {
					n.Value = rewriteString(n.Value)
				}
			} else if n.Kind == yaml.DocumentNode {
				for _, child := range n.Content {
					visit(child, key)
				}
			}
		}
		visit(&doc, "")
		encoded, err := yaml.Marshal(&doc)
		return string(encoded), err
	}
	root, err := managedDocument(engine, source)
	if err != nil {
		return "", err
	}
	outbounds, _ := root["outbounds"].([]any)
	var visit func(any, string) (any, error)
	visit = func(value any, key string) (any, error) {
		switch v := value.(type) {
		case map[string]any:
			for k, item := range v {
				if k == "proxySettings" {
					if object := mapValue(item); stringValue(object["tag"]) == previous {
						object["tag"] = next
					}
				}
				changed, err := visit(item, k)
				if err != nil {
					return nil, err
				}
				v[k] = changed
			}
		case []any:
			if key == "selector" && engine == core.EngineXray {
				matched := false
				for _, item := range v {
					if prefix, ok := item.(string); ok && strings.HasPrefix(previous, prefix) {
						matched = true
					}
				}
				if matched {
					desired := map[string]bool{next: true}
					for _, out := range outbounds {
						tag := managedTag(engine, out)
						if tag == previous {
							continue
						}
						for _, item := range v {
							if prefix, ok := item.(string); ok && strings.HasPrefix(tag, prefix) {
								desired[tag] = true
							}
						}
					}
					selectors := []any{}
					seen := map[string]bool{}
					for _, out := range outbounds {
						tag := managedTag(engine, out)
						if !desired[tag] {
							continue
						}
						for _, other := range outbounds {
							candidate := managedTag(engine, other)
							if candidate != previous && strings.HasPrefix(candidate, tag) && !desired[candidate] {
								return nil, fmt.Errorf("新名称 %s 会扩大负载均衡选择范围，请使用不重叠的名称", next)
							}
						}
						selectors = append(selectors, tag)
						seen[tag] = true
					}
					if !seen[next] {
						selectors = append(selectors, next)
					}
					return selectors, nil
				}
			}
			for i, item := range v {
				changed, err := visit(item, key)
				if err != nil {
					return nil, err
				}
				v[i] = changed
			}
		case string:
			if (reference(key) || key == "outbounds") && v == previous {
				return next, nil
			}
		}
		return value, nil
	}
	if _, err = visit(root, ""); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(root)
	return string(encoded), err
}

// Replacement is always explicitly selected by the caller. It is validated
// against the enabled source and keeps the implicit default at the same slot.
func ReplaceManagedOutboundReferences(engine core.Engine, source, previous, next string, state ManagedState) (string, error) {
	if previous == next || next == "" {
		return "", fmt.Errorf("请选择其他已启用出站")
	}
	objects, err := ManagedObjects(engine, source, state)
	if err != nil {
		return "", err
	}
	found := false
	for _, object := range objects {
		if !object.Inbound && object.Enabled && object.Tag == next {
			found = true
		}
	}
	if !found {
		return "", fmt.Errorf("替代出站不存在或已停用")
	}
	result, err := RewriteOutboundReferences(engine, source, previous, next)
	if err != nil {
		return "", err
	}
	if engine != core.EngineMihomo {
		root, err := managedDocument(engine, result)
		if err != nil {
			return "", err
		}
		values, _ := root["outbounds"].([]any)
		implicit := engine == core.EngineXray || stringValue(mapValue(root["route"])["final"]) == ""
		if implicit && len(values) > 0 && managedTag(engine, values[0]) == previous {
			for i, value := range values {
				if managedTag(engine, value) == next {
					values = append([]any{value}, append(values[:i], values[i+1:]...)...)
					break
				}
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				return "", err
			}
			result, err = configschema.MergeFragment(engine, result, "outbounds", string(encoded), false)
			if err != nil {
				return "", err
			}
		}
	}
	return result, nil
}
