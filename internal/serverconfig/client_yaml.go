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
	// Lead with node identity and connection details instead of map-key order.
	// Reorder only the root; nested protocol options retain their own structure.
	mapping := document.Content[0]
	ordered := make([]*yaml.Node, 0, len(mapping.Content))
	remaining := append([]*yaml.Node(nil), mapping.Content...)
	for _, key := range []string{
		"name", "type", "server", "port", "uuid", "username", "cipher", "password", "psk", "key",
		"alterId", "encryption", "flow", "udp", "tls", "servername", "sni", "client-fingerprint",
		"skip-cert-verify", "alpn", "network", "transport",
	} {
		for index := 0; index < len(remaining); index += 2 {
			if remaining[index].Value == key {
				ordered = append(ordered, remaining[index:index+2]...)
				remaining = append(remaining[:index], remaining[index+2:]...)
				break
			}
		}
	}
	mapping.Content = append(ordered, remaining...)
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
