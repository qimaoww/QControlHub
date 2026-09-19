package serverconfig

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func mieruProtocol(base string) Protocol {
	return Protocol{
		Key: ProtocolMieru, Name: "Mieru", Badge: "MIERU",
		Description: "用户名与密码认证的 Mieru 服务端，可选 TCP 或 UDP 传输，无需 TLS 证书。",
		Docs:        base + "mieru/", DefaultPort: 8443, Credential: "用户密码",
		Transports: []string{"raw"},
	}
}

func normalizeMieruInput(input *Input) {
	if input.MieruTransport == "" {
		input.MieruTransport = "TCP"
	}
}

func validateMieruInput(input Input) error {
	if input.MieruTransport != "TCP" && input.MieruTransport != "UDP" {
		return errors.New("Mieru 传输必须是 TCP 或 UDP")
	}
	if strings.TrimSpace(input.Username) == "" || len(input.Username) > 64 {
		return errors.New("Mieru 用户名不能为空且不能超过 64 个字符")
	}
	if input.Transport != "" && input.Transport != "raw" || input.TransportPath != "" || input.TLSEnabled || input.RealityEnabled {
		return errors.New("Mieru 使用原生 TCP/UDP 传输，不支持额外的 TLS、Reality 或传输路径")
	}
	return validateListenerOptions(input)
}

func configureMieruListener(listener map[string]any, input Input) {
	listener["type"] = "mieru"
	listener["transport"] = input.MieruTransport
	listener["users"] = map[string]any{input.Username: input.Credential}
}

func parseMieruListener(listener map[string]any, input *Input) bool {
	// Only expose the single-user preset shape. Advanced native settings
	// cannot be represented by this form or its matching client export.
	for key := range listener {
		switch key {
		case "name", "type", "listen", "port", "transport", "users", "routing-mark", "rule", "proxy":
		default:
			return false
		}
	}
	port, numericPort := listener["port"].(int)
	if !numericPort || port < 1 || port > 65535 {
		return false
	}
	users := mapValue(listener["users"])
	if len(users) != 1 {
		return false
	}
	for name, password := range users {
		input.Username, input.Credential = name, stringValue(password)
	}
	input.MieruTransport = stringValue(listener["transport"])
	return validateMieruInput(*input) == nil && validateCredential(*input) == nil
}

func buildMieruMihomoYAML(input Input, address, name string) (string, error) {
	return marshalSingleLineYAML(map[string]any{
		"name": name, "type": "mieru", "server": address, "port": input.Port,
		"transport": input.MieruTransport, "username": input.Username,
		"password": input.Credential, "udp": true,
	})
}

// Recheck the saved listener at the mutation boundary: source-only listeners
// must not lose extra users or native options through a direct preset request.
func validateMieruPresetMutation(content, tag string) error {
	var root map[string]any
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return err
	}
	listeners, _ := root["listeners"].([]any)
	for _, raw := range listeners {
		listener := mapValue(raw)
		if stringValue(listener["type"]) != ProtocolMieru || stringValue(listener["name"]) != tag {
			continue
		}
		input := Input{Protocol: ProtocolMieru}
		if !parseMieruListener(listener, &input) {
			return fmt.Errorf("Mieru 入站 %q 包含自定义字段或多用户配置，请使用完整源码编辑，避免覆盖", tag)
		}
	}
	return nil
}
