package serverconfig

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

const maxDiscoveredTrafficPorts = 256

// DiscoverTrafficPorts extracts listening ports without requiring the
// configuration to use one of QControlHub's generated protocol recipes. No
// credentials or other configuration fields leave this function.
func DiscoverTrafficPorts(engine core.Engine, content string) []core.PortTrafficEndpoint {
	root := decodeTrafficConfiguration(engine, content)
	if root == nil {
		return nil
	}
	var result []core.PortTrafficEndpoint
	switch engine {
	case core.EngineMihomo:
		result = discoverTrafficList(root["listeners"], engine, "name", "type", "port", core.TrafficProtocolBoth)
		for _, field := range []struct {
			key      string
			name     string
			protocol core.TrafficProtocol
		}{
			{key: "port", name: "HTTP proxy", protocol: core.TrafficProtocolTCP},
			{key: "socks-port", name: "SOCKS proxy", protocol: core.TrafficProtocolBoth},
			{key: "mixed-port", name: "Mixed proxy", protocol: core.TrafficProtocolBoth},
			{key: "redir-port", name: "Redirect proxy", protocol: core.TrafficProtocolTCP},
			{key: "tproxy-port", name: "TProxy", protocol: core.TrafficProtocolBoth},
		} {
			if port := trafficPortNumber(root[field.key]); port > 0 {
				result = append(result, trafficEndpoint(engine, field.name, port, field.protocol))
			}
		}
	case core.EngineXray:
		result = discoverTrafficList(root["inbounds"], engine, "tag", "protocol", "port", core.TrafficProtocolBoth)
	case core.EngineSingBox:
		result = discoverTrafficList(root["inbounds"], engine, "tag", "type", "listen_port", core.TrafficProtocolBoth)
	case core.EngineShadowsocksRust:
		mode := trafficProtocol(root["mode"], core.TrafficProtocolBoth)
		if entries, ok := root["servers"].([]any); ok {
			for index, value := range entries {
				entry, _ := value.(map[string]any)
				if entry == nil {
					continue
				}
				port := trafficPortNumber(entry["server_port"])
				if port == 0 {
					continue
				}
				name := trafficEndpointName(entry["name"], entry["method"], "Shadowsocks "+strconv.Itoa(index+1))
				result = append(result, trafficEndpoint(engine, name, port, trafficProtocol(entry["mode"], mode)))
			}
		} else if port := trafficPortNumber(root["server_port"]); port > 0 {
			result = append(result, trafficEndpoint(engine, trafficEndpointName(root["name"], root["method"], "Shadowsocks"), port, mode))
		}
	}
	return normalizeTrafficEndpoints(result)
}

func decodeTrafficConfiguration(engine core.Engine, content string) map[string]any {
	var root map[string]any
	if engine == core.EngineMihomo {
		if yaml.Unmarshal([]byte(content), &root) != nil {
			return nil
		}
		return root
	}
	if json.Unmarshal([]byte(content), &root) == nil {
		return root
	}
	// Xray accepts YAML sources when started with a configuration directory.
	if engine == core.EngineXray && yaml.Unmarshal([]byte(content), &root) == nil {
		return root
	}
	return nil
}

func discoverTrafficList(value any, engine core.Engine, nameField, typeField, portField string, fallback core.TrafficProtocol) []core.PortTrafficEndpoint {
	entries, _ := value.([]any)
	result := make([]core.PortTrafficEndpoint, 0, len(entries))
	for _, value := range entries {
		entry, _ := value.(map[string]any)
		if entry == nil {
			continue
		}
		port := trafficPortNumber(entry[portField])
		if port == 0 {
			continue
		}
		kind := strings.TrimSpace(stringValue(entry[typeField]))
		name := trafficEndpointName(entry[nameField], kind, string(engine)+" :"+strconv.Itoa(port))
		protocol := trafficProtocol(entry["network"], trafficProtocolForKind(kind, fallback))
		if engine == core.EngineXray {
			if settings, ok := entry["settings"].(map[string]any); ok {
				protocol = trafficProtocol(settings["network"], protocol)
			}
		}
		result = append(result, trafficEndpoint(engine, name, port, protocol))
	}
	return result
}

func trafficEndpoint(engine core.Engine, name string, port int, protocol core.TrafficProtocol) core.PortTrafficEndpoint {
	return core.PortTrafficEndpoint{Name: truncateTrafficName(name), Engine: engine, Port: port, Protocol: protocol}
}

func trafficEndpointName(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(stringValue(value)); text != "" {
			return text
		}
	}
	return "Configured port"
}

func trafficPortNumber(value any) int {
	var port int
	switch typed := value.(type) {
	case int:
		port = typed
	case int64:
		port = int(typed)
	case float64:
		if typed == float64(int(typed)) {
			port = int(typed)
		}
	case string:
		port, _ = strconv.Atoi(strings.TrimSpace(typed))
	}
	if port < 1 || port > 65535 {
		return 0
	}
	return port
}

func trafficProtocol(value any, fallback core.TrafficProtocol) core.TrafficProtocol {
	var values []string
	switch typed := value.(type) {
	case string:
		values = strings.FieldsFunc(strings.ToLower(typed), func(character rune) bool {
			return character == ',' || character == '_' || character == '+' || character == ' '
		})
	case []any:
		for _, item := range typed {
			values = append(values, strings.ToLower(stringValue(item)))
		}
	case []string:
		for _, item := range typed {
			values = append(values, strings.ToLower(item))
		}
	}
	tcp, udp := false, false
	for _, value := range values {
		switch value {
		case "tcp":
			tcp = true
		case "udp":
			udp = true
		case "both", "all":
			tcp, udp = true, true
		}
	}
	if tcp && udp {
		return core.TrafficProtocolBoth
	}
	if tcp {
		return core.TrafficProtocolTCP
	}
	if udp {
		return core.TrafficProtocolUDP
	}
	return fallback
}

func trafficProtocolForKind(kind string, fallback core.TrafficProtocol) core.TrafficProtocol {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "hysteria2", "hy2", "tuic", "quic":
		return core.TrafficProtocolUDP
	case "vless", "vmess", "trojan", "anytls", "http", "httpupgrade":
		return core.TrafficProtocolTCP
	case "shadowsocks", "ss", "socks", "mixed", "direct", "dokodemo-door":
		return core.TrafficProtocolBoth
	default:
		return fallback
	}
}

func truncateTrafficName(value string) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= 100 {
		return value
	}
	runes := []rune(value)
	return string(runes[:100])
}

func normalizeTrafficEndpoints(endpoints []core.PortTrafficEndpoint) []core.PortTrafficEndpoint {
	seen := make(map[string]struct{}, len(endpoints))
	result := make([]core.PortTrafficEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		key := strconv.Itoa(endpoint.Port) + "\x00" + string(endpoint.Protocol) + "\x00" + endpoint.Name
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, endpoint)
		if len(result) == maxDiscoveredTrafficPorts {
			break
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Port != result[right].Port {
			return result[left].Port < result[right].Port
		}
		return result[left].Name < result[right].Name
	})
	return result
}
