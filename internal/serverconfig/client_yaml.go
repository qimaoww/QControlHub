package serverconfig

import (
	"strings"

	"gopkg.in/yaml.v3"
)

func marshalSingleLineYAML(value map[string]any) (string, error) {
	encoded, err := yaml.Marshal(value)
	if err != nil {
		return "", err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(encoded, &document); err != nil {
		return "", err
	}
	setYAMLFlowStyle(&document)
	encoded, err = yaml.Marshal(&document)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(encoded)), nil
}

func setYAMLFlowStyle(node *yaml.Node) {
	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode {
		node.Style |= yaml.FlowStyle
	}
	for _, child := range node.Content {
		setYAMLFlowStyle(child)
	}
}
