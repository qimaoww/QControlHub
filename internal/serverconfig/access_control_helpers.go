package serverconfig

import (
	"encoding/json"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

func mainlandInboundExists(engine core.Engine, root map[string]any, tag string, port int) bool {
	if engine == core.EngineShadowsocksRust {
		entries := ParseAll(engine, mustMarshalJSON(root))
		for _, input := range entries {
			// The single-service ssserver JSON format has no tag field; its
			// canonical synthetic tag is `ss-rust`. Accept the operator's
			// generated tag when there is only one service and keep the durable
			// firewall key stable by port.
			if input.Port == port && (input.Tag == tag || len(entries) == 1) {
				return true
			}
		}
		return false
	}
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

func mustMarshalJSON(root map[string]any) string {
	value, err := json.Marshal(root)
	if err != nil {
		return ""
	}
	return string(value)
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
