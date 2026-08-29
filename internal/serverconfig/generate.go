package serverconfig

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func forwardTarget(input Input) string {
	return net.JoinHostPort(input.TargetAddress, strconv.Itoa(input.TargetPort))
}

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
	if protocol.PortForward {
		target, err := NormalizeClientAddress(input.TargetAddress)
		if err != nil {
			return "", errors.New("转发目标必须是有效域名或 IP 地址")
		}
		input.TargetAddress = target
		if input.TargetPort < 1 || input.TargetPort > 65535 {
			return "", errors.New("目标端口必须在 1 到 65535 之间")
		}
		if input.Network != "tcp" && input.Network != "udp" && input.Network != "tcp,udp" {
			return "", errors.New("转发协议必须是 TCP、UDP 或 TCP + UDP")
		}
	} else {
		if !protocol.IgnoresUsername && (strings.TrimSpace(input.Username) == "" || len(input.Username) > 64) {
			return "", errors.New("用户名不能为空且不能超过 64 个字符")
		}
		if err := validateCredential(input); err != nil {
			return "", err
		}
	}
	if input.Transport == "" {
		input.Transport = "raw"
	}
	if protocol.PortForward && input.Transport != "raw" {
		return "", errors.New("端口转发只支持原生 TCP/UDP 传输")
	}
	if input.Transport != "raw" && input.Transport != "websocket" && input.Transport != "grpc" && input.Transport != "xhttp" {
		return "", errors.New("不支持的传输方式")
	}
	if input.Transport != "raw" && strings.TrimSpace(input.TransportPath) == "" {
		return "", errors.New("传输路径或 ServiceName 不能为空")
	}
	if len(input.TransportPath) > 256 {
		return "", errors.New("传输路径或 ServiceName 不能超过 256 个字符")
	}
	if (input.Transport == "websocket" || input.Transport == "xhttp") && !strings.HasPrefix(input.TransportPath, "/") {
		return "", errors.New("WebSocket / XHTTP 路径必须以 / 开头")
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
		vision := input.Protocol == ProtocolVLESS && input.Transport == "raw" && input.Flow == "xtls-rprx-vision"
		xhttp := input.Protocol == ProtocolVLESSXHTTP && input.Transport == "xhttp" && input.Flow == ""
		encTCP := input.Protocol == ProtocolVLESSEncTCP && input.Transport == "raw" && input.Flow == "xtls-rprx-vision"
		encXHTTP := input.Protocol == ProtocolVLESSEncXHTTP && input.Transport == "xhttp" && input.Flow == "xtls-rprx-vision"
		if !vision && !xhttp && !encTCP && !encXHTTP {
			return "", errors.New("Reality 方案的协议、传输与 Vision Flow 组合无效")
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
		if engine == core.EngineXray {
			if input.RealityMinClientVer == "" {
				input.RealityMinClientVer = "0.0.0"
			}
			if !validXrayVersion(input.RealityMinClientVer) {
				return "", errors.New("Reality 最低客户端版本必须是 x.y.z，且每段为 0 到 255")
			}
			if input.RealityMLDSA65Seed != "" && !validRawURLBase64Size(input.RealityMLDSA65Seed, 32) {
				return "", errors.New("Reality ML-DSA-65 Seed 必须是 32 字节 Raw URL Base64")
			}
			if input.RealityMLDSA65Verify != "" && !validRawURLBase64Size(input.RealityMLDSA65Verify, 1952) {
				return "", errors.New("Reality ML-DSA-65 Verify 必须是 1952 字节 Raw URL Base64")
			}
			if input.RealityMLDSA65Seed != "" && input.RealityMLDSA65Verify != "" {
				verify, _ := mldsa65VerifyFromSeed(input.RealityMLDSA65Seed)
				if verify != input.RealityMLDSA65Verify {
					return "", errors.New("Reality ML-DSA-65 Seed 与 Verify 不属于同一密钥对")
				}
			}
		}
	}
	if protocol.UsesVLESSEncryption {
		if input.VLESSEncryption == "" {
			input.VLESSEncryption, _ = vlessEncryptionFromDecryption(input.VLESSDecryption)
		}
		if err := validateVLESSEncryptionPair(input.VLESSDecryption, input.VLESSEncryption); err != nil {
			return "", err
		}
	}
	if input.Protocol == ProtocolSnell && (input.SnellVersion < 1 || input.SnellVersion > 5) {
		return "", errors.New("Snell 版本必须在 1 到 5 之间")
	}
	if input.Protocol == ProtocolSudoku {
		if input.SudokuPaddingMin < 0 || input.SudokuPaddingMin > 100 || input.SudokuPaddingMax < input.SudokuPaddingMin || input.SudokuPaddingMax > 100 {
			return "", errors.New("Sudoku Padding 必须在 0 到 100 之间，且最大值不能小于最小值")
		}
		if input.SudokuTableType == "" {
			input.SudokuTableType = "prefer_ascii"
		}
		switch input.SudokuTableType {
		case "prefer_ascii", "prefer_entropy", "up_ascii_down_entropy", "up_entropy_down_ascii":
		default:
			return "", errors.New("不支持的 Sudoku Table Type")
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
	if input.Protocol == ProtocolVLESS || input.Protocol == ProtocolVLESSXHTTP || isVLESSEncryptionProtocol(input.Protocol) || input.Protocol == ProtocolVMess || input.Protocol == ProtocolTUIC || input.Protocol == ProtocolSudoku {
		if !uuidPattern.MatchString(input.Credential) {
			return errors.New("VLESS/VMess/TUIC/Sudoku 用户凭据必须是有效 UUID")
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

func validXrayVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || value > 255 {
			return false
		}
	}
	return true
}

func validRawURLBase64Size(value string, size int) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == size
}
