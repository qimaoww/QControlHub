package serverconfig

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// SharedTrafficEndpoints is deliberately stricter than best-effort discovery.
// A shared user may only deploy listeners whose entire ingress can be covered
// by their administrator-reserved TCP+UDP ports. Unknown/dynamic listeners and
// configuration-management APIs fail closed. This is not an OS sandbox.
func SharedTrafficEndpoints(engine core.Engine, content string) ([]core.PortTrafficEndpoint, error) {
	if err := core.ValidateConfig(engine, content); err != nil {
		return nil, err
	}
	if engine != core.EngineMihomo {
		if err := rejectAmbiguousSharedJSON(content); err != nil {
			return nil, err
		}
	}
	root := decodeTrafficConfiguration(engine, content)
	var allowed, listKey, portKey, nameKey, kindKey string
	switch engine {
	case core.EngineMihomo:
		allowed = "listeners rules proxies proxy-groups proxy-providers rule-providers sub-rules dns log-level ipv6 allow-lan bind-address mode tcp-concurrent unified-delay find-process-mode global-client-fingerprint profile geodata-mode geodata-loader geo-auto-update geo-update-interval geox-url sniffer hosts authentication skip-auth-prefixes lan-allowed-ips lan-disallowed-ips"
		listKey, portKey, nameKey, kindKey = "listeners", "port", "name", "type"
		if dns := mapValue(root["dns"]); dns["listen"] != nil && dns["listen"] != "" {
			return nil, fmt.Errorf("shared configuration cannot expose a separate DNS listener")
		}
	case core.EngineXray:
		allowed = "log routing dns inbounds outbounds policy stats api transport fakedns"
		listKey, portKey, nameKey, kindKey = "inbounds", "port", "tag", "protocol"
		if api := mapValue(root["api"]); api != nil {
			services, ok := api["services"].([]any)
			if !ok || len(services) != 1 || services[0] != "StatsService" {
				return nil, fmt.Errorf("shared Xray configurations permit only the statistics API")
			}
			if address, err := xrayAccountingAPIAddress(root); err != nil || address != "127.0.0.1:10085" {
				return nil, fmt.Errorf("shared Xray statistics API must use 127.0.0.1:10085")
			}
		}
	case core.EngineSingBox:
		allowed = "$schema log dns ntp inbounds outbounds route experimental"
		listKey, portKey, nameKey, kindKey = "inbounds", "listen_port", "tag", "type"
		if experimental := mapValue(root["experimental"]); experimental != nil {
			for key := range experimental {
				if key != "cache_file" && key != "v2ray_api" {
					return nil, fmt.Errorf("shared sing-box experimental.%s is not supported", key)
				}
			}
			if api := mapValue(experimental["v2ray_api"]); api != nil && api["listen"] != "127.0.0.1:10086" {
				return nil, fmt.Errorf("shared sing-box statistics API must use 127.0.0.1:10086")
			}
		}
	case core.EngineShadowsocksRust:
		allowed = "server server_port password method mode timeout no_delay fast_open keep_alive dns ipv6_first ipv6_only servers acl workers outbound_bind_addr outbound_bind_interface outbound_fwmark nofile tcp_keep_alive tcp_nodelay udp_timeout udp_max_associations udp_mtu manager_address local_address local_port name id"
		listKey, portKey, nameKey = "servers", "server_port", "name"
		if root["servers"] != nil && root["server_port"] != nil {
			return nil, fmt.Errorf("shared SS Rust configurations cannot combine a root listener with servers")
		}
		// A manager can add arbitrary ports after deployment; local mode can
		// expose a second, unmetered ingress.
		for _, key := range []string{"manager_address", "local_address", "local_port"} {
			if _, exists := root[key]; exists {
				return nil, fmt.Errorf("shared SS Rust configurations cannot use %s", key)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported shared configuration engine")
	}
	for key := range root {
		if !containsSharedKey(allowed, key) {
			return nil, fmt.Errorf("shared configuration field %q is not supported; use explicit fixed-port server listeners", key)
		}
	}
	entries, ok := root[listKey].([]any)
	if engine == core.EngineShadowsocksRust && root[listKey] == nil {
		entries, ok = []any{root}, true
	}
	if !ok || len(entries) == 0 || len(entries) > maxDiscoveredTrafficPorts {
		return nil, fmt.Errorf("shared configurations require 1 to 256 explicit listeners")
	}
	result := make([]core.PortTrafficEndpoint, 0, len(entries))
	seen := map[int]bool{}
	for _, raw := range entries {
		entry := mapValue(raw)
		if entry == nil {
			return nil, fmt.Errorf("shared listener must be an object")
		}
		// Go's JSON decoders accept case-insensitive struct fields; never
		// allow another spelling to override the port checked here.
		for key := range entry {
			for _, protected := range []string{portKey, kindKey, "listen", "allocate", "plugin", "plugin_args", "local_port", "server_port", "listen_port", "port"} {
				if protected != "" && strings.EqualFold(key, protected) && key != protected {
					return nil, fmt.Errorf("ambiguous shared listener field %q", key)
				}
			}
		}
		for _, key := range []string{"allocate", "plugin", "plugin_opts", "plugin_args", "plugin_mode", "local_port", "local_address", "detour", "port-range", "listen_ports"} {
			if _, exists := entry[key]; exists {
				return nil, fmt.Errorf("shared listener field %q cannot be safely metered", key)
			}
		}
		port := trafficPortNumber(entry[portKey])
		if port == 0 {
			return nil, fmt.Errorf("shared listeners require a single fixed numeric port")
		}
		if engine == core.EngineXray && xrayInternalAPIInbound(root, entry) {
			if port != 10085 || entry["listen"] != "127.0.0.1" {
				return nil, fmt.Errorf("invalid shared statistics listener")
			}
			continue
		}
		if port == 10085 || port == 10086 || seen[port] {
			return nil, fmt.Errorf("shared listener port %d is reserved or duplicated", port)
		}
		seen[port] = true
		if listen := stringValue(entry["listen"]); listen != "" && listen != "localhost" && net.ParseIP(listen) == nil {
			return nil, fmt.Errorf("shared listeners require an IP listen address")
		}
		kind := stringValue(entry[kindKey])
		kinds := ""
		switch engine {
		case core.EngineMihomo:
			kinds = "http shadowsocks vmess vless trojan snell sudoku hysteria2 tuic anytls tunnel"
		case core.EngineXray:
			kinds = "http shadowsocks vmess vless trojan hysteria tunnel dokodemo-door"
		case core.EngineSingBox:
			kinds = "http shadowsocks vmess vless trojan hysteria2 tuic anytls direct"
		}
		if kindKey != "" && !containsSharedKey(kinds, kind) {
			return nil, fmt.Errorf("shared listener type %q is not supported", kind)
		}
		result = append(result, trafficEndpoint(engine, trafficEndpointName(entry[nameKey], kind, "Shared listener"), port, core.TrafficProtocolBoth))
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("shared configuration has no billable listener")
	}
	return result, nil
}

func containsSharedKey(keys, key string) bool {
	return key != "" && strings.Contains(" "+keys+" ", " "+key+" ")
}

func rejectAmbiguousSharedJSON(content string) error {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name := strings.ToLower(key.(string))
				if seen[name] {
					return fmt.Errorf("ambiguous duplicate JSON field %q", key)
				}
				seen[name] = true
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case json.Delim('['):
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	if err := visit(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("unexpected JSON content")
	}
	return nil
}
