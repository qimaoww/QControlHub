// Package serverconfig generates complete server configurations from a
// focused inbound definition. Each core encoder is kept in its own file.
package serverconfig

import "github.com/qimaoww/qcontrolhub/internal/core"

const (
	ProtocolShadowsocks    = "shadowsocks"
	ProtocolSS2022         = "ss2022"
	ProtocolVLESS          = "vless"
	ProtocolVLESSXHTTP     = "vless-xhttp-reality"
	ProtocolVLESSEncTCP    = "vless-enc-tcp-reality-vision"
	ProtocolVLESSEncXHTTP  = "vless-enc-xhttp-reality-vision"
	ProtocolVMess          = "vmess"
	ProtocolTrojan         = "trojan"
	ProtocolHy2            = "hysteria2"
	ProtocolTUIC           = "tuic"
	ProtocolAnyTLS         = "anytls"
	ProtocolSnell          = "snell"
	ProtocolSnellShadowTLS = "snell-shadow-tls-v3"
	ProtocolSudoku         = "sudoku"
	ProtocolPortForward    = "port-forward"
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
	UsesVLESSEncryption  bool     `json:"uses_vless_encryption"`
	PortForward          bool     `json:"port_forward"`
}

type Input struct {
	Protocol                 string `json:"protocol"`
	Tag                      string `json:"tag"`
	Listen                   string `json:"listen"`
	Port                     int    `json:"port"`
	Username                 string `json:"username"`
	Credential               string `json:"credential"`
	SecondaryCredential      string `json:"secondary_credential"`
	Method                   string `json:"method"`
	Flow                     string `json:"flow"`
	Transport                string `json:"transport"`
	TransportPath            string `json:"transport_path"`
	TLSEnabled               bool   `json:"tls_enabled"`
	CertificatePath          string `json:"certificate_path"`
	PrivateKeyPath           string `json:"private_key_path"`
	RealityEnabled           bool   `json:"reality_enabled"`
	RealityPrivateKey        string `json:"reality_private_key"`
	RealityPublicKey         string `json:"reality_public_key"`
	RealityShortID           string `json:"reality_short_id"`
	RealityServerName        string `json:"reality_server_name"`
	RealityMinClientVer      string `json:"reality_min_client_ver"`
	RealityMLDSA65Seed       string `json:"reality_mldsa65_seed"`
	RealityMLDSA65Verify     string `json:"reality_mldsa65_verify"`
	VLESSDecryption          string `json:"vless_decryption"`
	VLESSEncryption          string `json:"vless_encryption"`
	ListenerRoutingMark      int    `json:"listener_routing_mark"`
	ListenerRule             string `json:"listener_rule"`
	ListenerProxy            string `json:"listener_proxy"`
	SnellVersion             int    `json:"snell_version"`
	SnellUDP                 bool   `json:"snell_udp"`
	SnellReuse               bool   `json:"snell_reuse"`
	SnellObfsMode            string `json:"snell_obfs_mode"`
	SnellObfsHost            string `json:"snell_obfs_host"`
	SnellClientFingerprint   string `json:"snell_client_fingerprint"`
	SnellShadowTLSVersion    int    `json:"snell_shadow_tls_version"`
	SnellShadowTLSPassword   string `json:"snell_shadow_tls_password"`
	SnellShadowTLSUser       string `json:"snell_shadow_tls_user"`
	SnellShadowTLSHandshake  string `json:"snell_shadow_tls_handshake"`
	SnellShadowTLSProxy      string `json:"snell_shadow_tls_proxy"`
	SnellShadowTLSALPN       string `json:"snell_shadow_tls_alpn"`
	SudokuClientKey          string `json:"sudoku_client_key"`
	SudokuPaddingMin         int    `json:"sudoku_padding_min"`
	SudokuPaddingMax         int    `json:"sudoku_padding_max"`
	SudokuTableType          string `json:"sudoku_table_type"`
	SudokuHandshakeTimeout   int    `json:"sudoku_handshake_timeout"`
	SudokuEnablePureDownlink bool   `json:"sudoku_enable_pure_downlink"`
	SudokuHTTPMaskEnabled    bool   `json:"sudoku_httpmask_enabled"`
	SudokuHTTPMaskMode       string `json:"sudoku_httpmask_mode"`
	SudokuHTTPMaskTLS        bool   `json:"sudoku_httpmask_tls"`
	SudokuHTTPMaskHost       string `json:"sudoku_httpmask_host"`
	SudokuHTTPMaskPathRoot   string `json:"sudoku_httpmask_path_root"`
	SudokuMultiplex          string `json:"sudoku_multiplex"`
	SudokuFallback           string `json:"sudoku_fallback"`
	TargetAddress            string `json:"target_address"`
	TargetPort               int    `json:"target_port"`
	Network                  string `json:"network"`
	BlockMainlandDestination bool   `json:"block_mainland_destination"`
	BlockMainlandSource      bool   `json:"block_mainland_source"`
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
	if engine == core.EngineMihomo || engine == core.EngineXray {
		protocols = append(protocols,
			Protocol{
				Key: ProtocolVLESSEncTCP, Name: "VLESS-ENC-TCP-Reality-Vision", Badge: "VLESS ENC TCP",
				Description: "VLESS Encryption + Raw/TCP + Reality + Vision；自动生成独立的服务端 Decryption 与客户端 Encryption。",
				Docs:        base + vlessPath, DefaultPort: 443, Credential: "用户 UUID",
				Transports: []string{"raw"}, UsesReality: true, SupportsRealityMLDSA: engine == core.EngineXray, UsesVLESSEncryption: true,
			},
			Protocol{
				Key: ProtocolVLESSEncXHTTP, Name: "VLESS-ENC-XHTTP-Reality-Vision", Badge: "VLESS ENC XHTTP",
				Description: "VLESS Encryption + XHTTP + Reality + Vision；Vision 穿透 VLESS Encryption，不依赖 XHTTP 底层直拷。",
				Docs:        base + vlessPath, DefaultPort: 443, Credential: "用户 UUID",
				Transports: []string{"xhttp"}, UsesReality: true, SupportsRealityMLDSA: engine == core.EngineXray, UsesVLESSEncryption: true, TransportConfig: true,
			},
		)
	}
	if engine == core.EngineMihomo {
		protocols = append(protocols,
			Protocol{
				Key: ProtocolSnell, Name: "Snell v5", Badge: "SNELL v5",
				Description: "固定 Snell v5 的原生服务端，支持 UDP over TCP 与连接复用。",
				Docs:        base + "snell/", DefaultPort: 8443, Credential: "预共享密钥（PSK）",
				Transports: []string{"raw"}, IgnoresUsername: true,
			},
			Protocol{
				Key: ProtocolSnellShadowTLS, Name: "Snell v5 + ShadowTLS v3", Badge: "SNELL STLS",
				Description: "固定 Snell v5 与 ShadowTLS v3；自动生成独立用户与强密码，并生成严格校验证书的匹配客户端配置。",
				Docs:        base + "snell/", DefaultPort: 8443, Credential: "预共享密钥（PSK）",
				Transports: []string{"raw"}, IgnoresUsername: true,
			},
			Protocol{
				Key: ProtocolSudoku, Name: "Sudoku", Badge: "SUDOKU",
				Description: "按 SUDOKU-ASCII 上游生成 Ed25519 公私钥、AEAD、低熵字节表、HTTPMask 与原生复用。",
				Docs:        base + "sudoku/", DefaultPort: 8443, Credential: "Master Public Key（服务端）",
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
