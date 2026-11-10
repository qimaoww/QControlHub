package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v4"
)

// PreserveRedactedConfig restores only unchanged placeholders at the original
// structural location. Callers must compare the original resource version in
// the transaction that saves the result. Placeholders never become credentials.
func PreserveRedactedConfig(original, edited string) (string, error) {
	if !strings.Contains(edited, "[redacted") {
		return edited, nil
	}
	redacted, ok := RedactDiagnosticValue(original).(string)
	if !ok {
		return "", fmt.Errorf("无法读取脱敏配置")
	}
	var before, masked, after yaml.Node
	for _, v := range []struct {
		value  string
		target *yaml.Node
	}{{original, &before}, {redacted, &masked}, {edited, &after}} {
		if err := yaml.Unmarshal([]byte(v.value), v.target); err != nil {
			return "", fmt.Errorf("脱敏配置须为有效 JSON 或 YAML")
		}
	}
	if len(before.Content) != 1 || len(masked.Content) != 1 || len(after.Content) != 1 {
		return "", fmt.Errorf("配置为空")
	}
	if err := restoreRedactedNode(before.Content[0], masked.Content[0], after.Content[0]); err != nil {
		return "", err
	}
	data, err := yaml.Marshal(&after)
	// Preserve the JSON format required by Xray, sing-box and ss-rust.
	if strings.HasPrefix(strings.TrimSpace(edited), "{") || strings.HasPrefix(strings.TrimSpace(edited), "[") {
		var value any
		if err = yaml.Unmarshal(data, &value); err == nil {
			data, err = json.Marshal(value)
		}
	}
	if err != nil {
		return "", err
	}
	if strings.Contains(string(data), "[redacted") {
		return "", fmt.Errorf("无法安全保留脱敏值；请重新读取并使用原位置或明确替换")
	}
	return string(data), nil
}
func restoreRedactedNode(original, masked, edited *yaml.Node) error {
	if !nodeHasRedaction(edited) {
		return nil
	}
	if original.Kind != edited.Kind || masked.Kind != edited.Kind {
		return fmt.Errorf("脱敏字段结构已变更，请明确替换敏感值")
	}
	switch edited.Kind {
	case yaml.ScalarNode:
		if edited.Value != masked.Value || !strings.Contains(masked.Value, "[redacted") {
			return fmt.Errorf("脱敏占位符不能作为新值保存")
		}
		*edited = *original
	case yaml.MappingNode:
		for i := 0; i < len(edited.Content); i += 2 {
			key := edited.Content[i].Value
			originalKey := key
			if strings.Contains(key, "[redacted") {
				originalKey = ""
				for j := 0; j < len(original.Content); j += 2 {
					if diagnosticUserKey(original.Content[j].Value) == key {
						originalKey = original.Content[j].Value
						break
					}
				}
				if originalKey == "" || mappingValue(masked, key) == nil {
					return fmt.Errorf("脱敏字典标识不能移动或替换；请提供明确凭据")
				}
				edited.Content[i].Value = originalKey
			}
			o, m := mappingValue(original, originalKey), mappingValue(masked, key)
			if nodeHasRedaction(edited.Content[i+1]) {
				if o == nil || m == nil {
					return fmt.Errorf("新增字段不能使用脱敏占位符")
				}
				if err := restoreRedactedNode(o, m, edited.Content[i+1]); err != nil {
					return err
				}
			}
		}
	case yaml.SequenceNode:
		if len(original.Content) != len(edited.Content) || len(masked.Content) != len(edited.Content) {
			return fmt.Errorf("含脱敏值的列表长度已变更；请明确替换敏感值")
		}
		for i, candidate := range edited.Content {
			if !nodeHasRedaction(candidate) {
				continue
			}
			identified := false
			for _, key := range []string{"tag", "name", "id", "username"} {
				o, m, c := mappingValue(original.Content[i], key), mappingValue(masked.Content[i], key), mappingValue(candidate, key)
				if o != nil && m != nil && !nodeHasRedaction(m) {
					if c == nil || c.Value != o.Value {
						return fmt.Errorf("含脱敏值的列表对象标识已变更")
					}
					identified = true
				}
			}
			if !identified {
				a, _ := yaml.Marshal(masked.Content[i])
				b, _ := yaml.Marshal(candidate)
				if string(a) != string(b) {
					return fmt.Errorf("含脱敏值的匿名列表项已变更；请明确替换敏感值")
				}
			}
			if err := restoreRedactedNode(original.Content[i], masked.Content[i], candidate); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("脱敏配置不支持别名或复杂节点")
	}
	return nil
}
func nodeHasRedaction(n *yaml.Node) bool {
	if strings.Contains(n.Value, "[redacted") {
		return true
	}
	for _, child := range n.Content {
		if nodeHasRedaction(child) {
			return true
		}
	}
	return false
}
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
