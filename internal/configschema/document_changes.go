package configschema

import (
	"bytes"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

// ApplyDocumentChanges patches a native document with a transformation's
// semantic changes, retaining unowned nodes, comments, aliases and key order.
func ApplyDocumentChanges(engine core.Engine, content, before, after string) (string, error) {
	if engine != core.EngineMihomo {
		return ApplyJSONChanges(content, before, after)
	}
	var original, previous, next yaml.Node
	for _, pair := range []struct {
		text string
		node *yaml.Node
	}{{content, &original}, {before, &previous}, {after, &next}} {
		if err := yaml.Unmarshal([]byte(pair.text), pair.node); err != nil {
			return "", err
		}
		if _, err := yamlRootNode(pair.node); err != nil {
			return "", err
		}
	}
	if yamlValueEqual(previous.Content[0], next.Content[0]) {
		return content, nil
	}
	applyYAMLChanges(original.Content[0], previous.Content[0], next.Content[0])
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&original); err != nil {
		return "", err
	}
	_ = encoder.Close()
	return output.String(), nil
}

func yamlValueEqual(left, right *yaml.Node) bool {
	if left.Kind != right.Kind || left.Tag != right.Tag || left.Value != right.Value || len(left.Content) != len(right.Content) {
		return false
	}
	if left.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(left.Content); i += 2 {
			_, value := yamlMapEntry(right, left.Content[i].Value)
			if value == nil || !yamlValueEqual(left.Content[i+1], value) {
				return false
			}
		}
		return true
	}
	for i := range left.Content {
		if !yamlValueEqual(left.Content[i], right.Content[i]) {
			return false
		}
	}
	return true
}

func applyYAMLChanges(current, before, after *yaml.Node) {
	if yamlValueEqual(before, after) {
		return
	}
	if current.Kind == yaml.MappingNode && before.Kind == yaml.MappingNode && after.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(before.Content); i += 2 {
			key := before.Content[i].Value
			if _, value := yamlMapEntry(after, key); value == nil {
				if index, value := yamlMapEntry(current, key); value != nil {
					current.Content = append(current.Content[:index], current.Content[index+2:]...)
				}
			}
		}
		for i := 0; i+1 < len(after.Content); i += 2 {
			key, value := after.Content[i], after.Content[i+1]
			_, original := yamlMapEntry(current, key.Value)
			_, previous := yamlMapEntry(before, key.Value)
			if previous != nil && original != nil {
				applyYAMLChanges(original, previous, value)
			} else if original != nil {
				replaceYAMLValue(original, value)
			} else {
				current.Content = append(current.Content, key, value)
			}
		}
		return
	}
	if current.Kind == yaml.SequenceNode && before.Kind == yaml.SequenceNode && after.Kind == yaml.SequenceNode {
		var values []*yaml.Node
		for index, value := range after.Content {
			prior := index
			if len(before.Content) != len(after.Content) {
				prior = -1
				identity := yamlIdentity(value)
				for candidate, previous := range before.Content {
					if identity != "" && yamlIdentity(previous) == identity {
						prior = candidate
						break
					}
				}
			}
			if prior >= 0 && prior < len(current.Content) && prior < len(before.Content) {
				original := current.Content[prior]
				applyYAMLChanges(original, before.Content[prior], value)
				values = append(values, original)
			} else {
				values = append(values, value)
			}
		}
		current.Content = values
		return
	}
	replaceYAMLValue(current, after)
}

func yamlIdentity(node *yaml.Node) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}
	for _, key := range []string{"tag", "name", "remarks"} {
		if _, value := yamlMapEntry(node, key); value != nil && strings.TrimSpace(value.Value) != "" {
			return value.Value
		}
	}
	return ""
}

func replaceYAMLValue(current, next *yaml.Node) {
	copy := *next
	copy.HeadComment, copy.LineComment, copy.FootComment = current.HeadComment, current.LineComment, current.FootComment
	copy.Anchor = current.Anchor
	*current = copy
}
