package serverconfig

import (
	"encoding/json"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// BuildClientOutbound exports only client connection material from a deployed
// inbound. Never serialize Input: it also contains server private keys.
func BuildClientOutbound(engine core.Engine, input Input, address, serverName string) (json.RawMessage, error) {
	if input.Protocol == ProtocolTailscale || input.Protocol == ProtocolOpenVPNServer {
		return nil, fmt.Errorf("%s 服务端端点没有 sing-box 出站等价物", input.Protocol)
	}
	if engine != core.EngineXray && engine != core.EngineSingBox {
		return nil, fmt.Errorf("此内核不支持节点入站转出站")
	}
	profile, err := BuildClientProfile(input, address, serverName)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, field := range profile.Fields {
		values[field.Label] = field.Value
	}
	address, serverName = values["服务器"], values["TLS ServerName"]
	protocol := input.Protocol
	switch protocol {
	case ProtocolShadowsocks, ProtocolSS2022:
		protocol = "shadowsocks"
	case ProtocolVLESS, ProtocolVLESSXHTTP, ProtocolVLESSEncTCP, ProtocolVLESSEncXHTTP, ProtocolVLESSEncPlain:
		protocol = "vless"
	case ProtocolVMess, ProtocolTrojan:
	case ProtocolHy2, ProtocolTUIC, ProtocolAnyTLS:
		if engine == core.EngineXray {
			return nil, fmt.Errorf("Xray 出站预设暂不支持 %s 的独立统计", input.Protocol)
		}
	default:
		return nil, fmt.Errorf("当前内核不能将 %s 入站用作出站", input.Protocol)
	}
	if engine == core.EngineSingBox && (input.Transport == "xhttp" || values["ML-DSA-65 Verify"] != "" || values["VLESS Encryption"] != "") {
		return nil, fmt.Errorf("sing-box 不支持此入站的 XHTTP、VLESS Encryption 或 ML-DSA-65 参数，不能降级连接安全配置")
	}
	out := map[string]any{"tag": "peer"}
	if engine == core.EngineXray {
		out["protocol"] = protocol
		user := map[string]any{"id": input.Credential}
		switch protocol {
		case "vless", "vmess":
			if protocol == "vless" {
				user["encryption"] = "none"
				if values["VLESS Encryption"] != "" {
					user["encryption"] = values["VLESS Encryption"]
				}
				if input.Flow != "" {
					user["flow"] = input.Flow
				}
			} else {
				user["security"] = "auto"
			}
			out["settings"] = map[string]any{"vnext": []any{map[string]any{"address": address, "port": input.Port, "users": []any{user}}}}
		default:
			server := map[string]any{"address": address, "port": input.Port, "password": input.Credential}
			if protocol == "shadowsocks" {
				server["method"] = input.Method
			}
			out["settings"] = map[string]any{"servers": []any{server}}
		}
		if protocol != "shadowsocks" {
			stream := map[string]any{"network": "tcp"}
			switch input.Transport {
			case "", "raw":
			case "websocket":
				stream["network"], stream["wsSettings"] = "ws", map[string]any{"path": input.TransportPath}
			case "grpc":
				stream["network"], stream["grpcSettings"] = "grpc", map[string]any{"serviceName": input.TransportPath}
			case "xhttp":
				stream["network"], stream["xhttpSettings"] = "xhttp", map[string]any{"path": input.TransportPath, "mode": "auto"}
			default:
				return nil, fmt.Errorf("不支持此入站传输：%s", input.Transport)
			}
			if input.RealityEnabled {
				reality := map[string]any{"serverName": serverName, "fingerprint": "chrome", "publicKey": input.RealityPublicKey, "shortId": input.RealityShortID}
				if values["ML-DSA-65 Verify"] != "" {
					reality["mldsa65Verify"] = values["ML-DSA-65 Verify"]
				}
				stream["security"], stream["realitySettings"] = "reality", reality
			} else if input.TLSEnabled {
				stream["security"], stream["tlsSettings"] = "tls", map[string]any{"serverName": serverName}
			}
			out["streamSettings"] = stream
		}
	} else {
		out["type"], out["server"], out["server_port"] = protocol, address, input.Port
		switch protocol {
		case "vless", "vmess":
			out["uuid"] = input.Credential
			if protocol == "vless" && input.Flow != "" {
				out["flow"] = input.Flow
			}
			if protocol == "vmess" {
				out["security"], out["alter_id"] = "auto", 0
			}
		case "tuic":
			out["uuid"], out["password"], out["congestion_control"] = input.Credential, input.SecondaryCredential, "bbr"
		default:
			out["password"] = input.Credential
		}
		if protocol == "shadowsocks" {
			out["method"] = input.Method
		}
		if protocol == "vless" || protocol == "vmess" || protocol == "trojan" {
			switch input.Transport {
			case "", "raw":
			case "websocket":
				out["transport"] = map[string]any{"type": "ws", "path": input.TransportPath}
			case "grpc":
				out["transport"] = map[string]any{"type": "grpc", "service_name": input.TransportPath}
			default:
				return nil, fmt.Errorf("不支持此入站传输：%s", input.Transport)
			}
		}
		if input.TLSEnabled || input.RealityEnabled {
			tls := map[string]any{"enabled": true, "server_name": serverName}
			if input.RealityEnabled {
				tls["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
				tls["reality"] = map[string]any{"enabled": true, "public_key": input.RealityPublicKey, "short_id": input.RealityShortID}
			}
			out["tls"] = tls
		}
	}
	return json.Marshal(out)
}
