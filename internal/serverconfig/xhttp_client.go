package serverconfig

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type ClientVariant struct {
	Name    string        `json:"name"`
	Profile ClientProfile `json:"profile"`
}

func xhttpClientAddress(input Input, address string) string {
	if input.ClientAddress != "" {
		return input.ClientAddress
	}
	return address
}

func xhttpClientHost(input Input) string {
	if input.ClientHost != "" {
		return input.ClientHost
	}
	return input.TransportHost
}

func xhttpClientStream(input Input, name string) map[string]any {
	stream := map[string]any{"network": "raw"}
	if input.Transport == "xhttp" {
		x := map[string]any{"path": input.TransportPath, "host": xhttpClientHost(input), "mode": xhttpMode(input.XHTTPMode)}
		if extra, _ := xhttpObject(input.XHTTPExtra); len(extra) > 0 {
			x["extra"] = extra
		}
		stream["network"], stream["xhttpSettings"] = "xhttp", x
	}
	if input.RealityEnabled {
		r := map[string]any{"serverName": input.RealityServerName, "publicKey": input.RealityPublicKey, "shortId": input.RealityShortID, "fingerprint": "chrome"}
		verify := input.RealityMLDSA65Verify
		if verify == "" && input.RealityMLDSA65Seed != "" {
			verify, _ = mldsa65VerifyFromSeed(input.RealityMLDSA65Seed)
		}
		if verify != "" {
			r["mldsa65Verify"] = verify
		}
		stream["security"], stream["realitySettings"] = "reality", r
	} else if input.TLSEnabled {
		if input.TLSServerName != "" {
			name = input.TLSServerName
		}
		t := map[string]any{"serverName": name, "allowInsecure": false, "fingerprint": "chrome"}
		alpn := input.TLSClientALPN
		if alpn == "" {
			alpn = "h2"
		}
		t["alpn"] = xhttpALPN(alpn)
		if alpn == "http/1.1" {
			// The Chrome hello advertises h2 independently of NextProtos.
			// "unsafe" selects standard TLS, with verification still enabled.
			t["fingerprint"] = "unsafe"
		}
		stream["security"], stream["tlsSettings"] = "tls", t
	}
	return stream
}

func xhttpEndpoint(input Input, tls bool) map[string]any {
	address := input.ClientAddress
	port := input.Port
	if input.ClientPort != 0 {
		port = input.ClientPort
	}
	if tls {
		address = input.XHTTPTLSAddress
		if address == "" {
			address = input.TLSServerName
		}
		input.RealityEnabled, input.TLSEnabled = false, true
		input.ClientHost = input.XHTTPTLSHost
		if input.ClientHost == "" {
			input.ClientHost = input.TLSServerName
		}
		if input.XHTTPTLSClientPort != 0 {
			port = input.XHTTPTLSClientPort
		}
	} else {
		input.RealityEnabled, input.TLSEnabled = true, false
	}
	input.XHTTPExtra, input.XHTTPMode = "", "auto"
	stream := xhttpClientStream(input, input.TLSServerName)
	stream["address"], stream["port"] = address, port
	return stream
}

func xrayVLESSOutbound(input Input, address, serverName string) map[string]any {
	address = xhttpClientAddress(input, address)
	port := input.Port
	if input.ClientPort != 0 {
		port = input.ClientPort
	}
	user := map[string]any{"id": input.Credential, "encryption": "none"}
	if input.Flow != "" {
		user["flow"] = input.Flow
	}
	if isVLESSEncryptionProtocol(input.Protocol) {
		user["encryption"] = input.VLESSEncryption
	}
	return map[string]any{"tag": "proxy", "protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": address, "port": port, "users": []any{user}}}}, "streamSettings": xhttpClientStream(input, serverName)}
}

func xrayClientJSON(input Input, address, name string) (string, error) {
	out := xrayVLESSOutbound(input, address, name)
	data, err := json.MarshalIndent(map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": []any{map[string]any{"tag": "socks", "listen": "127.0.0.1", "port": 10808, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": true}}}, "outbounds": []any{out}}, "", "  ")
	return string(data) + "\n", err
}

func buildXHTTPGroupProfile(input Input, address, name, nodeName string) (ClientProfile, error) {
	if input.ClientAddress == "" {
		input.ClientAddress = address
	}
	names := []struct{ key, name string }{{input.Protocol, "TLS/CDN 上传 → Reality 下载"}}
	if input.Protocol == ProtocolXHTTPRealityTLS {
		names[0].name = "Reality 上传 → TLS/CDN 下载"
	}
	if input.Protocol == ProtocolXHTTPFive {
		names = []struct{ key, name string }{{ProtocolVLESS, "Vision + Reality 直连"}, {ProtocolVLESSXHTTP, "XHTTP + Reality 直连"}, {ProtocolXHTTPTLS, "XHTTP + TLS/CDN"}, {ProtocolXHTTPTLSReality, "TLS/CDN 上传 → Reality 下载"}, {ProtocolXHTTPRealityTLS, "Reality 上传 → TLS/CDN 下载"}}
	}
	var result ClientProfile
	for i, item := range names {
		variant := xhttpClientVariant(input, item.key)
		profile, err := BuildClientProfileNamed(variant, address, name, nodeName)
		if err != nil {
			return ClientProfile{}, fmt.Errorf("%s：%w", item.name, err)
		}
		if i == 0 {
			result = profile
		}
		result.Variants = append(result.Variants, ClientVariant{Name: item.name, Profile: profile})
	}
	return result, nil
}

func addXHTTPClientExport(profile *ClientProfile, input Input, address, name string) error {
	if input.Transport != "xhttp" && !(input.Protocol == ProtocolVLESS && input.Transport == "raw" && input.RealityEnabled) {
		return nil
	}
	value, err := xrayClientJSON(input, address, name)
	if err != nil {
		return err
	}
	profile.Xray = value
	if input.Transport == "xhttp" {
		profile.SingBoxError = "sing-box 官方内核没有 XHTTP 传输，不能转换为其他传输"
		profile.Fields = append(profile.Fields, ClientField{Label: "XHTTP Mode", Value: xhttpMode(input.XHTTPMode)})
		extra, _ := xhttpObject(input.XHTTPExtra)
		if _, newer := mapValue(extra["xmux"])["hMaxReusableSecs"]; newer {
			profile.Fields = append(profile.Fields, ClientField{Label: "客户端版本", Value: "hMaxReusableSecs 请使用 Xray >= 26.3.27（当前验证版本）；24.12.15 使用旧 xmux 字段"})
		}
		if d := mapValue(extra["downloadSettings"]); d != nil {
			profile.Fields = append(profile.Fields, ClientField{Label: "下载端点", Value: stringValue(d["address"]) + ":" + strconv.Itoa(intValue(d["port"]))})
		}
		if len(extra) > 0 {
			profile.Format, profile.URI, profile.SubscriptionCompatible = "Xray client JSON", value, false
			profile.URIError = "完整 extra / downloadSettings 无统一分享链接标准，请复制或下载 Xray JSON"
			profile.Mihomo, profile.MihomoError = "", "Mihomo 导出器仅转换基础 XHTTP；extra.downloadSettings / xmux / sockopt 未完整映射到 download-settings / reuse-settings，请使用 Xray JSON"
		}
		if input.TLSEnabled && input.TLSClientALPN == "http/1.1" && input.XHTTPMode == "stream-one" {
			profile.Mihomo, profile.MihomoError = "", "Mihomo 1.19.32 的 HTTP/1.1 + stream-one 未通过连接验证；请使用 Xray JSON，或改为 h2 / packet-up / stream-up"
		}
	}
	return nil
}
