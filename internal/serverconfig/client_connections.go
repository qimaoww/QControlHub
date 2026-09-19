package serverconfig

import (
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// ConnectionListener deliberately contains no credentials or outbound settings.
type ConnectionListener struct {
	Engine    core.Engine
	Protocol  string
	Inbound   string
	Port      int
	Transport core.TrafficProtocol
}

func DiscoverConnectionListeners(engine core.Engine, content string) ([]ConnectionListener, error) {
	root := decodeTrafficConfiguration(engine, content)
	if root == nil {
		return nil, fmt.Errorf("invalid %s listener configuration", engine)
	}
	type listenerKey struct {
		port int
		name string
	}
	kinds := map[listenerKey]string{}
	add := func(value any, portKey, kindKey, nameKey string) {
		entries, _ := value.([]any)
		for _, raw := range entries {
			item, _ := raw.(map[string]any)
			kind, _ := item[kindKey].(string)
			if port := trafficPortNumber(item[portKey]); port > 0 {
				name := truncateTrafficName(trafficEndpointName(item[nameKey], kind, fmt.Sprintf("%s :%d", engine, port)))
				kinds[listenerKey{port, name}] = strings.ToLower(strings.TrimSpace(kind))
			}
		}
	}
	switch engine {
	case core.EngineMihomo:
		add(root["listeners"], "port", "type", "name")
		for key, kind := range map[string]string{"port": "http", "socks-port": "socks", "mixed-port": "mixed", "redir-port": "redirect", "tproxy-port": "tproxy"} {
			if port := trafficPortNumber(root[key]); port > 0 {
				names := map[string]string{"port": "HTTP proxy", "socks-port": "SOCKS proxy", "mixed-port": "Mixed proxy", "redir-port": "Redirect proxy", "tproxy-port": "TProxy"}
				kinds[listenerKey{port, names[key]}] = kind
			}
		}
	case core.EngineXray:
		add(root["inbounds"], "port", "protocol", "tag")
	case core.EngineSingBox:
		add(root["inbounds"], "listen_port", "type", "tag")
		add(root["endpoints"], "listen_port", "type", "tag")
	}
	var result []ConnectionListener
	for _, endpoint := range DiscoverTrafficPorts(engine, content) {
		kind := kinds[listenerKey{endpoint.Port, endpoint.Name}]
		if engine == core.EngineShadowsocksRust {
			kind = "shadowsocks"
		}
		if kind == "" {
			kind = "unknown"
		}
		// Validate metadata through the same public contract used on receipt.
		probe := core.ClientConnectionReport{Status: "ok", Connections: []core.ClientConnection{{Engine: engine, Protocol: kind, Inbound: endpoint.Name, Transport: "tcp", ClientIP: "127.0.0.1", LocalIP: "127.0.0.1", ClientPort: 1, LocalPort: endpoint.Port}}}
		if err := probe.Validate(); err != nil {
			return nil, fmt.Errorf("invalid listener metadata")
		}
		result = append(result, ConnectionListener{Engine: engine, Protocol: kind, Inbound: endpoint.Name, Port: endpoint.Port, Transport: endpoint.Protocol})
	}
	return result, nil
}
