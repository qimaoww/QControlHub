// Package serverconfig generates complete server configurations from a
// focused inbound definition. Each core encoder is kept in its own file.
package serverconfig

import "github.com/qimaoww/qcontrolhub/internal/core"

const (
	ProtocolShadowsocks = "shadowsocks"
	ProtocolSS2022      = "ss2022"
	ProtocolVLESS       = "vless"
	ProtocolVLESSXHTTP  = "vless-xhttp-reality"
	ProtocolVMess       = "vmess"
	ProtocolTrojan      = "trojan"
	ProtocolHy2         = "hysteria2"
	ProtocolTUIC        = "tuic"
	ProtocolAnyTLS      = "anytls"
	ProtocolSnell       = "snell"
	ProtocolSudoku      = "sudoku"
	ProtocolPortForward = "port-forward"
)

type Protocol struct {
	Key                  string   `json:"key"`
	Name                 string   `json:"name"`
	Badge                string   `json:"badge"`
	Description          string   `json:"description"`
	Docs                 string   `json:"docs"`
	DefaultPort          int      `json:"default_port"`
	Credential           string   `json:"credential_label"`
	SecondaryCredential  string   `json:"secondary_credential_label"`
	IgnoresUsername      bool     `json:"ignores_username"`
	Methods              []string `json:"methods"`
	Transports           []string `json:"transports"`
	SupportsTLS          bool     `json:"supports_tls"`
	RequiresTLS          bool     `json:"requires_tls"`
	DefaultTLS           bool     `json:"default_tls"`
	TransportConfig      bool     `json:"transport_config"`
	UsesReality          bool     `json:"uses_reality"`
	SupportsRealityMLDSA bool     `json:"supports_reality_mldsa"`
	PortForward          bool     `json:"port_forward"`
}

type Input struct {
	Protocol             string `json:"protocol"`
	Tag                  string `json:"tag"`
	Listen               string `json:"listen"`
	Port                 int    `json:"port"`
	Username             string `json:"username"`
	Credential           string `json:"credential"`
	SecondaryCredential  string `json:"secondary_credential"`
	Method               string `json:"method"`
	Flow                 string `json:"flow"`
	Transport            string `json:"transport"`
	TransportPath        string `json:"transport_path"`
	TLSEnabled           bool   `json:"tls_enabled"`
	CertificatePath      string `json:"certificate_path"`
	PrivateKeyPath       string `json:"private_key_path"`
	RealityEnabled       bool   `json:"reality_enabled"`
	RealityPrivateKey    string `json:"reality_private_key"`
	RealityPublicKey     string `json:"reality_public_key"`
	RealityShortID       string `json:"reality_short_id"`
	RealityServerName    string `json:"reality_server_name"`
	RealityMinClientVer  string `json:"reality_min_client_ver"`
	RealityMLDSA65Seed   string `json:"reality_mldsa65_seed"`
	RealityMLDSA65Verify string `json:"reality_mldsa65_verify"`
	SnellVersion         int    `json:"snell_version"`
	SudokuPaddingMin     int    `json:"sudoku_padding_min"`
	SudokuPaddingMax     int    `json:"sudoku_padding_max"`
	SudokuTableType      string `json:"sudoku_table_type"`
	TargetAddress        string `json:"target_address"`
	TargetPort           int    `json:"target_port"`
	Network              string `json:"network"`
}

func Protocols(engine core.Engine) []Protocol {
	if engine == core.EngineShadowsocksRust {
		return []Protocol{
			{
				Key: ProtocolShadowsocks, Name: "Shadowsocks", Badge: "SS",
				Description: "Shadowsocks Rust 标准 AEAD 服务端，默认同时监听 TCP 与 UDP。",
				Docs:        "https://shadowsocks.org/doc/configs.html", DefaultPort: 8388, Credential: "服务端密码",
				Methods:    []string{"chacha20-ietf-poly1305", "aes-256-gcm", "aes-128-gcm"},
				Transports: []string{"raw"}, IgnoresUsername: true,
			},
			{
				Key: ProtocolSS2022, Name: "Shadowsocks 2022", Badge: "SS2022",
				Description: "Shadowsocks Rust 的 AEAD-2022 服务端，使用标准 Base64 PSK。",
				Docs:        "https://shadowsocks.org/doc/configs.html", DefaultPort: 8388, Credential: "Base64 PSK",
				Methods:    []string{"2022-blake3-aes-256-gcm", "2022-blake3-aes-128-gcm", "2022-blake3-chacha20-poly1305"},
				Transports: []string{"raw"}, IgnoresUsername: true,
			},
		}
	}
	base := ""
	switch engine {
	case core.EngineMihomo:
		base = "https://wiki.metacubex.one/config/inbound/listeners/"
	case core.EngineXray:
		base = "https://xtls.github.io/config/inbounds/"
	case core.EngineSingBox:
		base = "https://sing-box.sagernet.org/configuration/inbound/"
	default:
		return nil
	}
	ssPath := "shadowsocks/"
	vlessPath := "vless/"
	vmessPath := "vmess/"
	trojanPath := "trojan/"
	hy2Path := "hysteria2/"
	portForwardPath := "direct/"
	if engine == core.EngineMihomo {
		ssPath = "ss/"
		portForwardPath = "tunnel/"
	}
	if engine == core.EngineXray {
		ssPath = "shadowsocks.html"
		vlessPath = "vless.html"
		vmessPath = "vmess.html"
		trojanPath = "trojan.html"
		hy2Path = "hysteria.html"
		portForwardPath = "tunnel.html"
	}
	protocols := []Protocol{
		{
			Key: ProtocolSS2022, Name: "Shadowsocks 2022", Badge: "SS2022",
			Description: "基于 BLAKE3 的现代 Shadowsocks 服务端，默认同时监听 TCP 与 UDP。",
			Docs:        base + ssPath, DefaultPort: 8388, Credential: "Base64 PSK",
			Methods:    []string{"2022-blake3-aes-256-gcm", "2022-blake3-aes-128-gcm", "2022-blake3-chacha20-poly1305"},
			Transports: []string{"raw"}, IgnoresUsername: true,
		},
		{
			Key: ProtocolVLESS, Name: "VLESS", Badge: "VLESS",
			Description: "自动生成 UUID、X25519 密钥对与 Short ID 的 VLESS Vision + Reality 方案。",
			Docs:        base + vlessPath, DefaultPort: 443, Credential: "用户 UUID",
			Transports: []string{"raw"}, UsesReality: true, SupportsRealityMLDSA: engine == core.EngineXray,
		},
		{
			Key: ProtocolVMess, Name: "VMess", Badge: "VMESS",
			Description: "UUID 用户认证的 VMess 服务端，可配置 TLS 与 WebSocket/gRPC 传输。",
			Docs:        base + vmessPath, DefaultPort: 443, Credential: "用户 UUID",
			Transports: []string{"raw", "websocket", "grpc"}, SupportsTLS: true, DefaultTLS: true, TransportConfig: true,
		},
		{
			Key: ProtocolTrojan, Name: "Trojan", Badge: "TROJAN",
			Description: "基于 TLS 的密码认证服务端，可使用 Raw、WebSocket 或 gRPC。",
			Docs:        base + trojanPath, DefaultPort: 443, Credential: "用户密码",
			Transports: []string{"raw", "websocket", "grpc"}, SupportsTLS: true, RequiresTLS: true, DefaultTLS: true, TransportConfig: true,
		},
		{
			Key: ProtocolHy2, Name: "Hysteria 2", Badge: "HY2",
			Description: "基于 QUIC 的高性能入站，可承载 TCP 与 UDP 流量，默认带宽为双向 100 Mbps。",
			Docs:        base + hy2Path, DefaultPort: 8443, Credential: "用户密码",
			Transports: []string{"raw"}, SupportsTLS: true, RequiresTLS: true, DefaultTLS: true,
		},
		{
			Key: ProtocolPortForward, Name: "端口转发", Badge: "FORWARD",
			Description: "将本机监听端口收到的 TCP、UDP 或双协议流量转发到指定目标地址与端口。",
			Docs:        base + portForwardPath, DefaultPort: 8080,
			Transports: []string{"raw"}, IgnoresUsername: true, PortForward: true,
		},
	}
	if engine == core.EngineMihomo || engine == core.EngineXray {
		protocols = append(protocols, Protocol{
			Key: ProtocolVLESSXHTTP, Name: "VLESS-XHTTP-Reality", Badge: "VLESS XHTTP",
			Description: "独立的 VLESS XHTTP + Reality 方案；自动生成 UUID、X25519 密钥、Short ID 与随机路径。",
			Docs:        "https://github.com/XTLS/Xray-examples/tree/main/VLESS-XHTTP-Reality/minimal-steal_others",
			DefaultPort: 443, Credential: "用户 UUID", Transports: []string{"xhttp"},
			UsesReality: true, SupportsRealityMLDSA: engine == core.EngineXray, TransportConfig: true,
		})
	}
	if engine == core.EngineMihomo {
		protocols = append(protocols,
			Protocol{
				Key: ProtocolSnell, Name: "Snell", Badge: "SNELL",
				Description: "Snell v1-v5 服务端，默认使用 v4 并启用 UDP over TCP。",
				Docs:        base + "snell/", DefaultPort: 8443, Credential: "预共享密钥（PSK）",
				Transports: []string{"raw"}, IgnoresUsername: true,
			},
			Protocol{
				Key: ProtocolSudoku, Name: "Sudoku", Badge: "SUDOKU",
				Description: "Sudoku 服务端，默认使用 UUID 共享密钥、ChaCha20-Poly1305 与 ASCII 优先表。",
				Docs:        base + "sudoku/", DefaultPort: 8443, Credential: "共享 UUID / 服务端公钥",
				Methods: []string{"chacha20-poly1305", "aes-128-gcm"}, Transports: []string{"raw"}, IgnoresUsername: true,
			},
		)
	}
	if engine != core.EngineXray {
		tuicPath, anyTLSPath := "tuic/", "anytls/"
		if engine == core.EngineMihomo {
			tuicPath = "tuic-v5/"
		}
		protocols = append(protocols,
			Protocol{
				Key: ProtocolTUIC, Name: "TUIC v5", Badge: "TUIC",
				Description: "采用 UUID 与密码双重认证的 QUIC 入站，默认凭据均由系统随机生成。",
				Docs:        base + tuicPath, DefaultPort: 8443, Credential: "用户 UUID", SecondaryCredential: "用户密码",
				Transports: []string{"raw"}, SupportsTLS: true, RequiresTLS: true, DefaultTLS: true, IgnoresUsername: engine == core.EngineMihomo,
			},
			Protocol{
				Key: ProtocolAnyTLS, Name: "AnyTLS", Badge: "ANYTLS",
				Description: "基于 TLS 的轻量密码认证入站，默认生成随机用户名和密码。",
				Docs:        base + anyTLSPath, DefaultPort: 443, Credential: "用户密码",
				Transports: []string{"raw"}, SupportsTLS: true, RequiresTLS: true, DefaultTLS: true,
			},
		)
	}
	return protocols
}

func FindProtocol(engine core.Engine, key string) (Protocol, bool) {
	for _, protocol := range Protocols(engine) {
		if protocol.Key == key {
			return protocol, true
		}
	}
	return Protocol{}, false
}
