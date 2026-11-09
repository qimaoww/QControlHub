package serverconfig

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
)

// Detached dial options never change the listener. Bind them to the native
// endpoint so a source edit cannot attach an old client's options to a new peer.
type clientConnectionMetadata struct {
	Binding    string `json:"binding"`
	Address    string `json:"address,omitempty"`
	Port       int    `json:"port,omitempty"`
	Host       string `json:"host,omitempty"`
	ALPN       string `json:"alpn,omitempty"`
	RealitySNI string `json:"reality_sni,omitempty"`
}

func clientConnectionBinding(input Input) string {
	if input.Transport == "" {
		input.Transport = "raw"
	}
	if input.Transport == "raw" || input.Transport == "quic" {
		input.TransportPath = ""
	}
	certificate, key := input.CertificatePath, input.PrivateKeyPath
	tls := input.TLSEnabled
	if !tls || proxiedTLS(input) {
		certificate, key, tls = "", "", false
	}
	values := []string{nativeProtocol(input.Protocol), input.Tag, input.Listen, strconv.Itoa(input.Port), input.Credential, input.SecondaryCredential, input.Transport, input.TransportPath, certificate, key, strconv.FormatBool(tls), strconv.FormatBool(input.RealityEnabled), input.RealityPublicKey, input.RealityShortID, input.WireGuardServerPublicKey, input.WireGuardClientPublicKey}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(values, "\x00"))))
}

func validateClientConnection(input Input) error {
	if input.ClientAddress != "" {
		if _, err := NormalizeClientAddress(input.ClientAddress); err != nil {
			return fmt.Errorf("客户端连接地址：%w", err)
		}
	}
	if input.ClientPort < 0 || input.ClientPort > 65535 {
		return fmt.Errorf("客户端连接端口必须在 1–65535，留空使用监听端口")
	}
	if input.ClientHost != "" {
		if input.Transport != "websocket" && input.Transport != "http" && input.Transport != "httpupgrade" && input.Transport != "xhttp" {
			return fmt.Errorf("此传输不支持客户端 HTTP Host")
		}
		if len(input.ClientHost) > 253 || strings.ContainsAny(input.ClientHost, "\r\n\x00\t /\\") {
			return fmt.Errorf("客户端 HTTP Host 不能包含空白、路径或控制字符")
		}
		if input.TransportHost != "" && input.ClientHost != input.TransportHost {
			return fmt.Errorf("服务端设置 Host 时，客户端 Host 必须相同；也可清空服务端 Host")
		}
	}
	if _, err := validatedSingBoxALPN(input.TLSALPN); err != nil {
		return err
	}
	if _, err := validatedSingBoxALPN(input.TLSClientALPN); err != nil {
		return err
	}
	return nil
}

// Resolve once before constructing either a share profile or a native outbound.
// Clear detached values in the copy to keep nested JSON exports idempotent.
func clientConnectionInput(input Input, address string) (Input, string) {
	if input.Transport == "xhttp" {
		return input, address
	}
	if proxiedTLS(input) {
		input.Port = input.TLSPublicPort
	}
	if input.ClientAddress != "" {
		address = input.ClientAddress
	}
	if input.ClientPort > 0 {
		input.Port = input.ClientPort
		// A nested export must retain the resolved public port.
		if proxiedTLS(input) {
			input.TLSPublicPort = input.ClientPort
		}
	}
	if input.ClientHost != "" {
		input.TransportHost = input.ClientHost
	}
	if input.TLSClientALPN != "" {
		input.TLSALPN = input.TLSClientALPN
	}
	input.ClientAddress, input.ClientPort, input.ClientHost, input.TLSClientALPN = "", 0, "", ""
	return input, address
}
