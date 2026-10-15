package serverconfig

import (
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func validateXHTTPInput(input *Input) error {
	if input.Transport != "xhttp" {
		return nil
	}
	input.XHTTPMode = xhttpMode(input.XHTTPMode)
	if !slices.Contains([]string{"auto", "packet-up", "stream-up", "stream-one"}, input.XHTTPMode) {
		return fmt.Errorf("XHTTP mode 仅支持 auto、packet-up、stream-up 或 stream-one")
	}
	if !isVLESSEncryptionProtocol(input.Protocol) && input.Flow != "" {
		return fmt.Errorf("普通 VLESS XHTTP 的 flow 必须留空；Vision 仅用于组合的 Raw 入站或独立 VLESS Encryption 规则")
	}
	if input.TLSEnabled && input.RealityEnabled {
		return fmt.Errorf("同一 XHTTP 入站不能同时终止 TLS 与 Reality")
	}
	if input.Protocol == ProtocolVLESSXHTTP && !input.RealityEnabled {
		return fmt.Errorf("XHTTP Reality 预设必须启用 Reality；TLS 请选 XHTTP TLS/CDN")
	}
	for _, address := range []string{input.ClientAddress, input.XHTTPTLSAddress} {
		if address != "" {
			if _, err := NormalizeClientAddress(address); err != nil {
				return fmt.Errorf("客户端连接地址：%w", err)
			}
		}
	}
	if input.ClientPort < 0 || input.ClientPort > 65535 || input.XHTTPTLSClientPort < 0 || input.XHTTPTLSClientPort > 65535 {
		return fmt.Errorf("客户端连接端口必须在 1–65535，留空使用监听端口")
	}
	for _, host := range []string{input.TransportHost, input.ClientHost, input.XHTTPTLSHost} {
		if len(host) > 253 || strings.ContainsAny(host, "\r\n\t /\\") {
			return fmt.Errorf("XHTTP Host 不能包含空白、路径或控制字符")
		}
	}
	if input.TransportHost != "" && input.ClientHost != "" && input.TransportHost != input.ClientHost {
		return fmt.Errorf("服务端设置 Host 时，客户端 Host 必须相同；也可清空服务端 Host")
	}
	if strings.ContainsAny(input.TransportPath, "\r\n\t?#") {
		return fmt.Errorf("XHTTP 路径不能包含控制字符、查询或片段")
	}
	if _, err := validatedSingBoxALPN(input.TLSALPN); input.TLSALPN != "" && err != nil {
		return err
	}
	if _, err := validatedSingBoxALPN(input.TLSClientALPN); input.TLSClientALPN != "" && err != nil {
		return err
	}
	if input.RealityEnabled {
		if len(xhttpRealityNames(*input)) > 16 {
			return fmt.Errorf("Reality 允许的 SNI 最多 16 个")
		}
		for _, name := range xhttpRealityNames(*input) {
			if _, err := normalizeRealityServerName(name); err != nil {
				return err
			}
		}
		if !slices.Contains(xhttpRealityNames(*input), input.RealityServerName) {
			return fmt.Errorf("客户端 Reality SNI 必须在允许的 serverNames 中")
		}
		if input.RealityTarget != "" {
			host, port, err := net.SplitHostPort(input.RealityTarget)
			n, _ := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("独立 Reality target 必须为公网域名:有效端口；组合的本地目标由面板管理")
			}
			if _, err := normalizeRealityServerName(host); err != nil {
				return err
			}
		}
	}
	group := isXHTTPGroup(input.Protocol)
	if group {
		if len(input.Tag) > 61 {
			return fmt.Errorf("组合标签最长 61 位，需保留内部 .xh 标签空间")
		}
		if input.XHTTPMode == "stream-one" {
			return fmt.Errorf("混合 / 五合一需要上下行分离，请将 mode 改为 auto 或 stream-up")
		}
		if input.TransportHost != "" {
			return fmt.Errorf("组合共享后端的服务端 Host 必须留空，客户端分别维护 TLS 与 Reality Host")
		}
		if input.RealityMLDSA65Seed != "" {
			return fmt.Errorf("组合本地 TLS 目标未提供 ML-DSA 证书链验证，请清空 Seed")
		}
		if !input.RealityEnabled {
			return fmt.Errorf("组合公共入口需要 Reality")
		}
		if input.XHTTPBackendPort < 1024 || input.XHTTPBackendPort > 65535 || input.XHTTPTLSPort < 1024 || input.XHTTPTLSPort > 65535 || input.XHTTPBackendPort == input.XHTTPTLSPort || input.XHTTPBackendPort == input.Port || input.XHTTPTLSPort == input.Port {
			return fmt.Errorf("内部 XHTTP / TLS 端口必须为不同的 1024–65535 端口，且不能等于公网端口")
		}
		if input.RealityServerName == input.TLSServerName {
			return fmt.Errorf("组合的 Reality SNI 与 TLS/CDN SNI 必须不同")
		}
	}
	if input.TLSEnabled || group {
		if _, err := normalizeRealityServerName(input.TLSServerName); err != nil {
			return fmt.Errorf("TLS SNI：%w", err)
		}
		for _, path := range []string{input.CertificatePath, input.PrivateKeyPath} {
			if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n\t\"'`;{}$\\") {
				return fmt.Errorf("证书 / 私钥须使用有效绝对路径，且目标服务组可读取")
			}
		}
	}
	if input.XHTTPCDN != "" && input.XHTTPCDN != "none" && input.XHTTPCDN != "cloudflare" && input.XHTTPCDN != "other" {
		return fmt.Errorf("未知 CDN 类型")
	}
	tlsPort := input.ClientPort
	if tlsPort == 0 {
		tlsPort = input.Port
	}
	if group && input.XHTTPTLSClientPort != 0 {
		tlsPort = input.XHTTPTLSClientPort
	}
	if input.XHTTPCDN == "cloudflare" && !slices.Contains([]int{443, 2053, 2083, 2087, 2096, 8443}, tlsPort) {
		return fmt.Errorf("Cloudflare HTTPS 支持端口为 443、2053、2083、2087、2096、8443；请调整入口端口或回源端口规则")
	}
	if input.RealityEnabled && net.ParseIP(input.ClientAddress) != nil && net.ParseIP(input.ClientAddress).To4() == nil && input.Listen == "0.0.0.0" {
		return fmt.Errorf("上传 IPv6 无法到达 IPv4 监听；将监听改为 :: 或使用可达的 IPv4 入口")
	}
	if (input.TLSEnabled || group) && !group && input.XHTTPCDN != "" && input.XHTTPCDN != "none" && xhttpClientHost(*input) == "" {
		return fmt.Errorf("CDN 需要站点 XHTTP Host；请填写客户端 Host（服务端可留空）")
	}
	if group && input.TLSALPN != "" && input.TLSALPN != "h2" {
		return fmt.Errorf("组合本地 TLS 目标固定使用 h2，请将服务端 ALPN 改为 h2")
	}
	if (input.TLSEnabled || group) && (input.XHTTPCDN == "" || input.XHTTPCDN == "none") {
		if slices.Contains(xhttpALPN(input.TLSClientALPN), "h3") {
			return fmt.Errorf("原生 TLS / 组合源站只有 TCP 入口，客户端 ALPN 请使用 h2；h3 仅用于具备 HTTP/3 入口的 CDN")
		}
		clientALPN := xhttpALPN(input.TLSClientALPN)
		if len(clientALPN) == 0 {
			clientALPN = []string{"h2"}
		}
		serverALPN := xhttpALPN(input.TLSALPN)
		if group {
			serverALPN = []string{"h2"}
		}
		shared := len(serverALPN) == 0
		for _, value := range clientALPN {
			shared = shared || slices.Contains(serverALPN, value)
		}
		if !shared {
			return fmt.Errorf("直连客户端与源站 ALPN 没有交集，请两端启用 h2")
		}
	}
	extra, err := xhttpObject(input.XHTTPExtra)
	if err != nil {
		return err
	}
	if err = validateXHTTPExtra(extra, false); err != nil {
		return err
	}
	server, err := xhttpObject(input.XHTTPServerExtra)
	if err != nil {
		return err
	}
	if err = validateXHTTPExtra(server, true); err != nil {
		return err
	}
	if d, exists := extra["downloadSettings"]; exists {
		if input.XHTTPMode == "stream-one" {
			return fmt.Errorf("stream-one 不支持 downloadSettings，请改为 auto / stream-up / packet-up")
		}
		if group {
			return fmt.Errorf("组合下载方向由所选方案生成；请使用独立 Reality / TLS 预设配置自定义 downloadSettings")
		}
		if err := validateXHTTPDownload(*input, d); err != nil {
			return err
		}
	}
	return nil
}

func validateXHTTPExtra(extra map[string]any, server bool) error {
	for _, key := range []string{"host", "path", "mode", "extra"} {
		if _, ok := extra[key]; ok {
			return fmt.Errorf("extra.%s 会被内核覆盖，请使用对应基础字段", key)
		}
	}
	if server {
		for _, key := range []string{"downloadSettings", "xmux", "noGRPCHeader", "scMinPostsIntervalMs"} {
			if _, ok := extra[key]; ok {
				return fmt.Errorf("%s 是客户端专属字段，请移到客户端 extra", key)
			}
		}
	}
	xmux := mapValue(extra["xmux"])
	if _, old := xmux["cMaxLifetimeMs"]; old {
		return fmt.Errorf("当前 Xray 已不支持 xmux.cMaxLifetimeMs，请改用 hMaxReusableSecs（秒）")
	}
	positive := func(v any) bool {
		text := fmt.Sprint(v)
		if text == "<nil>" {
			return false
		}
		for _, x := range strings.Split(text, "-") {
			n, _ := strconv.Atoi(x)
			if n > 0 {
				return true
			}
		}
		return false
	}
	if positive(xmux["maxConnections"]) && positive(xmux["maxConcurrency"]) {
		return fmt.Errorf("xmux.maxConnections 与 maxConcurrency 不能同时为正；将其中一个设为 0")
	}
	return nil
}

func validateXHTTPDownload(input Input, raw any) error {
	d := mapValue(raw)
	if d == nil || d["network"] != "xhttp" {
		return fmt.Errorf("downloadSettings 必须为 network=xhttp 的完整对象")
	}
	address, err := NormalizeClientAddress(stringValue(d["address"]))
	if err != nil || intValue(d["port"]) < 1 || intValue(d["port"]) > 65535 {
		return fmt.Errorf("downloadSettings 需要有效的 address 与 port（IPv6 不附端口）")
	}
	x := mapValue(d["xhttpSettings"])
	if stringValue(x["path"]) != input.TransportPath {
		return fmt.Errorf("下载路径必须关联同一个 XHTTP 入站路径")
	}
	if input.TransportHost != "" && stringValue(x["host"]) != input.TransportHost {
		return fmt.Errorf("下载 Host 必须匹配服务端 Host")
	}
	if input.RealityEnabled {
		if d["security"] != "reality" {
			return fmt.Errorf("独立 Reality 下载入口需使用 Reality；混合方案请选择对应组合预设")
		}
		r := mapValue(d["realitySettings"])
		if !slices.Contains(xhttpRealityNames(input), stringValue(r["serverName"])) || stringValue(r["publicKey"]) != input.RealityPublicKey || stringValue(r["shortId"]) != input.RealityShortID {
			return fmt.Errorf("下载 Reality 的 SNI、公钥和 Short ID 必须关联当前入站")
		}
		ip := net.ParseIP(address)
		if ip != nil && input.Listen == "0.0.0.0" && ip.To4() == nil {
			return fmt.Errorf("下载 IPv6 无法到达 IPv4 监听；将监听改为 :: 或配置可达的转发入口")
		}
	} else {
		if d["security"] != "tls" {
			return fmt.Errorf("独立 TLS 下载入口需使用 TLS")
		}
		t := mapValue(d["tlsSettings"])
		if t["allowInsecure"] == true {
			return fmt.Errorf("下载 TLS 必须开启证书验证：allowInsecure=false")
		}
		if _, err := normalizeRealityServerName(stringValue(t["serverName"])); err != nil {
			return fmt.Errorf("下载 TLS 需要有效 SNI")
		}
		alpn, _ := t["alpn"].([]any)
		if len(alpn) == 1 && alpn[0] == "http/1.1" && t["fingerprint"] != "unsafe" {
			return fmt.Errorf("下载 HTTP/1.1 请设置 tlsSettings.fingerprint=unsafe 使用标准 TLS（证书验证仍开启），避免 uTLS 协商 h2")
		}
		if input.XHTTPCDN == "" || input.XHTTPCDN == "none" {
			for _, value := range alpn {
				if value == "h3" {
					return fmt.Errorf("下载源站只有 TCP 入口；请使用 h2，h3 需选择支持 HTTP/3 的 CDN")
				}
			}
		}
		if input.XHTTPCDN == "cloudflare" && !slices.Contains([]int{443, 2053, 2083, 2087, 2096, 8443}, intValue(d["port"])) {
			return fmt.Errorf("下载 Cloudflare HTTPS 端口需为 443、2053、2083、2087、2096、8443")
		}
	}
	if e := mapValue(x["extra"]); e != nil {
		if _, nested := e["downloadSettings"]; nested {
			return fmt.Errorf("下载参数不支持递归 downloadSettings")
		}
		return validateXHTTPExtra(e, false)
	}
	return nil
}
