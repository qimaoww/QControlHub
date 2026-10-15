package serverconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Recognize an entire graph, never a tag prefix alone. Every dependency must
// point at the same loopback XHTTP instance before it can be edited as a group.
func xhttpGroup(root map[string]any, primary map[string]any) (map[string]any, int) {
	if primary["protocol"] != "vless" {
		return nil, 0
	}
	stream := mapValue(primary["streamSettings"])
	if stream["security"] != "reality" || stream["network"] != "raw" {
		return nil, 0
	}
	host, target, err := net.SplitHostPort(stringValue(mapValue(stream["realitySettings"])["target"]))
	tlsPort, _ := strconv.Atoi(target)
	if err != nil || host != "127.0.0.1" || tlsPort < 1024 {
		return nil, 0
	}
	fallbacks, _ := mapValue(primary["settings"])["fallbacks"].([]any)
	clients, _ := mapValue(primary["settings"])["clients"].([]any)
	if len(fallbacks) != 1 || len(clients) > 1 {
		return nil, 0
	}
	f := mapValue(fallbacks[0])
	if len(f) != 2 || intValue(f["xver"]) != 0 {
		return nil, 0
	}
	entries, _ := root["inbounds"].([]any)
	for _, raw := range entries {
		b := mapValue(raw)
		if b["tag"] != stringValue(primary["tag"])+".xh" || b["listen"] != "127.0.0.1" || b["protocol"] != "vless" || f["dest"] != net.JoinHostPort("127.0.0.1", strconv.Itoa(intValue(b["port"]))) {
			continue
		}
		s := mapValue(b["streamSettings"])
		if s["network"] != "xhttp" || s["security"] != "none" || mapValue(b["settings"])["decryption"] != "none" {
			return nil, 0
		}
		users, _ := mapValue(b["settings"])["clients"].([]any)
		if len(users) != 1 {
			return nil, 0
		}
		user := mapValue(users[0])
		if stringValue(user["flow"]) != "" {
			return nil, 0
		}
		if len(clients) == 1 {
			vision := mapValue(clients[0])
			if vision["id"] != user["id"] || vision["flow"] != "xtls-rprx-vision" || vision["email"] != user["email"] || intValue(vision["level"]) != intValue(user["level"]) {
				return nil, 0
			}
		}
		return b, tlsPort
	}
	return nil, 0
}

func xhttpGroupOwner(root map[string]any, tag string) string {
	entries, _ := root["inbounds"].([]any)
	for _, raw := range entries {
		p := mapValue(raw)
		if b, _ := xhttpGroup(root, p); b != nil && b["tag"] == tag {
			return stringValue(p["tag"])
		}
	}
	return ""
}

func parseXHTTPGroup(root map[string]any, primary map[string]any) (Input, bool) {
	backend, tlsPort := xhttpGroup(root, primary)
	if backend == nil {
		return Input{}, false
	}
	synthetic := cloneAccountingObject(backend)
	synthetic["tag"], synthetic["listen"], synthetic["port"] = primary["tag"], primary["listen"], primary["port"]
	stream := mapValue(synthetic["streamSettings"])
	stream["security"] = "reality"
	stream["realitySettings"] = mapValue(primary["streamSettings"])["realitySettings"]
	data, _ := json.Marshal(map[string]any{"inbounds": []any{synthetic}})
	input, ok := parseXray(string(data))
	if !ok {
		return Input{}, false
	}
	input.Protocol = ProtocolXHTTPTLSReality
	if u := firstMap(mapValue(primary["settings"])["clients"]); u != nil {
		if u["id"] != input.Credential || u["flow"] != "xtls-rprx-vision" {
			return Input{}, false
		}
		input.Protocol = ProtocolXHTTPFive
	}
	input.XHTTPBackendPort, input.XHTTPTLSPort = intValue(backend["port"]), tlsPort
	input.TLSALPN = "h2"
	input.RealityTarget = "" // The local target is derived exclusively from the managed graph.
	input.BlockMainlandDestination, input.BlockMainlandSource = mainlandXrayFlags(root, input.Tag)
	return input, true
}

func mutateXHTTPGroup(current, generated, tag, operation string) (string, bool, error) {
	var old, next map[string]any
	if json.Unmarshal([]byte(current), &old) != nil || json.Unmarshal([]byte(generated), &next) != nil {
		return "", false, nil
	}
	oldItems, _ := old["inbounds"].([]any)
	newPrimary := firstMap(next["inbounds"])
	var oldBackend map[string]any
	for _, raw := range oldItems {
		p := mapValue(raw)
		if p["tag"] == tag {
			oldBackend, _ = xhttpGroup(old, p)
		}
	}
	newBackend, _ := xhttpGroup(next, newPrimary)
	if owner := xhttpGroupOwner(old, tag); owner != "" {
		return "", true, fmt.Errorf("内部入站由 %s 维护，请编辑整个组合", owner)
	}
	if oldBackend == nil && newBackend == nil {
		return "", false, nil
	}
	if oldBackend != nil {
		if err := applyMainlandXray(old, stringValue(oldBackend["tag"]), false, false, nil); err != nil {
			return "", true, err
		}
	}
	filtered := []any{}
	for _, raw := range oldItems {
		p := mapValue(raw)
		if oldBackend == nil || p["tag"] != oldBackend["tag"] {
			filtered = append(filtered, raw)
		}
	}
	old["inbounds"] = filtered
	value, _ := json.Marshal(old)
	keys := []string{"tag", "listen", "port", "protocol", "settings", "streamSettings"}
	primaryDocument, _ := json.Marshal(map[string]any{"inbounds": []any{newPrimary}})
	merged, err := configschema.MutateListItem(core.EngineXray, string(value), string(primaryDocument), "inbounds", "tag", tag, operation, keys...)
	if err != nil {
		return "", true, err
	}
	if oldBackend != nil {
		nextTag := ""
		if operation != "delete" && newBackend != nil {
			nextTag = stringValue(newBackend["tag"])
		}
		merged, err = ReconcilePresetInboundReferences(core.EngineXray, merged, stringValue(oldBackend["tag"]), nextTag)
		if err != nil {
			return "", true, err
		}
	}
	if operation != "delete" && newBackend != nil {
		var root map[string]any
		_ = json.Unmarshal([]byte(merged), &root)
		entries, _ := root["inbounds"].([]any)
		paired := []any{}
		for _, raw := range entries {
			paired = append(paired, raw)
			if mapValue(raw)["tag"] == newPrimary["tag"] {
				paired = append(paired, newBackend)
			}
		}
		root["inbounds"] = paired
		destination, source := mainlandXrayFlags(next, stringValue(newPrimary["tag"]))
		if err := applyMainlandXray(root, stringValue(newBackend["tag"]), destination, source, nil); err != nil {
			return "", true, err
		}
		value, marshalErr := json.MarshalIndent(root, "", "  ")
		merged, err = string(value)+"\n", marshalErr
	}
	if err == nil {
		err = validateXHTTPGroupPorts(merged)
	}
	return merged, true, err
}

func validateXHTTPGroupPorts(content string) error {
	var root map[string]any
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return err
	}
	ports := map[int]string{}
	entries, _ := root["inbounds"].([]any)
	for _, raw := range entries {
		p := mapValue(raw)
		port := intValue(p["port"])
		if port == 0 {
			continue
		}
		if owner := ports[port]; owner != "" {
			return fmt.Errorf("监听端口 %d 在 %s 与 %s 之间冲突，请重新分配端口", port, owner, p["tag"])
		}
		ports[port] = stringValue(p["tag"])
	}
	for _, raw := range entries {
		p := mapValue(raw)
		if b, tlsPort := xhttpGroup(root, p); b != nil {
			if owner := ports[tlsPort]; owner != "" {
				return fmt.Errorf("本地 TLS 目标端口 %d 与 %s 冲突", tlsPort, owner)
			}
			ports[tlsPort] = stringValue(p["tag"]) + " TLS"
		}
	}
	return nil
}

func xhttpFrontend(input Input) core.TLSFrontend {
	return core.TLSFrontend{Tag: input.Tag, Mode: "nginx", Domain: input.TLSServerName, PublicPort: input.XHTTPTLSPort, BackendPort: input.XHTTPBackendPort, Transport: "xhttp", Path: input.TransportPath, LocalOnly: true, RealityName: input.RealityServerName, RealityNames: xhttpRealityNames(input), CertificatePath: input.CertificatePath, PrivateKeyPath: input.PrivateKeyPath}
}

func xhttpManagedFrontends(content string, metadata map[string]string) ([]core.TLSFrontend, error) {
	if err := validateXHTTPGroupPorts(content); err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return nil, err
	}
	var result []core.TLSFrontend
	entries, _ := root["inbounds"].([]any)
	for _, raw := range entries {
		p := mapValue(raw)
		input, ok := parseXHTTPGroup(root, p)
		if !ok {
			continue
		}
		var m clientMetadata
		if json.Unmarshal([]byte(metadata[input.Tag]), &m) != nil || m.XHTTP == nil || !isXHTTPGroup(m.Protocol) {
			return nil, fmt.Errorf("组合 %s 缺少版本关联的证书 / 入口元数据，请通过预设重新保存", input.Tag)
		}
		if err := ApplyClientMetadata(&input, metadata[input.Tag]); err != nil {
			return nil, err
		}
		if input.TLSServerName == "" {
			return nil, fmt.Errorf("组合 %s 的源码与受管关系已变化，请通过预设修复后部署", input.Tag)
		}
		f := xhttpFrontend(input)
		if err := f.Validate(); err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	for tag, value := range metadata {
		var m clientMetadata
		if json.Unmarshal([]byte(value), &m) != nil || !isXHTTPGroup(m.Protocol) {
			continue
		}
		for _, raw := range entries {
			if mapValue(raw)["tag"] == tag {
				found := false
				for _, f := range result {
					found = found || f.Tag == tag
				}
				if !found {
					return nil, fmt.Errorf("组合 %s 的回落或本地目标已脱离受管关系，请使用预设修改", tag)
				}
			}
		}
	}
	return result, nil
}

func xhttpGroupRouteTags(root map[string]any, tag string) []string {
	entries, _ := root["inbounds"].([]any)
	for _, raw := range entries {
		p := mapValue(raw)
		if p["tag"] == tag {
			if b, _ := xhttpGroup(root, p); b != nil {
				return []string{tag, stringValue(b["tag"])}
			}
		}
	}
	return []string{tag}
}

func xhttpGroupTarget(input Input) bool {
	return isXHTTPGroup(input.Protocol) && strings.HasPrefix(input.TransportPath, "/")
}
