package serverconfig

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

var (
	tagPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
)

func Generate(engine core.Engine, input Input) (string, error) {
	protocol, ok := FindProtocol(engine, input.Protocol)
	if !ok {
		return "", errors.New("不支持的服务端入站协议")
	}
	if !tagPattern.MatchString(input.Tag) {
		return "", errors.New("入站标签只能包含字母、数字、点、下划线和短横线，最长 64 位")
	}
	if input.Port < 1 || input.Port > 65535 {
		return "", errors.New("监听端口必须在 1 到 65535 之间")
	}
	if strings.TrimSpace(input.Listen) == "" || (net.ParseIP(input.Listen) == nil && input.Listen != "localhost") {
		return "", errors.New("监听地址必须是 IP 地址或 localhost")
	}
	if !protocol.IgnoresUsername && (strings.TrimSpace(input.Username) == "" || len(input.Username) > 64) {
		return "", errors.New("用户名不能为空且不能超过 64 个字符")
	}
	if err := validateCredential(input); err != nil {
		return "", err
	}
	if input.Transport == "" {
		input.Transport = "raw"
	}
	if input.Transport != "raw" && input.Transport != "websocket" && input.Transport != "grpc" {
		return "", errors.New("不支持的传输方式")
	}
	if input.Transport != "raw" && strings.TrimSpace(input.TransportPath) == "" {
		return "", errors.New("WebSocket 路径或 gRPC ServiceName 不能为空")
	}
	if len(input.TransportPath) > 256 {
		return "", errors.New("传输路径或 ServiceName 不能超过 256 个字符")
	}
	if input.Transport == "websocket" && !strings.HasPrefix(input.TransportPath, "/") {
		return "", errors.New("WebSocket 路径必须以 / 开头")
	}
	if protocol.RequiresTLS && !input.TLSEnabled {
		return "", errors.New("此服务端方案必须启用 TLS")
	}
	if input.TLSEnabled {
		if len(input.CertificatePath) > 1024 || len(input.PrivateKeyPath) > 1024 {
			return "", errors.New("TLS 文件路径不能超过 1024 个字符")
		}
		if strings.TrimSpace(input.CertificatePath) == "" || strings.TrimSpace(input.PrivateKeyPath) == "" {
			return "", errors.New("启用 TLS 时必须填写证书和私钥路径")
		}
	}
	if input.RealityEnabled {
		if input.Protocol != ProtocolVLESS || input.Transport != "raw" || input.Flow != "xtls-rprx-vision" {
			return "", errors.New("Reality 方案仅支持 VLESS Vision + Raw")
		}
		serverName, err := normalizeRealityServerName(input.RealityServerName)
		if err != nil {
			return "", err
		}
		input.RealityServerName = serverName
		privateBytes, err := base64.RawURLEncoding.DecodeString(input.RealityPrivateKey)
		if err != nil {
			return "", errors.New("Reality 私钥格式无效")
		}
		privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
		if err != nil || base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()) != input.RealityPublicKey {
			return "", errors.New("Reality X25519 密钥对无效")
		}
		shortID, err := hex.DecodeString(input.RealityShortID)
		if err != nil || len(shortID) != 8 {
			return "", errors.New("Reality Short ID 无效")
		}
	}
	switch engine {
	case core.EngineMihomo:
		return generateMihomo(input)
	case core.EngineXray:
		return generateXray(input)
	case core.EngineSingBox:
		return generateSingBox(input)
	case core.EngineShadowsocksRust:
		return generateShadowsocksRust(input)
	default:
		return "", fmt.Errorf("unsupported engine %q", engine)
	}
}

func validateCredential(input Input) error {
	if input.Protocol == ProtocolShadowsocks {
		switch input.Method {
		case "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305":
		default:
			return errors.New("不支持的 Shadowsocks 加密方法")
		}
	}
	if input.Protocol == ProtocolSS2022 {
		expected := 32
		if input.Method == "2022-blake3-aes-128-gcm" {
			expected = 16
		}
		if input.Method != "2022-blake3-aes-128-gcm" && input.Method != "2022-blake3-aes-256-gcm" && input.Method != "2022-blake3-chacha20-poly1305" {
			return errors.New("不支持的 Shadowsocks 2022 加密方法")
		}
		decoded, err := base64.StdEncoding.DecodeString(input.Credential)
		if err != nil || len(decoded) != expected {
			return fmt.Errorf("%s 的 PSK 必须是 %d 字节的标准 Base64", input.Method, expected)
		}
		return nil
	}
	if input.Protocol == ProtocolVLESS || input.Protocol == ProtocolVMess || input.Protocol == ProtocolTUIC {
		if !uuidPattern.MatchString(input.Credential) {
			return errors.New("VLESS/VMess/TUIC 用户凭据必须是有效 UUID")
		}
		if input.Protocol == ProtocolTUIC && (len(input.SecondaryCredential) < 16 || len(input.SecondaryCredential) > 128) {
			return errors.New("TUIC 用户密码必须在 16 到 128 个字符之间")
		}
		return nil
	}
	if len(input.Credential) < 16 || len(input.Credential) > 128 {
		return errors.New("服务端用户密码必须在 16 到 128 个字符之间")
	}
	return nil
}
