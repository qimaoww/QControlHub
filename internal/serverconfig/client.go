package serverconfig

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ClientField is one exact value a client needs to reach a generated inbound.
// Secret fields must remain masked by callers until the administrator reveals
// or copies them.
type ClientField struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// ClientProfile contains a common client import URI plus the same connection
// data as explicit fields. The explicit fields remain useful when a particular
// client does not implement the URI scheme.
type ClientProfile struct {
	Format                 string        `json:"format"`
	URI                    string        `json:"uri"`
	SubscriptionCompatible bool          `json:"subscription_compatible"`
	Fields                 []ClientField `json:"fields"`
}

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
	address, err := NormalizeClientAddress(address)
	if err != nil {
		return ClientProfile{}, err
	}
	if input.Port < 1 || input.Port > 65535 {
		return ClientProfile{}, errors.New("客户端接入端口必须在 1 到 65535 之间")
	}
	if err := validateCredential(input); err != nil {
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
	case ProtocolShadowsocks, ProtocolSS2022:
		identity := base64.RawURLEncoding.EncodeToString([]byte(input.Method + ":" + input.Credential))
		profile.Format = "Shadowsocks SIP002 URI"
		profile.URI = (&url.URL{Scheme: "ss", User: url.User(identity), Host: host, Fragment: fragment}).String()
		profile.SubscriptionCompatible = true
	case ProtocolVLESS, ProtocolVLESSXHTTP:
		query := url.Values{"encryption": {"none"}, "type": {transport}}
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
	case ProtocolSnell:
		value, marshalErr := yaml.Marshal(map[string]any{"name": fragment, "type": "snell", "server": address, "port": input.Port, "psk": input.Credential, "version": input.SnellVersion, "udp": true})
		if marshalErr != nil {
			return ClientProfile{}, marshalErr
		}
		profile.Format = "Mihomo Snell YAML"
		profile.URI = string(value)
	case ProtocolSudoku:
		value, marshalErr := yaml.Marshal(map[string]any{
			"name": fragment, "type": "sudoku", "server": address, "port": input.Port, "key": input.Credential,
			"aead-method": input.Method, "padding-min": input.SudokuPaddingMin, "padding-max": input.SudokuPaddingMax,
			"table-type": input.SudokuTableType, "enable-pure-downlink": false,
			"httpmask": map[string]any{"disable": false, "mode": "legacy"},
		})
		if marshalErr != nil {
			return ClientProfile{}, marshalErr
		}
		profile.Format = "Mihomo Sudoku YAML"
		profile.URI = string(value)
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
	return profile, nil
}

// NormalizeClientAddress validates and canonicalizes the address that will be
// embedded in a client connection profile. Wildcard and loopback listeners
// are deliberately rejected because they are not reachable client targets.
func NormalizeClientAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	}
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/@?#%") {
		return "", errors.New("客户端连接地址必须是有效域名或 IP 地址")
	}
	if ip := net.ParseIP(value); ip != nil {
		if ip.IsUnspecified() {
			return "", errors.New("客户端连接地址不能使用 0.0.0.0 或 :: 等监听地址")
		}
		return ip.String(), nil
	}
	value = strings.TrimSuffix(strings.ToLower(value), ".")
	if value == "" {
		return "", errors.New("客户端连接地址必须是有效域名或 IP 地址")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("客户端连接地址必须是有效域名或 IP 地址")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", errors.New("客户端连接地址必须是有效域名或 IP 地址")
			}
		}
	}
	return value, nil
}

func clientTransport(value string) string {
	switch value {
	case "websocket":
		return "ws"
	case "grpc":
		return "grpc"
	case "xhttp":
		return "xhttp"
	default:
		return "tcp"
	}
}

func addTransportQuery(query url.Values, input Input) {
	switch input.Transport {
	case "websocket":
		query.Set("path", input.TransportPath)
	case "grpc":
		query.Set("serviceName", input.TransportPath)
	case "xhttp":
		query.Set("path", input.TransportPath)
		query.Set("mode", "auto")
	}
}

func clientFields(input Input, address, serverName string) []ClientField {
	fields := []ClientField{
		{Label: "协议", Value: input.Protocol},
		{Label: "服务器", Value: address},
		{Label: "端口", Value: strconv.Itoa(input.Port)},
	}
	if input.Username != "" && input.Username != "default" {
		fields = append(fields, ClientField{Label: "用户备注", Value: input.Username})
	}
	credentialLabel := "密码"
	switch input.Protocol {
	case ProtocolShadowsocks:
		credentialLabel = "密码"
	case ProtocolSS2022:
		credentialLabel = "Base64 PSK"
	case ProtocolVLESS, ProtocolVLESSXHTTP, ProtocolVMess, ProtocolTUIC, ProtocolSudoku:
		credentialLabel = "用户 UUID"
	}
	fields = append(fields, ClientField{Label: credentialLabel, Value: input.Credential, Secret: true})
	if input.SecondaryCredential != "" {
		fields = append(fields, ClientField{Label: "用户密码", Value: input.SecondaryCredential, Secret: true})
	}
	if input.Method != "" {
		fields = append(fields, ClientField{Label: "加密方法", Value: input.Method})
	}
	fields = append(fields, ClientField{Label: "传输", Value: input.Transport})
	if input.TransportPath != "" {
		fields = append(fields, ClientField{Label: "路径 / ServiceName", Value: input.TransportPath})
	}
	security := "无"
	if input.RealityEnabled {
		security = "Reality"
	} else if input.TLSEnabled {
		security = "TLS"
	}
	fields = append(fields, ClientField{Label: "传输安全", Value: security})
	if serverName != "" {
		fields = append(fields, ClientField{Label: "TLS ServerName", Value: serverName})
	}
	if input.RealityEnabled {
		fields = append(fields,
			ClientField{Label: "Reality Public Key", Value: input.RealityPublicKey},
			ClientField{Label: "Reality Short ID", Value: input.RealityShortID},
			ClientField{Label: "客户端指纹", Value: "chrome"},
		)
		if input.RealityMLDSA65Verify != "" {
			fields = append(fields, ClientField{Label: "ML-DSA-65 Verify", Value: input.RealityMLDSA65Verify})
		}
	}
	return fields
}
