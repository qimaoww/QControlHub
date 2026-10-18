package serverconfig

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestClientURLComponentsRoundTrip(t *testing.T) {
	const uuid = "123e4567-e89b-42d3-a456-426614174000"
	const secret = "测试🚀 @:/?#&=+%2F"
	inputs := []Input{
		{Protocol: ProtocolShadowsocks, Method: "aes-256-gcm", Credential: secret},
		{Protocol: ProtocolSS2022, Method: "2022-blake3-aes-256-gcm", Credential: "+/8wMTIzNDU2Nzg5YWJjZGVmMDEyMzQ1Njc4OWFiY2Q="},
		{Protocol: ProtocolVLESS, Credential: uuid},
		{Protocol: ProtocolTrojan, Credential: secret, TLSEnabled: true},
		{Protocol: ProtocolHy2, Credential: secret, TLSEnabled: true},
		{Protocol: ProtocolTUIC, Credential: uuid, SecondaryCredential: secret, TLSEnabled: true},
		{Protocol: ProtocolSocks, Username: secret, Credential: secret},
		{Protocol: ProtocolAnyTLS, Credential: secret, TLSEnabled: true},
	}
	for _, input := range inputs {
		for _, address := range []string{"edge.example.test", "192.0.2.10", "2001:db8::10", "[2001:db8::10]"} {
			for _, name := range []string{"", "   ", "香港 🚀 @:/?#&=+%2F"} {
				t.Run(input.Protocol+"/"+address+"/"+name, func(t *testing.T) {
					input.Tag, input.Port, input.Transport = "fallback", 24443, "raw"
					profile, err := BuildClientProfileNamed(input, address, "tls.example.test", name)
					if err != nil {
						t.Fatal(err)
					}
					repeated, err := BuildClientProfileNamed(input, address, "tls.example.test", name)
					if err != nil || repeated.URI != profile.URI {
						t.Fatal("non-deterministic export")
					}

					if input.Protocol == ProtocolHy2 || input.Protocol == ProtocolSocks {
						if profile.URIError == "" || !profile.SubscriptionCompatible {
							t.Fatal("sensitive auth must retain native import")
						}
						var proxy map[string]any
						if err := yaml.Unmarshal([]byte(profile.URI), &proxy); err != nil {
							t.Fatal(err)
						}
						if proxy["password"] != input.Credential || proxy["server"] != strings.Trim(address, "[]") || proxy["port"] != input.Port {
							t.Fatal("native credentials or endpoint changed")
						}
						if input.Protocol == ProtocolSocks && proxy["username"] != input.Username {
							t.Fatal("native username changed")
						}
						return
					}
					u, err := url.Parse(profile.URI)
					if err != nil {
						t.Fatal(err)
					}
					if u.Hostname() != strings.Trim(address, "[]") || u.Port() != "24443" {
						t.Fatalf("endpoint: %s", profile.URI)
					}
					wantName := strings.TrimSpace(name)
					if wantName == "" {
						wantName = "fallback"
					}
					if u.Fragment != wantName || strings.ContainsAny(profile.URI, " \r\n") {
						t.Fatalf("name: %s", profile.URI)
					}
					switch input.Protocol {
					case ProtocolShadowsocks:
						identity, err := base64.RawURLEncoding.DecodeString(u.User.Username())
						if err != nil || string(identity) != input.Method+":"+input.Credential {
							t.Fatal("SS credentials changed")
						}
					case ProtocolSS2022:
						password, ok := u.User.Password()
						if !ok || u.User.Username() != input.Method || password != input.Credential || !strings.Contains(profile.URI, "%2B%2F") {
							t.Fatal("SIP002 2022 userinfo must be percent encoded")
						}
					case ProtocolTUIC:
						password, _ := u.User.Password()
						if u.User.Username() != uuid || password != secret {
							t.Fatal("TUIC credentials changed")
						}
					case ProtocolSocks:
						password, _ := u.User.Password()
						if u.User.Username() != secret || password != secret {
							t.Fatal("SOCKS credentials changed")
						}
					default:
						if u.User.Username() != input.Credential {
							t.Fatal("credentials changed")
						}
					}
				})
			}
		}
	}
}

func TestClientURLTransportParameters(t *testing.T) {
	const path = "/路径 +%2F?token=@:#/=&other=+"
	for _, transport := range []string{"websocket", "grpc", "xhttp", "raw"} {
		input := Input{Protocol: ProtocolVLESS, Tag: "transport", Port: 443, Credential: "123e4567-e89b-42d3-a456-426614174000", Transport: transport, TransportPath: path, TransportHost: "http.example.test", TLSEnabled: true, TLSServerName: "tls.example.test", TLSALPN: "h2,http/1.1", XHTTPMode: "packet-up"}
		profile, err := BuildClientProfile(input, "2001:db8::10", "")
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(profile.URI)
		q := u.Query()
		if q.Get("sni") != "tls.example.test" || q.Get("alpn") != "h2,http/1.1" {
			t.Fatal("TLS fields missing")
		}
		for key, values := range q {
			if len(values) != 1 || values[0] == "" {
				t.Fatalf("invalid %s: %v", key, values)
			}
		}
		if strings.Contains(u.RawQuery, "+") {
			t.Fatal("query used form spaces")
		}
		switch transport {
		case "websocket", "xhttp":
			if q.Get("path") != path || q.Get("host") != "http.example.test" {
				t.Fatal("HTTP fields changed")
			}
			if transport == "xhttp" && q.Get("mode") != "packet-up" {
				t.Fatal("XHTTP mode lost")
			}
		case "grpc":
			if q.Get("serviceName") != path || q.Has("host") {
				t.Fatal("gRPC fields changed")
			}
		case "raw":
			if q.Has("path") || q.Has("host") {
				t.Fatal("stale HTTP fields exported on TCP")
			}
		}
		input.Protocol = ProtocolVMess
		if transport == "xhttp" {
			continue
		} // VMess XHTTP is not a project preset.
		profile, err = BuildClientProfile(input, "edge.example.test", "")
		if err != nil {
			t.Fatal(err)
		}
		payload, err := vmessURLPayload(profile.URI)
		if err != nil {
			t.Fatal(err)
		}
		field := func(key string) string { var value string; _ = json.Unmarshal(payload[key], &value); return value }
		if field("sni") != "tls.example.test" {
			t.Fatal("VMess SNI changed")
		}
		if transport != "raw" && field("path") != path || transport == "websocket" && field("host") != "http.example.test" {
			t.Fatal("VMess transport fields lost")
		}
		if transport == "grpc" && payload["host"] != nil {
			t.Fatal("stale HTTP Host exported on gRPC")
		}
		if transport == "raw" && (payload["path"] != nil || payload["host"] != nil) {
			t.Fatal("VMess stale HTTP fields exported")
		}
	}
	// A missing WS Host must not silently become TLS SNI.
	input := Input{Protocol: ProtocolVMess, Port: 443, Credential: "123e4567-e89b-42d3-a456-426614174000", Transport: "websocket", TLSEnabled: true}
	profile, err := BuildClientProfile(input, "edge.example.test", "tls.example.test")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := vmessURLPayload(profile.URI)
	if payload["host"] != nil {
		t.Fatal("SNI used as HTTP Host")
	}
}

func TestRenameClientURLPreservesConnectionParameters(t *testing.T) {
	const name = "香港 🚀 @:/?#&=+%2F"
	const original = "vless://uuid@[2001:db8::10]:24443?encryption=none&path=%2f%252F%20%2B&type=ws#old"
	renamed, err := RenameClientURL(original, name)
	if err != nil || strings.SplitN(renamed, "#", 2)[0] != strings.SplitN(original, "#", 2)[0] || ClientURLName(renamed) != name {
		t.Fatalf("rename: %s %v", renamed, err)
	}
	payload := map[string]json.RawMessage{"v": json.RawMessage(`"2"`), "ps": json.RawMessage(`"old"`), "add": json.RawMessage(`"2001:db8::10"`), "id": json.RawMessage(`"uuid"`), "port": json.RawMessage(`24443`), "extra": json.RawMessage(`{"unknown":true}`)}
	data, _ := json.Marshal(payload)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		uri := "vmess://" + encoding.EncodeToString(data) + "#legacy@name"
		if ClientURLName(uri) != "old" {
			t.Fatal("legacy fragment changed VMess JSON name")
		}
		renamed, err = RenameClientURL(uri, name)
		if err != nil || ClientURLName(renamed) != name || strings.Contains(renamed, "#") {
			t.Fatalf("VMess name: %s %v", renamed, err)
		}
		got, _ := vmessURLPayload(renamed)
		delete(got, "ps")
		copy := map[string]json.RawMessage{}
		for k, v := range payload {
			if k != "ps" {
				copy[k] = v
			}
		}
		if !reflect.DeepEqual(got, copy) {
			t.Fatal("VMess connection or extension fields changed")
		}
	}
}

func TestClientURLReservedWebSocketPathUsesNativeFormat(t *testing.T) {
	for _, protocol := range []string{ProtocolVLESS, ProtocolVMess, ProtocolTrojan} {
		input := Input{Protocol: protocol, Tag: "ws-literal", Port: 443, Credential: "123e4567-e89b-42d3-a456-426614174000", Transport: "websocket", TransportPath: "/relay%2F?ed=2048&token=%2F", TLSEnabled: true}
		profile, err := BuildClientProfile(input, "edge.example.test", "tls.example.test")
		if err != nil {
			t.Fatal(err)
		}
		if profile.URIError == "" || profile.Format != "Mihomo YAML" || !profile.SubscriptionCompatible {
			t.Fatal("reserved share path must use native format")
		}
		var proxy map[string]any
		if err := yaml.Unmarshal([]byte(profile.URI), &proxy); err != nil {
			t.Fatal(err)
		}
		if proxy["ws-opts"].(map[string]any)["path"] != input.TransportPath {
			t.Fatal("native WS path changed")
		}
	}
}
