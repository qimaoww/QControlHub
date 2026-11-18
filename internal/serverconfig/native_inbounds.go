package serverconfig

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

var xrayPortSpec = regexp.MustCompile(`^(env:[A-Za-z_][A-Za-z0-9_]*|[0-9]{1,5}(-[0-9]{1,5})?(,[0-9]{1,5}(-[0-9]{1,5})?)*)$`)

const ssRustRootSourceIndex = -1

// NativeInbound describes a source row even when there is no preset or
// routable access-policy target. TargetTag is present only for an unambiguous
// native identity. A legacy SS Rust singleton has an implicit identity, but
// its display name still comes from the document.
type NativeInbound struct {
	Section     string `json:"section"`
	Tag         string `json:"tag"`
	Listen      string `json:"listen,omitempty"`
	Port        int    `json:"port,omitempty"`
	PortSpec    string `json:"port_spec,omitempty"`
	Network     string `json:"network,omitempty"`
	TargetTag   string `json:"target_tag,omitempty"`
	SourceIndex int    `json:"-"`
}

func DiscoverNativeInbounds(engine core.Engine, content string) ([]NativeInbound, error) {
	if err := core.ValidateConfig(engine, content); err != nil {
		return nil, err
	}
	if engine != core.EngineMihomo {
		if err := rejectAmbiguousSharedJSON(content); err != nil {
			return nil, err
		}
	}
	root, err := managedDocument(engine, content)
	if err != nil {
		return nil, err
	}
	var sections []string
	switch engine {
	case core.EngineXray:
		sections = []string{"inbounds"}
	case core.EngineSingBox:
		sections = []string{"inbounds", "endpoints"}
	case core.EngineMihomo:
		sections = []string{"listeners"}
	case core.EngineShadowsocksRust:
		sections = []string{"servers", "shadowsocks"}
	}
	rows := make([]NativeInbound, 0)
	counts := map[string]int{}
	_, rootServer := root["server"]
	_, rootPort := root["server_port"]
	single := engine == core.EngineShadowsocksRust && (rootServer || rootPort)
	ssRustMixed := engine == core.EngineShadowsocksRust &&
		(root["servers"] != nil && root["shadowsocks"] != nil || single && (root["servers"] != nil || root["shadowsocks"] != nil))
	if single {
		row := describeNativeInbound(engine, "servers", root, root)
		row.SourceIndex = ssRustRootSourceIndex
		if !ssRustMixed {
			row.TargetTag = shadowsocksRustSingleTag(root)
		}
		rows = append(rows, row)
	}
	for _, section := range sections {
		raw := root[section]
		if raw == nil {
			continue
		}
		entries, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("%s 必须是入站数组", section)
		}
		for index, value := range entries {
			entry := mapValue(value)
			if len(entry) == 0 {
				continue
			}
			row := describeNativeInbound(engine, section, root, entry)
			row.SourceIndex = index
			identity := row.Tag
			if engine == core.EngineShadowsocksRust {
				identity = shadowsocksRustEntryTag(entry, index)
			}
			counts[identity]++
			if hiddenNativeInbound(engine, root, entry, identity) {
				continue
			}
			if strings.TrimSpace(row.Tag) != "" && !ssRustMixed {
				row.TargetTag = identity
			}
			rows = append(rows, row)
		}
	}
	for index := range rows {
		if counts[rows[index].TargetTag] > 1 {
			rows[index].TargetTag = ""
		}
	}
	return rows, nil
}

func hiddenNativeInbound(engine core.Engine, root, entry map[string]any, tag string) bool {
	return engine == core.EngineXray && (tag == "qch-stat-api" || xrayInternalAPIInbound(root, entry) ||
		strings.HasSuffix(tag, ".xh") && xhttpGroupOwner(root, tag) != "")
}

func describeNativeInbound(engine core.Engine, section string, root, entry map[string]any) NativeInbound {
	tag, listen := managedTag(engine, entry), stringValue(entry["listen"])
	port := trafficPortNumber(entry["listen_port"])
	portSpec := ""
	kind := stringValue(entry["type"])
	switch engine {
	case core.EngineXray:
		kind = stringValue(entry["protocol"])
		port = xrayInboundPort(entry)
		if port == 0 && xrayUsesPort(entry) {
			if raw := stringValue(entry["port"]); xrayPortSpec.MatchString(raw) {
				portSpec = raw
			}
		}
	case core.EngineMihomo:
		port = trafficPortNumber(entry["port"])
	case core.EngineShadowsocksRust:
		tag = shadowsocksRustName(entry)
		listen = shadowsocksRustListen(entry)
		port = trafficPortNumber(shadowsocksRustPort(entry))
		mode := stringValue(entry["mode"])
		if mode == "" {
			mode = stringValue(root["mode"])
		}
		if mode == "" {
			mode = "tcp_only"
		}
		return NativeInbound{Section: section, Tag: tag, Listen: listen, Port: port, Network: nativeNetworkLabel(mode)}
	}
	return NativeInbound{
		Section: section, Tag: tag, Listen: listen, Port: port, PortSpec: portSpec,
		Network: nativeNetworkLabel(string(trafficEntryProtocol(engine, entry, kind, ""))),
	}
}

func nativeNetworkLabel(network string) string {
	switch network {
	case "tcp", "tcp_only":
		return "TCP"
	case "udp", "udp_only":
		return "UDP"
	case "both", "tcp_and_udp":
		return "TCP / UDP"
	default:
		return network
	}
}

func xrayUsesPort(entry map[string]any) bool {
	listen := stringValue(entry["listen"])
	return !strings.HasPrefix(listen, "/") && !strings.HasPrefix(listen, "@") && entry["protocol"] != "tun"
}

func xrayInboundPort(entry map[string]any) int {
	if xrayUsesPort(entry) {
		return trafficPortNumber(entry["port"])
	}
	return 0
}

func defaultPresetListen(engine core.Engine, listen string) string {
	if listen == "" && (engine == core.EngineMihomo || engine == core.EngineXray) {
		return net.IPv4zero.String()
	}
	return listen
}
