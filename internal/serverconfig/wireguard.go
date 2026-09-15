package serverconfig

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
)

func newWireGuardSecret() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return base64.StdEncoding.EncodeToString(b), e
}

func newWireGuardKeyPair() (string, string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", "", e
	}
	// X25519 clamping, followed by scalar multiplication with the canonical base point.
	b[0] &= 248
	b[31] &= 127
	b[31] |= 64
	pub, err := x25519Basepoint(b)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(b), base64.StdEncoding.EncodeToString(pub), nil
}

func x25519Basepoint(private []byte) ([]byte, error) {
	k, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil {
		return nil, err
	}
	return k.PublicKey().Bytes(), nil
}

func parseWireGuardB64(value string) ([]byte, error) {
	b, e := base64.StdEncoding.DecodeString(value)
	if e != nil || len(b) != 32 || base64.StdEncoding.EncodeToString(b) != value {
		return nil, errors.New("WireGuard 密钥必须是 32 字节标准 Base64")
	}
	return b, nil
}

func splitWireGuardList(value string) []string {
	var out []string
	for _, v := range strings.Split(value, ",") {
		if s := strings.TrimSpace(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func validateWireGuardInput(input Input, requireClient bool) error {
	if input.Protocol != ProtocolWireGuard || !tagPattern.MatchString(input.Tag) || input.Port < 1 || input.Port > 65535 {
		return errors.New("WireGuard 协议、标签或端口无效")
	}
	if input.Transport != "" && input.Transport != "raw" || input.TLSEnabled || input.RealityEnabled {
		return errors.New("WireGuard 仅支持原生 UDP，不支持 TLS、Reality 或额外传输")
	}
	server, err := parseWireGuardB64(input.WireGuardServerPrivateKey)
	if err != nil {
		return fmt.Errorf("WireGuard 服务端私钥无效: %w", err)
	}
	serverKey, err := ecdh.X25519().NewPrivateKey(server)
	if err != nil {
		return errors.New("WireGuard 服务端私钥无效")
	}
	serverPublic := base64.StdEncoding.EncodeToString(serverKey.PublicKey().Bytes())
	if input.WireGuardServerPublicKey != "" && input.WireGuardServerPublicKey != serverPublic {
		return errors.New("WireGuard 服务端公私钥不匹配")
	}
	client, err := parseWireGuardB64(input.WireGuardClientPublicKey)
	if err != nil {
		return fmt.Errorf("WireGuard 客户端公钥无效: %w", err)
	}
	clientPublic, err := ecdh.X25519().NewPublicKey(client)
	if err != nil {
		return errors.New("WireGuard 客户端公钥无效")
	}
	if _, err := serverKey.ECDH(clientPublic); err != nil {
		return errors.New("WireGuard 客户端公钥不能用于密钥交换")
	}
	if serverPublic == input.WireGuardClientPublicKey {
		return errors.New("WireGuard 服务端与客户端必须使用独立密钥对")
	}
	if requireClient && input.WireGuardClientPrivateKey == "" {
		return errors.New("缺少此版本的 WireGuard 客户端私钥，请提供匹配私钥或重新生成密钥对")
	}
	if input.WireGuardClientPrivateKey != "" {
		if _, err := parseWireGuardB64(input.WireGuardClientPrivateKey); err != nil {
			return err
		}
		if wireguardServerPublic(input.WireGuardClientPrivateKey) != input.WireGuardClientPublicKey {
			return errors.New("WireGuard 客户端公私钥不匹配")
		}
	}
	if input.WireGuardPresharedKey != "" {
		if _, err := parseWireGuardB64(input.WireGuardPresharedKey); err != nil {
			return fmt.Errorf("WireGuard PSK 无效: %w", err)
		}
	}
	addresses := splitWireGuardList(input.WireGuardClientAddress)
	if len(addresses) == 0 || len(addresses) > 2 {
		return errors.New("WireGuard 客户端须填写一个 IPv4 地址和/或一个 IPv6 地址")
	}
	families := map[int]bool{}
	for _, cidr := range addresses {
		ip, subnet, err := net.ParseCIDR(cidr)
		if err != nil || !ip.IsGlobalUnicast() {
			return fmt.Errorf("WireGuard 客户端地址无效: %s", cidr)
		}
		ones, bits := subnet.Mask.Size()
		if ones != bits || families[bits] {
			return errors.New("WireGuard 单客户端地址须使用独立的 /32 或 /128；自定义网段请使用源配置编辑器")
		}
		families[bits] = true
	}
	for _, cidr := range splitWireGuardList(input.WireGuardAllowedIPs) {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("WireGuard 客户端 AllowedIPs 无效: %s", cidr)
		}
	}
	if input.WireGuardMTU < 576 || input.WireGuardMTU > 65535 || families[128] && input.WireGuardMTU < 1280 {
		return errors.New("WireGuard MTU 须在 576 到 65535 之间；IPv6 至少需要 1280")
	}
	if input.WireGuardKeepalive < 0 || input.WireGuardKeepalive > 65535 {
		return errors.New("WireGuard Keepalive 须在 0 到 65535 秒之间")
	}
	return nil
}

func wireguardServerPublic(private string) string {
	b, e := parseWireGuardB64(private)
	if e != nil {
		return ""
	}
	k, e := ecdh.X25519().NewPrivateKey(b)
	if e != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(k.PublicKey().Bytes())
}
