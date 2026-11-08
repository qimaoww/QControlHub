package serverconfig

import (
	"encoding/json"
	"fmt"
	"go.yaml.in/yaml/v4"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Metadata cannot turn an edited public plaintext listener into a managed one.
// Bind the proxy to the native listener, credential, host and path at this version.
func standaloneXHTTPFrontends(engine core.Engine, content string, metadata map[string]string) ([]core.TLSFrontend, error) {
	var root map[string]any
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return nil, err
	}
	key, tagField := "inbounds", "tag"
	if engine == core.EngineMihomo {
		key, tagField = "listeners", "name"
	}
	raw, _ := root[key].([]any)
	inputs := ParseAll(engine, content)
	var result []core.TLSFrontend
	for tag, value := range metadata {
		var m clientMetadata
		if json.Unmarshal([]byte(value), &m) != nil || m.Protocol != ProtocolXHTTPTLS || m.Frontend == nil {
			continue
		}
		f := *m.Frontend
		found := false
		for _, input := range inputs {
			if input.Tag != tag {
				if input.Port == f.BackendPort {
					return nil, fmt.Errorf("请通过预设修改 XHTTP TLS 入站名称，以保留资源归属")
				}
				continue
			}
			found = true
			if engine == core.EngineMihomo {
				enabled := false
				for _, item := range raw {
					listener := mapValue(item)
					if stringValue(listener[tagField]) == tag {
						enabled = boolValue(listener["allow-insecure"])
					}
				}
				if !enabled {
					return nil, fmt.Errorf("入站 %s 未启用 Mihomo 明文回源，请通过预设修复", tag)
				}
			}
			if m.XHTTP == nil || m.XHTTP.Binding != xhttpBinding(input) || input.Listen != "127.0.0.1" || input.Port != f.BackendPort || input.Transport != "xhttp" || input.TransportPath != f.Path || input.TransportHost != f.Host || input.TLSEnabled || input.RealityEnabled || f.LocalOnly || f.Tag != tag {
				return nil, fmt.Errorf("入站 %s 的 XHTTP TLS 受管关系已变化，请通过预设修改", tag)
			}
			if err := f.Validate(); err != nil {
				return nil, err
			}
			result = append(result, f)
		}
		// A removed inbound releases only its own frontend. An unparseable inbound
		// with the same tag must not silently detach a proxy.
		if !found {
			for _, item := range raw {
				p := mapValue(item)
				if stringValue(p[tagField]) == tag || intValue(p["port"]) == f.BackendPort {
					return nil, fmt.Errorf("XHTTP TLS 入站 %s 无法解析，请通过预设修复", tag)
				}
			}
		}
	}
	return result, nil
}
