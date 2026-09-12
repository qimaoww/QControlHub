package serverconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// ValidateIndependentEgress is shared by task submission and Agent execution.
// A syntax-valid configuration is not enough: every proxy listener must have
// an attributable independent exit, with no unknown route silently skipped.
func ValidateIndependentEgress(engine core.Engine, content string) error {
	_, err := PrepareIndependentEgress(engine, content)
	if err != nil {
		return fmt.Errorf("独立出口校验失败：%w", err)
	}
	return nil
}

// PrepareIndependentEgress also permits an explicitly empty proxy listener
// list, so deleting the final inbound remains possible. This is not a
// discovery-based fallback: unknown ingress fields and mutable APIs cannot
// qualify as an empty configuration. The core still validates its own schema.
func PrepareIndependentEgress(engine core.Engine, content string) (AccountingPlan, error) {
	root, err := decodeAccountingRoot(engine, content)
	if err != nil {
		return AccountingPlan{}, err
	}
	if err := rejectMutableRoutingAPI(engine, root); err != nil {
		return AccountingPlan{}, err
	}
	if explicitlyEmptyProxyConfig(engine, root) {
		return AccountingPlan{Content: content, Source: "disabled"}, nil
	}
	plan, err := PrepareAccounting(engine, content)
	if err != nil && engine == core.EngineSingBox {
		return PrepareMarkedSingBoxAccounting(content)
	}
	return plan, err
}

func decodeAccountingRoot(engine core.Engine, content string) (map[string]any, error) {
	if err := core.ValidateConfig(engine, content); err != nil {
		return nil, err
	}
	root := decodeTrafficConfiguration(engine, content)
	if engine != core.EngineMihomo {
		if err := rejectAmbiguousSharedJSON(content); err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&root); err != nil {
			return nil, err
		}
	}
	if root == nil {
		return nil, fmt.Errorf("invalid core configuration")
	}
	if err := validateAccountingFieldNames(root); err != nil {
		return nil, err
	}
	return root, nil
}

func rejectMutableRoutingAPI(engine core.Engine, root map[string]any) error {
	switch engine {
	case core.EngineMihomo:
		for _, key := range []string{"external-controller", "external-controller-tls", "external-controller-unix", "external-controller-pipe"} {
			if root[key] != nil && root[key] != "" {
				return fmt.Errorf("mutable routing APIs cannot guarantee independent exits")
			}
		}
	case core.EngineXray:
		api := mapValue(root["api"])
		if raw := api["services"]; raw != nil {
			services, ok := raw.([]any)
			if !ok {
				return fmt.Errorf("mutable routing APIs cannot guarantee independent exits")
			}
			for _, service := range services {
				if service != "StatsService" {
					return fmt.Errorf("mutable routing APIs cannot guarantee independent exits")
				}
			}
		}
	case core.EngineSingBox:
		if mapValue(root["experimental"])["clash_api"] != nil {
			return fmt.Errorf("mutable routing APIs cannot guarantee independent exits")
		}
	}
	return nil
}

func explicitlyEmptyProxyConfig(engine core.Engine, root map[string]any) bool {
	var listKey, allowed string
	switch engine {
	case core.EngineMihomo:
		listKey, allowed = "listeners", "listeners rules proxies dns log-level ipv6 allow-lan bind-address mode tcp-concurrent unified-delay find-process-mode global-client-fingerprint profile geodata-mode geodata-loader geo-auto-update geo-update-interval geox-url sniffer hosts authentication skip-auth-prefixes lan-allowed-ips lan-disallowed-ips"
		if dns := mapValue(root["dns"]); dns["listen"] != nil && dns["listen"] != "" {
			return false
		}
	case core.EngineXray:
		listKey, allowed = "inbounds", "log routing dns inbounds outbounds policy stats api transport fakedns"
		if root["api"] != nil {
			if _, err := xrayAccountingAPIAddress(root); err != nil {
				return false
			}
		}
	case core.EngineSingBox:
		listKey, allowed = "inbounds", "$schema log dns ntp inbounds outbounds route experimental"
		for key := range mapValue(root["experimental"]) {
			if key != "v2ray_api" && key != "cache_file" {
				return false
			}
		}
		if api := mapValue(mapValue(root["experimental"])["v2ray_api"]); api != nil && api["listen"] != "127.0.0.1:10086" {
			return false
		}
	case core.EngineShadowsocksRust:
		listKey, allowed = "servers", "servers mode timeout no_delay fast_open keep_alive dns ipv6_first ipv6_only acl workers outbound_bind_addr outbound_bind_interface nofile tcp_keep_alive tcp_nodelay udp_timeout udp_max_associations udp_mtu"
	default:
		return false
	}
	raw, exists := root[listKey]
	entries, list := raw.([]any)
	if !exists || (raw != nil && (!list || len(entries) != 0)) {
		return false
	}
	for key := range root {
		if !containsSharedKey(allowed, key) {
			return false
		}
	}
	return true
}

// JSON-based cores can decode struct fields case-insensitively. Do not let a
// differently cased routing field bypass checks performed on a map. Names in
// opaque user maps are left alone unless they shadow a security-sensitive key.
func validateAccountingFieldNames(value any) error {
	canonical := strings.Fields("inbounds outbounds endpoints routing route rules action inboundTag inbound outboundTag outbound balancerTag balancers final reverse api services listen port listen_port listen_ports server_port servers tag protocol type settings streamSettings sockopt dialerProxy proxySettings detour mux multiplex smux allocate fallbacks routing_mark outbound_fwmark plugin manager_address local_address local_port mode listeners name rule proxy tun tunnels external-controller external-controller-tls external-controller-unix external-controller-pipe experimental clash_api")
	var visit func(any) error
	visit = func(value any) error {
		switch item := value.(type) {
		case map[string]any:
			for key, child := range item {
				for _, field := range canonical {
					if key != field && strings.EqualFold(key, field) {
						return fmt.Errorf("ambiguous routing field %q; use %q", key, field)
					}
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range item {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(value)
}
