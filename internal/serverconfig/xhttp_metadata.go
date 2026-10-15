package serverconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// Only client values and managed certificate references belong here. Native
// listener settings and server secrets are always recovered from Xray source.
type xhttpMetadata struct {
	Binding         string `json:"binding"`
	Mode            string `json:"mode"`
	Extra           string `json:"extra,omitempty"`
	Address         string `json:"address,omitempty"`
	Port            int    `json:"port,omitempty"`
	Host            string `json:"host,omitempty"`
	ALPN            string `json:"alpn,omitempty"`
	CDN             string `json:"cdn,omitempty"`
	TLSAddress      string `json:"tls_address,omitempty"`
	TLSPort         int    `json:"tls_port,omitempty"`
	TLSHost         string `json:"tls_host,omitempty"`
	TLSName         string `json:"tls_name,omitempty"`
	RealityName     string `json:"reality_name,omitempty"`
	CertificatePath string `json:"certificate_path,omitempty"`
	PrivateKeyPath  string `json:"private_key_path,omitempty"`
}

func xhttpBinding(input Input) string {
	values := []string{input.Tag, input.Listen, strconv.Itoa(input.Port), input.Credential, input.TransportPath, input.TransportHost, input.RealityPublicKey, input.RealityShortID, strings.Join(xhttpRealityNames(input), ","), strconv.Itoa(input.XHTTPBackendPort), strconv.Itoa(input.XHTTPTLSPort)}
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}

func marshalXHTTPMetadata(input Input) *xhttpMetadata {
	return &xhttpMetadata{Binding: xhttpBinding(input), Mode: input.XHTTPMode, Extra: input.XHTTPExtra, Address: input.ClientAddress, Port: input.ClientPort, Host: input.ClientHost, ALPN: input.TLSClientALPN, CDN: input.XHTTPCDN, TLSAddress: input.XHTTPTLSAddress, TLSPort: input.XHTTPTLSClientPort, TLSHost: input.XHTTPTLSHost, TLSName: input.TLSServerName, RealityName: input.RealityServerName, CertificatePath: input.CertificatePath, PrivateKeyPath: input.PrivateKeyPath}
}

func applyXHTTPMetadata(input *Input, m clientMetadata) {
	x := m.XHTTP
	if x == nil || x.Binding != xhttpBinding(*input) {
		return
	}
	input.XHTTPMode, input.XHTTPExtra = x.Mode, x.Extra
	input.ClientAddress, input.ClientPort, input.ClientHost = x.Address, x.Port, x.Host
	input.TLSClientALPN, input.XHTTPCDN = x.ALPN, x.CDN
	if slices.Contains(xhttpRealityNames(*input), x.RealityName) {
		input.RealityServerName = x.RealityName
	}
	if isXHTTPGroup(input.Protocol) && isXHTTPGroup(m.Protocol) {
		input.Protocol = m.Protocol
		input.XHTTPTLSAddress, input.TLSServerName = x.TLSAddress, x.TLSName
		input.XHTTPTLSClientPort, input.XHTTPTLSHost = x.TLSPort, x.TLSHost
		input.CertificatePath, input.PrivateKeyPath = x.CertificatePath, x.PrivateKeyPath
	}
}

func xhttpClientVariant(input Input, kind string) Input {
	input.Protocol, input.Flow = kind, ""
	if kind == ProtocolVLESS {
		input.Transport, input.Flow, input.XHTTPExtra = "raw", "xtls-rprx-vision", ""
		input.TransportPath, input.TransportHost = "", ""
		return input
	}
	input.Transport = "xhttp"
	reality := xhttpEndpoint(input, false)
	tls := xhttpEndpoint(input, true)
	if kind == ProtocolXHTTPTLS || kind == ProtocolXHTTPTLSReality {
		input.RealityEnabled, input.TLSEnabled = false, true
		input.ClientAddress = stringValue(tls["address"])
		input.ClientPort = intValue(tls["port"])
		input.ClientHost = stringValue(mapValue(tls["xhttpSettings"])["host"])
	}
	extra, _ := xhttpObject(input.XHTTPExtra)
	if kind == ProtocolXHTTPTLSReality {
		extra["downloadSettings"] = reality
	}
	if kind == ProtocolXHTTPRealityTLS {
		extra["downloadSettings"] = tls
	}
	if len(extra) > 0 {
		data, _ := json.Marshal(extra)
		input.XHTTPExtra = string(data)
	}
	// Variants represent a single client, never another server deployment.
	input.XHTTPBackendPort, input.XHTTPTLSPort = 0, 0
	if kind == ProtocolXHTTPTLSReality {
		input.Protocol = ProtocolXHTTPTLS
	}
	if kind == ProtocolXHTTPRealityTLS {
		input.Protocol = ProtocolVLESSXHTTP
	}
	return input
}
