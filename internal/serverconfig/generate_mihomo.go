package serverconfig

import (
	"encoding/hex"
	"net"
	"strings"

	"filippo.io/edwards25519"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

func generateMihomo(input Input) (string, error) {
	listener := map[string]any{
		"name": input.Tag, "listen": input.Listen, "port": input.Port,
	}
	if input.ListenerRoutingMark > 0 {
		listener["routing-mark"] = input.ListenerRoutingMark
	}
	if input.ListenerRule != "" {
		listener["rule"] = input.ListenerRule
	}
	if input.ListenerProxy != "" {
		listener["proxy"] = input.ListenerProxy
	}
	switch input.Protocol {
	case ProtocolSS2022:
		listener["type"] = "shadowsocks"
		listener["cipher"] = input.Method
		listener["password"] = input.Credential
		listener["udp"] = true
	case ProtocolVLESS, ProtocolVLESSXHTTP, ProtocolVLESSEncTCP, ProtocolVLESSEncXHTTP:
		listener["type"] = "vless"
		user := map[string]any{"username": input.Username, "uuid": input.Credential}
		if input.Flow != "" {
			user["flow"] = input.Flow
		}
		listener["users"] = []any{user}
		if isVLESSEncryptionProtocol(input.Protocol) {
			listener["decryption"] = input.VLESSDecryption
		}
		mihomoTransport(listener, input)
	case ProtocolSnell, ProtocolSnellShadowTLS:
		listener["type"] = "snell"
		listener["psk"] = input.Credential
		listener["version"] = input.SnellVersion
		listener["udp"] = input.SnellUDP
		switch input.SnellObfsMode {
		case SnellObfsShadowTLS:
			shadowTLS := map[string]any{
				"enable": true, "version": input.SnellShadowTLSVersion,
				"handshake": compactMap(map[string]any{"dest": input.SnellShadowTLSHandshake, "proxy": input.SnellShadowTLSProxy}),
			}
			if input.SnellShadowTLSVersion == 2 {
				shadowTLS["password"] = input.SnellShadowTLSPassword
			}
			if input.SnellShadowTLSVersion == 3 {
				shadowTLS["strict-mode"] = true
				shadowTLS["users"] = []any{map[string]any{"name": input.SnellShadowTLSUser, "password": input.SnellShadowTLSPassword}}
			}
			listener["shadow-tls"] = shadowTLS
		}
	case ProtocolSudoku:
		listener["type"] = "sudoku"
		listener["key"] = input.Credential
		listener["aead-method"] = input.Method
		listener["padding-min"] = input.SudokuPaddingMin
		listener["padding-max"] = input.SudokuPaddingMax
		listener["table-type"] = input.SudokuTableType
		listener["handshake-timeout"] = input.SudokuHandshakeTimeout
		listener["enable-pure-downlink"] = input.SudokuEnablePureDownlink
		listener["httpmask"] = compactMap(map[string]any{
			"disable": !input.SudokuHTTPMaskEnabled, "mode": "auto",
			"path-root": input.SudokuHTTPMaskPathRoot,
		})
		if input.SudokuFallback != "" {
			listener["fallback"] = input.SudokuFallback
		}
	case ProtocolVMess:
		listener["type"] = "vmess"
		listener["users"] = []any{map[string]any{"username": input.Username, "uuid": input.Credential, "alterId": 0}}
		mihomoTransport(listener, input)
	case ProtocolTrojan:
		listener["type"] = "trojan"
		listener["users"] = []any{map[string]any{"username": input.Username, "password": input.Credential}}
		mihomoTransport(listener, input)
	case ProtocolHy2:
		listener["type"] = "hysteria2"
		listener["users"] = map[string]any{input.Username: input.Credential}
		listener["up"], listener["down"] = 100, 100
		listener["alpn"] = []string{"h3"}
	case ProtocolTUIC:
		listener["type"] = "tuic"
		listener["users"] = map[string]any{input.Credential: input.SecondaryCredential}
		listener["congestion-controller"] = "bbr"
		listener["max-idle-time"] = 15000
		listener["authentication-timeout"] = 1000
		listener["alpn"] = []string{"h3"}
	case ProtocolAnyTLS:
		listener["type"] = "anytls"
		listener["users"] = map[string]any{input.Username: input.Credential}
	case ProtocolPortForward:
		listener["type"] = "tunnel"
		listener["network"] = strings.Split(input.Network, ",")
		listener["target"] = forwardTarget(input)
	}
	if input.TLSEnabled {
		listener["certificate"] = input.CertificatePath
		listener["private-key"] = input.PrivateKeyPath
	}
	if input.RealityEnabled {
		listener["reality-config"] = map[string]any{
			"dest": input.RealityServerName + ":443", "private-key": input.RealityPrivateKey,
			"short-id": []string{input.RealityShortID}, "server-names": []string{input.RealityServerName},
		}
	}
	root := map[string]any{
		"log-level": "info", "listeners": []any{listener}, "rules": []string{"MATCH,DIRECT"},
	}
	if err := applyMainlandMihomo(root, input.Tag, input.BlockMainlandDestination, input.BlockMainlandSource); err != nil {
		return "", err
	}
	value, err := yaml.Marshal(root)
	return string(value), err
}

func parseMihomo(content string) (Input, bool) {
	var root map[string]any
	if yaml.Unmarshal([]byte(content), &root) != nil {
		return Input{}, false
	}
	listener := firstSupportedInbound(core.EngineMihomo, root["listeners"], "type")
	if listener == nil {
		return Input{}, false
	}
	input := Input{
		Protocol: protocolKey(stringValue(listener["type"])), Tag: stringValue(listener["name"]),
		Listen: stringValue(listener["listen"]), Port: intValue(listener["port"]), Username: "default",
		Method: stringValue(listener["cipher"]), Credential: stringValue(listener["password"]), Transport: "raw",
		CertificatePath: stringValue(listener["certificate"]), PrivateKeyPath: stringValue(listener["private-key"]),
	}
	input.ListenerRoutingMark = intValue(listener["routing-mark"])
	input.ListenerRule = stringValue(listener["rule"])
	input.ListenerProxy = stringValue(listener["proxy"])
	input.VLESSDecryption = stringValue(listener["decryption"])
	if input.Protocol == ProtocolSnell {
		input.Credential = stringValue(listener["psk"])
		input.SnellVersion = intValue(listener["version"])
		if input.SnellVersion == 0 {
			input.SnellVersion = 4
		}
		if input.SnellVersion != 5 {
			return Input{}, false
		}
		input.SnellUDP = true
		if value, present := listener["udp"]; present {
			input.SnellUDP = boolValue(value)
		}
		input.SnellObfsMode = SnellObfsNone
		input.SnellClientFingerprint = "chrome"
		input.SnellShadowTLSALPN = "h2,http/1.1"
		if obfs := mapValue(listener["obfs-opts"]); obfs != nil {
			input.SnellObfsMode = stringValue(obfs["mode"])
			input.SnellObfsHost = stringValue(obfs["host"])
		}
		if shadowTLS := mapValue(listener["shadow-tls"]); boolValue(shadowTLS["enable"]) {
			input.SnellObfsMode = SnellObfsShadowTLS
			input.SnellShadowTLSVersion = intValue(shadowTLS["version"])
			input.SnellShadowTLSPassword = stringValue(shadowTLS["password"])
			if user := firstMap(shadowTLS["users"]); user != nil {
				input.SnellShadowTLSUser = stringValue(user["name"])
				input.SnellShadowTLSPassword = stringValue(user["password"])
			}
			handshake := mapValue(shadowTLS["handshake"])
			input.SnellShadowTLSHandshake = stringValue(handshake["dest"])
			input.SnellShadowTLSProxy = stringValue(handshake["proxy"])
			input.SnellObfsHost = endpointHost(input.SnellShadowTLSHandshake)
		}
		if input.SnellObfsMode == SnellObfsShadowTLS && input.SnellShadowTLSVersion == 3 {
			input.Protocol = ProtocolSnellShadowTLS
		} else if input.SnellObfsMode != SnellObfsNone {
			return Input{}, false
		}
	}
	if input.Protocol == ProtocolSudoku {
		input.Credential = stringValue(listener["key"])
		publicKey, keyErr := hex.DecodeString(input.Credential)
		if keyErr != nil || len(publicKey) != 32 {
			return Input{}, false
		}
		if _, keyErr = new(edwards25519.Point).SetBytes(publicKey); keyErr != nil {
			return Input{}, false
		}
		input.Method = stringValue(listener["aead-method"])
		if input.Method == "" {
			input.Method = "chacha20-poly1305"
		}
		if input.Method != "chacha20-poly1305" && input.Method != "aes-128-gcm" {
			return Input{}, false
		}
		input.SudokuPaddingMin = intValue(listener["padding-min"])
		input.SudokuPaddingMax = intValue(listener["padding-max"])
		input.SudokuTableType = stringValue(listener["table-type"])
		if stringValue(listener["custom-table"]) != "" || len(stringSliceValue(listener["custom-tables"])) > 0 {
			return Input{}, false
		}
		input.SudokuHandshakeTimeout = intValue(listener["handshake-timeout"])
		if input.SudokuHandshakeTimeout == 0 {
			input.SudokuHandshakeTimeout = 5
		}
		input.SudokuEnablePureDownlink = boolValue(listener["enable-pure-downlink"])
		input.SudokuHTTPMaskEnabled = true
		input.SudokuHTTPMaskMode = "ws"
		if httpmask := mapValue(listener["httpmask"]); httpmask != nil {
			input.SudokuHTTPMaskEnabled = !boolValue(httpmask["disable"])
			if mode := stringValue(httpmask["mode"]); mode != "" {
				if mode != "auto" {
					return Input{}, false
				}
			}
			input.SudokuHTTPMaskPathRoot = stringValue(httpmask["path-root"])
		} else {
			return Input{}, false
		}
		input.SudokuMultiplex = "off"
		input.SudokuFallback = stringValue(listener["fallback"])
		if mapValue(listener["mux-option"]) != nil {
			return Input{}, false
		}
	}
	if input.Protocol == ProtocolPortForward {
		input.Network = networkListValue(listener["network"])
		input.TargetAddress, input.TargetPort, _ = splitForwardTarget(stringValue(listener["target"]))
	}
	input.TLSEnabled = input.CertificatePath != "" && input.PrivateKeyPath != ""
	if reality := mapValue(listener["reality-config"]); reality != nil {
		input.RealityEnabled = true
		input.RealityPrivateKey = stringValue(reality["private-key"])
		input.RealityShortID = firstString(reality["short-id"])
		input.RealityServerName = firstString(reality["server-names"])
		input.RealityPublicKey = realityPublicKey(input.RealityPrivateKey)
	}
	if value := stringValue(listener["ws-path"]); value != "" {
		input.Transport, input.TransportPath = "websocket", value
	}
	if value := stringValue(listener["grpc-service-name"]); value != "" {
		input.Transport, input.TransportPath = "grpc", value
	}
	if config := mapValue(listener["xhttp-config"]); config != nil {
		input.Transport, input.TransportPath = "xhttp", stringValue(config["path"])
	}
	if user := firstMap(listener["users"]); user != nil {
		input.Username = stringValue(user["username"])
		input.Credential = stringValue(user["uuid"])
		input.Flow = stringValue(user["flow"])
	}
	if input.Protocol == ProtocolVLESS && input.VLESSDecryption != "" {
		var err error
		input.VLESSEncryption, err = vlessEncryptionFromDecryption(input.VLESSDecryption)
		if err != nil {
			return Input{}, false
		}
		if input.Transport == "xhttp" {
			input.Protocol = ProtocolVLESSEncXHTTP
		} else {
			input.Protocol = ProtocolVLESSEncTCP
		}
	} else if input.Protocol == ProtocolVLESS && input.Transport == "xhttp" {
		input.Protocol = ProtocolVLESSXHTTP
	}
	if input.Protocol == ProtocolTrojan {
		if user := firstMap(listener["users"]); user != nil {
			input.Username, input.Credential = stringValue(user["username"]), stringValue(user["password"])
		}
	}
	if input.Protocol == ProtocolHy2 {
		if users := mapValue(listener["users"]); len(users) > 0 {
			for name, password := range users {
				input.Username, input.Credential = name, stringValue(password)
				break
			}
		}
	}
	if input.Protocol == ProtocolTUIC {
		if users := mapValue(listener["users"]); len(users) > 0 {
			for uuid, password := range users {
				input.Credential, input.SecondaryCredential = uuid, stringValue(password)
				break
			}
		}
	}
	if input.Protocol == ProtocolAnyTLS {
		if users := mapValue(listener["users"]); len(users) > 0 {
			for name, password := range users {
				input.Username, input.Credential = name, stringValue(password)
				break
			}
		}
	}
	input.BlockMainlandDestination, input.BlockMainlandSource = mainlandMihomoFlags(root, input.Tag)
	return input, parsedInputValid(input)
}

func mihomoTransport(listener map[string]any, input Input) {
	switch input.Transport {
	case "websocket":
		listener["ws-path"] = input.TransportPath
	case "grpc":
		listener["grpc-service-name"] = input.TransportPath
	case "xhttp":
		listener["xhttp-config"] = map[string]any{"path": input.TransportPath, "mode": "auto"}
	}
}

func compactMap(input map[string]any) map[string]any {
	for key, value := range input {
		switch typed := value.(type) {
		case string:
			if typed == "" {
				delete(input, key)
			}
		case int:
			if typed == 0 {
				delete(input, key)
			}
		case []string:
			if len(typed) == 0 {
				delete(input, key)
			}
		}
	}
	return input
}

func endpointHost(value string) string {
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	return ""
}
