package serverconfig

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

func isXHTTPGroup(protocol string) bool {
	return protocol == ProtocolXHTTPFive || protocol == ProtocolXHTTPTLSReality || protocol == ProtocolXHTTPRealityTLS
}

func xhttpProtocols() []Protocol {
	var result []Protocol
	for _, item := range []struct{ key, name, description string }{
		{ProtocolXHTTPTLS, "VLESS-XHTTP-TLS / CDN", "独立 XHTTP：支持 Caddy、Nginx 自动证书或内核 TLS；CDN 需严格回源并绕过路径缓存。"},
		{ProtocolXHTTPTLSReality, "XHTTP · TLS/CDN 上传 → Reality 下载", "独立混合方案：一个公共 Reality 入口、一个共享 XHTTP 后端与受管 Nginx 本地 TLS 目标。"},
		{ProtocolXHTTPRealityTLS, "XHTTP · Reality 上传 → TLS/CDN 下载", "独立混合方案：一个公共 Reality 入口、一个共享 XHTTP 后端与受管 Nginx 本地 TLS 目标。"},
		{ProtocolXHTTPFive, "XHTTP · 五合一组合", "五种客户端连接共享公共端口、凭据与 XHTTP 路径；依赖域名证书及受管 Nginx。"},
	} {
		group := isXHTTPGroup(item.key)
		result = append(result, Protocol{Key: item.key, Name: item.name, Badge: "XHTTP", Group: "VLESS", Description: item.description, Docs: "https://github.com/XTLS/Xray-core/discussions/4118", DefaultPort: 443, Credential: "用户 UUID", Transports: []string{"xhttp"}, TransportConfig: true, UsesReality: group, SupportsTLS: !group, RequiresTLS: !group, DefaultTLS: !group, SupportsManagedTLS: !group})
	}
	result[0].StaticPages = xhttpStaticPages()
	return result
}

func initializeXHTTPPlan(protocol Protocol, input *Input) error {
	if input.Transport != "xhttp" {
		return nil
	}
	input.XHTTPMode = "auto"
	if input.TLSEnabled {
		input.TLSServerName = ""
		input.TLSALPN, input.TLSClientALPN = "h2,http/1.1", "h2"
	}
	if !isXHTTPGroup(protocol.Key) {
		return nil
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	input.RealityEnabled, input.TLSEnabled = true, false
	input.RealityServerName, input.RealityMinClientVer = "", "0.0.0"
	input.CertificatePath, input.PrivateKeyPath = "", ""
	input.TLSServerName = ""
	input.RealityPrivateKey = base64.RawURLEncoding.EncodeToString(key.Bytes())
	input.RealityPublicKey = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	shortID := make([]byte, 8)
	if _, err := rand.Read(shortID); err != nil {
		return err
	}
	input.RealityShortID = hex.EncodeToString(shortID)
	input.XHTTPBackendPort, input.XHTTPTLSPort = input.Port+1, input.Port+2
	input.Port, input.ClientPort = 443, 443
	input.TLSALPN, input.TLSClientALPN = "h2", "h2"
	return nil
}

func copyXHTTPOptions(plan *Input, current Input) {
	plan.XHTTPMode, plan.XHTTPExtra, plan.XHTTPServerExtra = current.XHTTPMode, current.XHTTPExtra, current.XHTTPServerExtra
	plan.ClientAddress, plan.ClientPort, plan.ClientHost = current.ClientAddress, current.ClientPort, current.ClientHost
	plan.TLSClientALPN, plan.XHTTPCDN = current.TLSClientALPN, current.XHTTPCDN
	plan.RealityTarget, plan.RealityServerNames = current.RealityTarget, current.RealityServerNames
	plan.XHTTPTLSAddress = current.XHTTPTLSAddress
	plan.XHTTPTLSClientPort, plan.XHTTPTLSHost = current.XHTTPTLSClientPort, current.XHTTPTLSHost
	plan.XHTTPBackendPort, plan.XHTTPTLSPort = current.XHTTPBackendPort, current.XHTTPTLSPort
}

// Preserve endpoint overrides and unknown download options while updating the
// fields that belong to the regenerated listener and Reality identity.
func regenerateXHTTPReferences(plan *Input, current Input) error {
	if plan.Transport != "xhttp" {
		return nil
	}
	extra, err := xhttpObject(plan.XHTTPExtra)
	if err != nil {
		return err
	}
	if plan.ClientPort == current.Port && (current.XHTTPCDN == "" || current.XHTTPCDN == "none") {
		plan.ClientPort = plan.Port
	}
	if d := mapValue(extra["downloadSettings"]); d != nil {
		if x := mapValue(d["xhttpSettings"]); x != nil {
			x["path"] = plan.TransportPath
		}
		if r := mapValue(d["realitySettings"]); r != nil {
			r["publicKey"], r["shortId"] = plan.RealityPublicKey, plan.RealityShortID
			if verify := realityMLDSAVerify(*plan); verify != "" {
				r["mldsa65Verify"] = verify
			} else {
				delete(r, "mldsa65Verify")
			}
		}
		if intValue(d["port"]) == current.Port && (current.XHTTPCDN == "" || current.XHTTPCDN == "none") {
			d["port"] = plan.Port
		}
		value, err := json.Marshal(extra)
		if err != nil {
			return err
		}
		plan.XHTTPExtra = string(value)
	}
	return nil
}

func xhttpALPN(value string) []string {
	var result []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func xhttpMode(value string) string {
	if value == "" {
		return "auto"
	}
	return value
}

func xhttpRealityTarget(input Input) string {
	if input.RealityTarget != "" {
		return input.RealityTarget
	}
	return net.JoinHostPort(input.RealityServerName, "443")
}

func xhttpRealityNames(input Input) []string {
	if input.RealityServerNames != "" {
		return xhttpALPN(input.RealityServerNames)
	}
	return []string{input.RealityServerName}
}

func xhttpObject(value string) (map[string]any, error) {
	if strings.TrimSpace(value) == "" {
		return map[string]any{}, nil
	}
	var result map[string]any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if len(value) > 64<<10 || !json.Valid([]byte(value)) || decoder.Decode(&result) != nil || result == nil {
		return nil, fmt.Errorf("XHTTP extra 必须为 JSON 对象，最大 64 KiB")
	}
	return result, nil
}

func xhttpServerSettings(input Input) map[string]any {
	settings := map[string]any{"path": input.TransportPath, "host": input.TransportHost, "mode": xhttpServerMode(input)}
	if extra, _ := xhttpObject(input.XHTTPServerExtra); len(extra) > 0 {
		settings["extra"] = extra
	}
	return settings
}

func xhttpServerMode(input Input) string {
	// Preserve the shared and Reality backend behavior of existing presets.
	if input.Protocol != ProtocolXHTTPTLS {
		return "auto"
	}
	return xhttpMode(input.XHTTPMode)
}

func parseXHTTPSettings(input *Input, stream map[string]any) {
	x := mapValue(stream["xhttpSettings"])
	if x == nil {
		x = mapValue(stream["splithttpSettings"])
	}
	input.TransportPath, input.TransportHost = stringValue(x["path"]), stringValue(x["host"])
	input.XHTTPMode = xhttpMode(stringValue(x["mode"]))
	extra := mapValue(x["extra"])
	// Legacy native top-level options have the same semantics unless extra exists.
	if extra == nil {
		extra = cloneAccountingObject(x)
		delete(extra, "path")
		delete(extra, "host")
		delete(extra, "mode")
	}
	if len(extra) > 0 {
		value, _ := json.Marshal(extra)
		input.XHTTPServerExtra = string(value)
	}
}

func xhttpGroupInbounds(input Input, primary map[string]any) []any {
	backend := cloneAccountingObject(primary)
	backend["tag"], backend["listen"], backend["port"] = input.Tag+".xh", "127.0.0.1", input.XHTTPBackendPort
	backend["settings"] = map[string]any{"clients": []any{map[string]any{"id": input.Credential, "email": input.Username, "level": 0}}, "decryption": "none"}
	backend["streamSettings"] = map[string]any{"network": "xhttp", "security": "none", "xhttpSettings": xhttpServerSettings(input)}
	stream := xrayStream(input)
	stream["network"] = "raw"
	delete(stream, "xhttpSettings")
	mapValue(stream["realitySettings"])["target"] = net.JoinHostPort("127.0.0.1", strconv.Itoa(input.XHTTPTLSPort))
	primary["streamSettings"] = stream
	users := []any{}
	if input.Protocol == ProtocolXHTTPFive {
		users = append(users, map[string]any{"id": input.Credential, "email": input.Username, "level": 0, "flow": "xtls-rprx-vision"})
	}
	primary["settings"] = map[string]any{"clients": users, "decryption": "none", "fallbacks": []any{map[string]any{"dest": net.JoinHostPort("127.0.0.1", strconv.Itoa(input.XHTTPBackendPort)), "xver": 0}}}
	return []any{primary, backend}
}
