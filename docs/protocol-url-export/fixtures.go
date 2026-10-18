// Generates test-only exports for reproducible URL and browser comparisons.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func main() {
	const uuid = "123e4567-e89b-42d3-a456-426614174000"
	const password = "test 密码🚀 @:/?#&=+%2F"
	type sample struct {
		Name    string                     `json:"name"`
		Address string                     `json:"address"`
		Input   serverconfig.Input         `json:"input"`
		Profile serverconfig.ClientProfile `json:"profile"`
		Mihomo  string                     `json:"mihomo_yaml,omitempty"`
	}
	var samples []sample
	add := func(name, address string, input serverconfig.Input) {
		input.Tag = name
		if input.Port == 0 {
			input.Port = 24443 + len(samples)
		}
		if input.Transport == "" {
			input.Transport = "raw"
		}
		profile, err := serverconfig.BuildClientProfileNamed(input, address, "tls.example.test", "香港 🚀 + %2F @:/?#&=")
		if err != nil {
			panic(fmt.Sprintf("%s: %v", name, err))
		}
		samples = append(samples, sample{name, address, input, profile, profile.Mihomo})
	}
	add("ss", "edge.example.test", serverconfig.Input{Protocol: serverconfig.ProtocolShadowsocks, Method: "aes-256-gcm", Credential: password})
	add("ss2022", "2001:db8::10", serverconfig.Input{Protocol: serverconfig.ProtocolSS2022, Method: "2022-blake3-aes-256-gcm", Credential: base64.StdEncoding.EncodeToString([]byte("0123456789abcdefghijklmnopqrstuv"))})
	add("vless-reality", "192.0.2.10", serverconfig.Input{Protocol: serverconfig.ProtocolVLESS, Credential: uuid, Flow: "xtls-rprx-vision", RealityEnabled: true, RealityServerName: "cover.example.test", RealityPublicKey: "Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw", RealityShortID: "0123456789abcdef"})
	for _, transport := range []string{"websocket", "grpc", "xhttp"} {
		input := serverconfig.Input{Protocol: serverconfig.ProtocolVLESS, Credential: uuid, Transport: transport, TransportPath: "/relay +%2F?token=@:#/=&other=+", TransportHost: "http.example.test", TLSEnabled: true, TLSServerName: "tls.example.test", TLSALPN: "h2,http/1.1"}
		if transport == "xhttp" {
			input.Protocol = serverconfig.ProtocolVLESSXHTTP
			input.XHTTPMode = "packet-up"
		}
		add("vless-"+transport, "2001:db8::10", input)
		if transport != "xhttp" {
			input.Protocol = serverconfig.ProtocolVMess
			add("vmess-"+transport, "edge.example.test", input)
		}
	}
	add("trojan", "edge.example.test", serverconfig.Input{Protocol: serverconfig.ProtocolTrojan, Credential: password, TLSEnabled: true})
	add("hysteria2", "192.0.2.10", serverconfig.Input{Protocol: serverconfig.ProtocolHy2, Credential: "test-hysteria+&=password", TLSEnabled: true, TLSALPN: "h3"})
	add("tuic", "2001:db8::10", serverconfig.Input{Protocol: serverconfig.ProtocolTUIC, Credential: uuid, SecondaryCredential: password, TLSEnabled: true, TLSALPN: "h3"})
	add("socks5", "192.0.2.10", serverconfig.Input{Protocol: serverconfig.ProtocolSocks, Username: "test+&=user", Credential: "test-socks+&=password"})
	add("anytls", "edge.example.test", serverconfig.Input{Protocol: serverconfig.ProtocolAnyTLS, Credential: password, TLSEnabled: true})
	add("xhttp-native", "edge.example.test", serverconfig.Input{Protocol: serverconfig.ProtocolVLESSXHTTP, Credential: uuid, Transport: "xhttp", TransportPath: "/relay", TransportHost: "http.example.test", XHTTPMode: "stream-up", XHTTPExtra: `{"noGRPCHeader":true,"xmux":{"maxConcurrency":"16-32"},"downloadSettings":{"address":"download.example.test","port":443,"network":"xhttp","security":"tls","tlsSettings":{"serverName":"download.example.test"},"xhttpSettings":{"path":"/down","mode":"stream-one"}}}`, TLSEnabled: true})
	add("http-native", "edge.example.test", serverconfig.Input{Protocol: serverconfig.ProtocolVLESS, Credential: uuid, Transport: "http", TransportPath: "/native", TransportHost: "http.example.test", TransportHTTPMethod: "POST", TLSEnabled: true, TLSALPN: "h2"})
	add("ws-native", "edge.example.test", serverconfig.Input{Protocol: serverconfig.ProtocolVMess, Credential: uuid, Transport: "websocket", TransportPath: "/relay%2F?ed=2048&token=test", TLSEnabled: true})
	add("hysteria2-native", "192.0.2.10", serverconfig.Input{Protocol: serverconfig.ProtocolHy2, Credential: password, TLSEnabled: true, TLSALPN: "h3"})
	add("socks5-native", "2001:db8::10", serverconfig.Input{Protocol: serverconfig.ProtocolSocks, Username: "user+%2F@:/?#&=", Credential: password})
	if err := json.NewEncoder(os.Stdout).Encode(samples); err != nil {
		panic(err)
	}
}
