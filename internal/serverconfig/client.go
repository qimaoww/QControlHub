package serverconfig

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// BuildClientProfile converts a generated inbound into client-facing access
// data. address is deliberately separate from Input.Listen: wildcard listener
// addresses such as 0.0.0.0 and :: are never valid remote destinations.
func BuildClientProfile(input Input, address, serverName string) (ClientProfile, error) {
	return BuildClientProfileNamed(input, address, serverName, "")
}

// BuildClientProfileNamed is the client-profile builder with an optional
// operator-facing node name. An empty name preserves the inbound tag.
func BuildClientProfileNamed(input Input, address, serverName, nodeName string) (ClientProfile, error) {
	if input.RealityMLDSA65Verify == "" && input.RealityMLDSA65Seed != "" {
		input.RealityMLDSA65Verify, _ = mldsa65VerifyFromSeed(input.RealityMLDSA65Seed)
	}
	if isVLESSEncryptionProtocol(input.Protocol) {
		if input.VLESSEncryption == "" {
			input.VLESSEncryption, _ = vlessEncryptionFromDecryption(input.VLESSDecryption)
		}
		if err := validateVLESSEncryptionPair(input.VLESSDecryption, input.VLESSEncryption); err != nil {
			return ClientProfile{}, err
		}
	}
	if isSnellProtocol(input.Protocol) {
		if err := validateSnellPresetIdentity(input); err != nil {
			return ClientProfile{}, err
		}
		normalizeSnellInput(&input)
		if err := validateSnellInput(input); err != nil {
			return ClientProfile{}, err
		}
	}
	if input.Protocol == ProtocolSudoku {
		normalizeSudokuInput(&input)
		if err := validateSudokuInput(input); err != nil {
			return ClientProfile{}, err
		}
	}
	address, err := NormalizeClientAddress(address)
	if err != nil {
		return ClientProfile{}, err
	}
	if input.Port < 1 || input.Port > 65535 {
		return ClientProfile{}, errors.New("客户端接入端口必须在 1 到 65535 之间")
	}
	if input.Protocol == ProtocolWireGuard {
		if err := validateWireGuardInput(input, true); err != nil {
			return ClientProfile{}, err
		}
	} else if input.Protocol == ProtocolTailscale {
		return ClientProfile{}, errors.New("Tailscale 端点通过 sing-box 登录状态管理，没有通用客户端配置")
	} else if input.Protocol == ProtocolOpenVPNServer {
		if strings.TrimSpace(input.OpenVPNClientCAPath) == "" || strings.TrimSpace(input.OpenVPNUsername) == "" || strings.TrimSpace(input.OpenVPNPassword) == "" {
			return ClientProfile{}, errors.New("OpenVPN 客户端证书或凭据不完整")
		}
	} else if err := validateCredential(input); err != nil {
		return ClientProfile{}, err
	}
	if input.Transport == "" {
		input.Transport = "raw"
	}
	if input.RealityEnabled {
		serverName = input.RealityServerName
	} else if input.TLSEnabled && strings.TrimSpace(serverName) == "" {
		serverName = address
	} else if !input.TLSEnabled {
		serverName = ""
	}
	if serverName != "" {
		serverName, err = NormalizeClientAddress(serverName)
		if err != nil {
			return ClientProfile{}, errors.New("TLS ServerName 必须是有效域名或 IP 地址")
		}
	}

	profile := ClientProfile{Fields: clientFields(input, address, serverName)}
	host := net.JoinHostPort(address, strconv.Itoa(input.Port))
	transport := clientTransport(input.Transport)
	fragment := input.Tag
	if fragment == "" {
		fragment = input.Protocol
	}
	if strings.TrimSpace(nodeName) != "" {
		fragment = strings.TrimSpace(nodeName)
	}

	switch input.Protocol {
	case ProtocolOpenVPNServer:
		profile.Format = "OpenVPN native config"
		profile.URI = "client\nproto udp\nremote " + address + " " + strconv.Itoa(input.Port) + "\ndev tun\nca " + input.OpenVPNClientCAPath + "\nauth-user-pass\n<auth-user-pass>\n" + input.OpenVPNUsername + "\n" + input.OpenVPNPassword + "\n</auth-user-pass>\n"
		profile.SubscriptionCompatible = false
		return profile, nil
	case ProtocolWireGuard:
		profile.Format = "WireGuard native config"
		profile.URI, err = wireguardClientConfig(input, address)
		if err != nil {
			return ClientProfile{}, err
		}
		profile.SubscriptionCompatible = false
	case ProtocolShadowsocks, ProtocolSS2022:
		identity := base64.RawURLEncoding.EncodeToString([]byte(input.Method + ":" + input.Credential))
		profile.Format = "Shadowsocks SIP002 URI"
		profile.URI = (&url.URL{Scheme: "ss", User: url.User(identity), Host: host, Fragment: fragment}).String()
		profile.SubscriptionCompatible = true
	case ProtocolVLESS, ProtocolVLESSXHTTP, ProtocolVLESSEncTCP, ProtocolVLESSEncXHTTP, ProtocolVLESSEncPlain:
		encryption := "none"
		if isVLESSEncryptionProtocol(input.Protocol) {
			encryption = input.VLESSEncryption
		}
		query := url.Values{"encryption": {encryption}, "type": {transport}}
		if input.Flow != "" {
			query.Set("flow", input.Flow)
		}
		if input.RealityEnabled {
			query.Set("security", "reality")
			query.Set("sni", serverName)
			query.Set("fp", "chrome")
			query.Set("pbk", input.RealityPublicKey)
			query.Set("sid", input.RealityShortID)
			if input.RealityMLDSA65Verify != "" {
				query.Set("pqv", input.RealityMLDSA65Verify)
			}
		} else if input.TLSEnabled {
			query.Set("security", "tls")
			query.Set("sni", serverName)
		}
		addTransportQuery(query, input)
		profile.Format = "VLESS URI"
		profile.URI = (&url.URL{Scheme: "vless", User: url.User(input.Credential), Host: host, RawQuery: query.Encode(), Fragment: fragment}).String()
		profile.SubscriptionCompatible = true
	case ProtocolSnell, ProtocolSnellShadowTLS:
		profile.Format = "Surge Snell"
		if input.Protocol == ProtocolSnellShadowTLS {
			profile.Format = "Surge Snell + ShadowTLS"
		}
		profile.URI = buildSnellSurgeConfig(input, address, fragment)
		profile.SubscriptionCompatible = true
	case ProtocolSudoku:
		profile.Format = "Mihomo Sudoku YAML"
		profile.URI, err = buildSudokuMihomoYAML(input, address, fragment)
		if err != nil {
			return ClientProfile{}, err
		}
		profile.SubscriptionCompatible = true
	case ProtocolVMess:
		payload := struct {
			Version string `json:"v"`
			Name    string `json:"ps"`
			Address string `json:"add"`
			Port    string `json:"port"`
			ID      string `json:"id"`
			AlterID string `json:"aid"`
			Cipher  string `json:"scy"`
			Network string `json:"net"`
			Type    string `json:"type"`
			Host    string `json:"host"`
			Path    string `json:"path"`
			TLS     string `json:"tls"`
			SNI     string `json:"sni"`
		}{
			Version: "2", Name: fragment, Address: address, Port: strconv.Itoa(input.Port), ID: input.Credential,
			AlterID: "0", Cipher: "auto", Network: transport, Type: "none", Path: input.TransportPath,
		}
		if input.Transport == "websocket" {
			payload.Host = serverName
		}
		if input.TLSEnabled {
			payload.TLS = "tls"
			payload.SNI = serverName
		}
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return ClientProfile{}, marshalErr
		}
		profile.Format = "VMess v2 JSON URI"
		profile.URI = "vmess://" + base64.StdEncoding.EncodeToString(encoded)
		profile.SubscriptionCompatible = true
	case ProtocolTrojan:
		query := url.Values{"type": {transport}}
		if input.TLSEnabled {
			query.Set("security", "tls")
			query.Set("sni", serverName)
		}
		addTransportQuery(query, input)
		profile.Format = "Trojan URI"
		profile.URI = (&url.URL{Scheme: "trojan", User: url.User(input.Credential), Host: host, RawQuery: query.Encode(), Fragment: fragment}).String()
		profile.SubscriptionCompatible = true
	case ProtocolHy2:
		query := url.Values{}
		if serverName != "" {
			query.Set("sni", serverName)
		}
		profile.Format = "Hysteria 2 URI"
		profile.URI = (&url.URL{Scheme: "hysteria2", User: url.User(input.Credential), Host: host, RawQuery: query.Encode(), Fragment: fragment}).String()
		profile.SubscriptionCompatible = true
	case ProtocolTUIC:
		query := url.Values{"congestion_control": {"bbr"}, "udp_relay_mode": {"native"}}
		if serverName != "" {
			query.Set("sni", serverName)
		}
		profile.Format = "TUIC v5 URI"
		profile.URI = (&url.URL{Scheme: "tuic", User: url.UserPassword(input.Credential, input.SecondaryCredential), Host: host, RawQuery: query.Encode(), Fragment: fragment}).String()
		profile.SubscriptionCompatible = true
	case ProtocolAnyTLS:
		query := url.Values{"security": {"tls"}}
		if serverName != "" {
			query.Set("sni", serverName)
		}
		profile.Format = "AnyTLS URI"
		profile.URI = (&url.URL{Scheme: "anytls", User: url.User(input.Credential), Host: host, RawQuery: query.Encode(), Fragment: fragment}).String()
		profile.SubscriptionCompatible = true
	default:
		return ClientProfile{}, errors.New("不支持生成此协议的客户端接入资料")
	}
	profile.Mihomo, err = buildMihomoClientYAML(input, address, serverName, fragment)
	if err != nil {
		// URL export remains available for protocols whose security options
		// cannot be represented by Mihomo without losing information.
		profile.MihomoError = err.Error()
	}
	return profile, nil
}
