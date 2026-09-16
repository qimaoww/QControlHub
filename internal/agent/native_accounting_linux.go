package agent

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"gopkg.in/yaml.v3"
)

type nativeAccountingSnapshot struct {
	Plan              serverconfig.AccountingPlan
	Counters          map[string]uint64
	ProcessEpoch      string
	ListenerProtocols map[int]core.TrafficProtocol
}

type nativeAccountingConfigEntry struct {
	digest   [sha256.Size]byte
	snapshot nativeAccountingSnapshot
	err      error
}

// Cache only immutable, content-derived metadata, never counters or process
// identity. Every sample still securely reads the file and checks the live core.
// A single entry per engine bounds memory and also avoids recompiling unchanged
// invalid configurations every second. Changes and rollbacks are checked at once.
func (e *Executor) accountingConfiguration(engine core.Engine, content string) (nativeAccountingSnapshot, error) {
	if !engine.Valid() {
		return nativeAccountingSnapshot{}, errors.New("invalid accounting engine")
	}
	digest := sha256.Sum256([]byte(content))
	e.accountingConfigMu.Lock()
	defer e.accountingConfigMu.Unlock()
	if entry, ok := e.accountingConfigs[engine]; ok && entry.digest == digest {
		return entry.snapshot, entry.err
	}
	snapshot, err := compileAccountingConfiguration(engine, content)
	if e.accountingConfigs == nil {
		e.accountingConfigs = make(map[core.Engine]nativeAccountingConfigEntry)
	}
	e.accountingConfigs[engine] = nativeAccountingConfigEntry{digest: digest, snapshot: snapshot, err: err}
	return snapshot, err
}

// The returned plan and listener map are read-only to accounting consumers.
func compileAccountingConfiguration(engine core.Engine, content string) (nativeAccountingSnapshot, error) {
	plan, err := serverconfig.PrepareAccounting(engine, content)
	if engine == core.EngineSingBox && (err != nil || !strings.Contains(content, `"v2ray_api"`)) {
		plan, err = serverconfig.PrepareMarkedSingBoxAccounting(content)
	}
	if err != nil {
		return nativeAccountingSnapshot{}, err
	}
	var actual, compiled any
	decode := func(content string, dest *any) error {
		if engine == core.EngineMihomo {
			return yaml.Unmarshal([]byte(content), dest)
		}
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		return decoder.Decode(dest)
	}
	if decode(content, &actual) != nil || decode(plan.Content, &compiled) != nil || !reflect.DeepEqual(actual, compiled) {
		return nativeAccountingSnapshot{}, errors.New("managed configuration has not migrated to independent outbound accounting")
	}
	return nativeAccountingSnapshot{Plan: plan, ListenerProtocols: accountingListenerProtocols(engine, content)}, nil
}

// Inbound payloads may carry UDP over a TCP listener (and vice versa).
// Unknown transports and duplicate ports deliberately remain unsupported.
func exclusiveXrayProtocols(engine core.Engine, content string) map[int]core.TrafficProtocol {
	ports := map[int]core.TrafficProtocol{}
	if engine != core.EngineXray {
		return ports
	}
	var root struct {
		Inbounds []struct {
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
			Settings struct {
				Network string `json:"network"`
			} `json:"settings"`
			Stream struct {
				Network  string `json:"network"`
				Security string `json:"security"`
				TLS      struct {
					ALPN []string `json:"alpn"`
				} `json:"tlsSettings"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if json.Unmarshal([]byte(content), &root) != nil {
		return ports
	}
	seen := map[int]bool{}
	for _, inbound := range root.Inbounds {
		if seen[inbound.Port] {
			ports[inbound.Port] = core.TrafficProtocolBoth
			continue
		}
		seen[inbound.Port] = true
		switch inbound.Protocol {
		case "wireguard":
			// WireGuard's outer listener is UDP even for tunneled TCP.
			ports[inbound.Port] = core.TrafficProtocolUDP
		case "vless", "vmess", "trojan":
			switch inbound.Stream.Network {
			case "", "tcp", "raw", "ws", "grpc", "http", "h2", "httpupgrade":
				ports[inbound.Port] = core.TrafficProtocolTCP
			case "splithttp", "xhttp":
				ports[inbound.Port] = core.TrafficProtocolTCP
				if inbound.Stream.Security == "tls" && len(inbound.Stream.TLS.ALPN) == 1 && inbound.Stream.TLS.ALPN[0] == "h3" {
					ports[inbound.Port] = core.TrafficProtocolUDP
				}
			case "quic", "kcp":
				ports[inbound.Port] = core.TrafficProtocolUDP
			}
		case "shadowsocks", "dokodemo-door":
			if inbound.Settings.Network == "tcp" {
				ports[inbound.Port] = core.TrafficProtocolTCP
			}
			if inbound.Settings.Network == "udp" {
				ports[inbound.Port] = core.TrafficProtocolUDP
			}
		}
	}
	return ports
}

func nativeAccountingScopeAllowed(policy core.PortTrafficPolicy, snapshot nativeAccountingSnapshot) bool {
	return policy.Protocol == core.TrafficProtocolBoth ||
		(snapshot.ListenerProtocols[policy.Port] == policy.Protocol && policy.Protocol != "")
}

func accountingListenerProtocols(engine core.Engine, content string) map[int]core.TrafficProtocol {
	result := map[int]core.TrafficProtocol{}
	seen := map[int]bool{}
	for _, endpoint := range serverconfig.DiscoverTrafficPorts(engine, content) {
		if seen[endpoint.Port] {
			result[endpoint.Port] = core.TrafficProtocolBoth
			continue
		}
		seen[endpoint.Port] = true
		result[endpoint.Port] = endpoint.Protocol
	}
	if engine == core.EngineXray {
		// Discovery is for presentation; only verified transports can authorize billing.
		for port := range result {
			result[port] = core.TrafficProtocolBoth
		}
		for port, protocol := range exclusiveXrayProtocols(engine, content) {
			result[port] = protocol
		}
	}
	if engine == core.EngineSingBox {
		var root struct {
			Inbounds []struct {
				Port      int    `json:"listen_port"`
				Type      string `json:"type"`
				Transport struct {
					Type string `json:"type"`
				} `json:"transport"`
			} `json:"inbounds"`
			Endpoints []struct {
				Port int    `json:"listen_port"`
				Type string `json:"type"`
			} `json:"endpoints"`
		}
		if json.Unmarshal([]byte(content), &root) != nil {
			return nil
		}
		seen := map[int]bool{}
		for _, in := range root.Inbounds {
			if seen[in.Port] {
				result[in.Port] = core.TrafficProtocolBoth
				continue
			}
			seen[in.Port] = true
			switch in.Type {
			case "vless", "vmess", "trojan", "anytls", "http":
				result[in.Port] = core.TrafficProtocolTCP
			case "hysteria", "hysteria2", "tuic":
				result[in.Port] = core.TrafficProtocolUDP
			case "direct", "shadowsocks":
				// These listener types honor discovery's explicit network
				// selection. Other kinds must not authorize billing by hint.
			default:
				result[in.Port] = core.TrafficProtocolBoth
			}
			if in.Transport.Type != "" && in.Transport.Type != "tcp" && in.Transport.Type != "ws" && in.Transport.Type != "http" && in.Transport.Type != "httpupgrade" && in.Transport.Type != "grpc" {
				result[in.Port] = core.TrafficProtocolBoth
			}
		}
		for _, endpoint := range root.Endpoints {
			if endpoint.Port < 1 || endpoint.Port > 65535 {
				continue // Dynamic endpoints have no verifiable fixed listener.
			}
			if seen[endpoint.Port] {
				result[endpoint.Port] = core.TrafficProtocolBoth
				continue
			}
			seen[endpoint.Port] = true
			result[endpoint.Port] = core.TrafficProtocolBoth
			if endpoint.Type == "wireguard" {
				result[endpoint.Port] = core.TrafficProtocolUDP
			}
		}
	}
	if engine == core.EngineMihomo {
		// Do not trust arbitrary `network` hints on listener kinds that do not
		// use them. Unknown kinds remain protocol-ambiguous.
		for port := range result {
			result[port] = core.TrafficProtocolBoth
		}
		var root struct {
			Port      int `yaml:"port"`
			RedirPort int `yaml:"redir-port"`
			Listeners []struct {
				Port int    `yaml:"port"`
				Type string `yaml:"type"`
			} `yaml:"listeners"`
		}
		if yaml.Unmarshal([]byte(content), &root) != nil {
			return nil
		}
		seen := map[int]bool{}
		for _, port := range []int{root.Port, root.RedirPort} {
			if port != 0 {
				result[port] = core.TrafficProtocolTCP
				seen[port] = true
			}
		}
		for _, in := range root.Listeners {
			if seen[in.Port] {
				result[in.Port] = core.TrafficProtocolBoth
				continue
			}
			seen[in.Port] = true
			switch in.Type {
			case "vless", "vmess", "trojan", "http", "redir", "anytls":
				result[in.Port] = core.TrafficProtocolTCP
			case "hysteria2", "hysteria", "tuic":
				result[in.Port] = core.TrafficProtocolUDP
			}
		}
	}
	return result
}
