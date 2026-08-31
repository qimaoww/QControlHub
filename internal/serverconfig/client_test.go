package serverconfig

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestBuildClientProfileCoversEveryServerProtocol(t *testing.T) {
	t.Parallel()
	const (
		uuid     = "123e4567-e89b-42d3-a456-426614174000"
		password = "correct-horse-battery-staple"
		psk      = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	)
	sudokuProtocol, ok := FindProtocol(core.EngineMihomo, ProtocolSudoku)
	if !ok {
		t.Fatal("Sudoku preset is unavailable")
	}
	sudoku, err := NewPlan(sudokuProtocol)
	if err != nil {
		t.Fatal(err)
	}
	sudoku.Tag, sudoku.Port = "sudoku", 20010
	tests := []struct {
		name   string
		input  Input
		scheme string
	}{
		{name: "shadowsocks standard", input: Input{Protocol: ProtocolShadowsocks, Tag: "ss-standard", Port: 20000, Credential: password, Method: "chacha20-ietf-poly1305", Transport: "raw"}, scheme: "ss"},
		{name: "shadowsocks", input: Input{Protocol: ProtocolSS2022, Tag: "ss", Port: 20001, Credential: psk, Method: "2022-blake3-aes-256-gcm", Transport: "raw"}, scheme: "ss"},
		{name: "vless reality", input: Input{Protocol: ProtocolVLESS, Tag: "vless", Port: 20002, Credential: uuid, Flow: "xtls-rprx-vision", Transport: "raw", RealityEnabled: true, RealityServerName: "www.cloudflare.com", RealityPublicKey: "public-key", RealityShortID: "0123456789abcdef"}, scheme: "vless"},
		{name: "vless xhttp reality", input: Input{Protocol: ProtocolVLESSXHTTP, Tag: "vless-xhttp", Port: 20008, Credential: uuid, Transport: "xhttp", TransportPath: "/xhttp", RealityEnabled: true, RealityServerName: "www.cloudflare.com", RealityPublicKey: "public-key", RealityShortID: "0123456789abcdef"}, scheme: "vless"},
		{name: "vmess", input: Input{Protocol: ProtocolVMess, Tag: "vmess", Port: 20003, Credential: uuid, Transport: "websocket", TransportPath: "/relay", TLSEnabled: true}, scheme: "vmess"},
		{name: "trojan", input: Input{Protocol: ProtocolTrojan, Tag: "trojan", Port: 20004, Credential: password, Transport: "grpc", TransportPath: "relay", TLSEnabled: true}, scheme: "trojan"},
		{name: "hysteria2", input: Input{Protocol: ProtocolHy2, Tag: "hy2", Port: 20005, Credential: password, Transport: "raw", TLSEnabled: true}, scheme: "hysteria2"},
		{name: "tuic", input: Input{Protocol: ProtocolTUIC, Tag: "tuic", Port: 20006, Credential: uuid, SecondaryCredential: password, Transport: "raw", TLSEnabled: true}, scheme: "tuic"},
		{name: "anytls", input: Input{Protocol: ProtocolAnyTLS, Tag: "anytls", Port: 20007, Credential: password, Transport: "raw", TLSEnabled: true}, scheme: "anytls"},
		{name: "snell", input: Input{Protocol: ProtocolSnell, Tag: "snell", Port: 20009, Credential: password, Transport: "raw", SnellVersion: 5}, scheme: "snell"},
		{name: "sudoku", input: sudoku, scheme: "sudoku"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			profile, err := BuildClientProfile(test.input, "edge.example.com", "tls.example.com")
			if err != nil {
				t.Fatal(err)
			}
			if profile.Format == "" || profile.URI == "" || len(profile.Fields) < 6 {
				t.Fatalf("incomplete client profile: %+v", profile)
			}
			if strings.ContainsAny(profile.URI, "\r\n") {
				t.Fatalf("client export must be a single-line URL: %+v", profile)
			}
			if test.input.Protocol == ProtocolSudoku {
				for _, field := range profile.Fields {
					if field.Value == test.input.Credential {
						t.Fatalf("Sudoku client fields leaked the server-only Master Public Key: %+v", profile.Fields)
					}
				}
			}
			if test.input.Protocol == ProtocolSnell {
				if profile.SubscriptionCompatible || !strings.HasPrefix(profile.URI, "snell://") {
					t.Fatalf("Snell profile = %+v", profile)
				}
			}
			if test.input.Protocol == ProtocolSudoku {
				if profile.SubscriptionCompatible || !strings.HasPrefix(profile.URI, "sudoku://") {
					t.Fatalf("Sudoku profile = %+v", profile)
				}
			}
			if !test.input.TLSEnabled && !test.input.RealityEnabled {
				for _, field := range profile.Fields {
					if field.Label == "TLS ServerName" {
						t.Fatalf("non-TLS profile exported a TLS ServerName: %+v", profile.Fields)
					}
				}
			}
			if test.scheme == "vmess" {
				if !strings.HasPrefix(profile.URI, "vmess://") {
					t.Fatalf("URI = %q", profile.URI)
				}
				decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(profile.URI, "vmess://"))
				if decodeErr != nil {
					t.Fatal(decodeErr)
				}
				var payload map[string]string
				if err := json.Unmarshal(decoded, &payload); err != nil {
					t.Fatal(err)
				}
				if payload["add"] != "edge.example.com" || payload["port"] != "20003" || payload["id"] != uuid || payload["net"] != "ws" || payload["tls"] != "tls" {
					t.Fatalf("VMess payload = %#v", payload)
				}
				return
			}
			parsed, parseErr := url.Parse(profile.URI)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			if parsed.Scheme != test.scheme || parsed.Hostname() != "edge.example.com" || parsed.Port() == "" {
				t.Fatalf("parsed URI = %#v", parsed)
			}
		})
	}
}

func TestBuildSnellClientURIIncludesOptionalTransportParameters(t *testing.T) {
	t.Parallel()
	input := Input{
		Protocol: ProtocolSnellShadowTLS, Tag: "snell-shadow", Port: 20009,
		Credential: "psk with symbols", Transport: "raw", SnellVersion: 5,
		SnellUDP: true, SnellReuse: true, SnellObfsMode: SnellObfsShadowTLS,
		SnellObfsHost: "cover.example.com", SnellClientFingerprint: "chrome",
		SnellShadowTLSVersion: 3, SnellShadowTLSPassword: "shadow secret",
		SnellShadowTLSUser: "shadow-user", SnellShadowTLSHandshake: "cover.example.com:443",
		SnellShadowTLSALPN: "h2,http/1.1",
	}
	profile, err := BuildClientProfile(input, "edge.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(profile.URI)
	if err != nil {
		t.Fatalf("Snell URI = %q, err = %v", profile.URI, err)
	}
	if parsed.Scheme != "snell" || parsed.Hostname() != "edge.example.com" || parsed.Port() != "20009" {
		t.Fatalf("Snell URI target = %#v", parsed)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"psk": "psk with symbols", "version": "5", "udp": "true", "tfo": "true", "reuse": "true",
		"obfs": "shadow-tls", "obfs-host": "cover.example.com", "client-fingerprint": "chrome",
		"obfs-password": "shadow secret", "obfs-version": "3", "obfs-alpn": "h2,http/1.1",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("query[%q] = %q, want %q", key, got, want)
		}
	}
	if profile.SubscriptionCompatible {
		t.Fatal("Snell raw URI must not be advertised as a universal subscription format")
	}
}

func TestBuildClientProfileRejectsListenerAndUnsafeAddresses(t *testing.T) {
	t.Parallel()
	input := Input{Protocol: ProtocolTrojan, Port: 443, Credential: "correct-horse-battery-staple", Transport: "raw", TLSEnabled: true}
	for _, address := range []string{"", "0.0.0.0", "::", "https://edge.example.com", "user@edge.example.com", "edge.example.com:443", "-edge.example.com"} {
		if _, err := BuildClientProfile(input, address, "edge.example.com"); err == nil {
			t.Errorf("address %q unexpectedly accepted", address)
		}
	}
	profile, err := BuildClientProfile(input, "[2001:db8::1]", "edge.example.com")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(profile.URI)
	if err != nil || parsed.Hostname() != "2001:db8::1" {
		t.Fatalf("IPv6 URI = %q, err = %v", profile.URI, err)
	}
}

func TestBuildClientProfileNamedUsesOperatorName(t *testing.T) {
	t.Parallel()
	input := Input{Protocol: ProtocolVLESS, Tag: "inbound-tag", Port: 443, Credential: "123e4567-e89b-42d3-a456-426614174000", Transport: "raw"}
	profile, err := BuildClientProfileNamed(input, "edge.example.com", "", "Tokyo v6")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(profile.URI, "#Tokyo%20v6") {
		t.Fatalf("named profile URI = %q", profile.URI)
	}
}

func TestClientProfileNeverExportsServerPrivateMaterial(t *testing.T) {
	t.Parallel()
	input := Input{
		Protocol: ProtocolVLESS, Tag: "reality", Port: 443, Credential: "123e4567-e89b-42d3-a456-426614174000",
		Flow: "xtls-rprx-vision", Transport: "raw", RealityEnabled: true, RealityServerName: "www.cloudflare.com",
		RealityPrivateKey: "server-private-key", RealityPublicKey: "client-public-key", RealityShortID: "0123456789abcdef",
		CertificatePath: "/secret/server.crt", PrivateKeyPath: "/secret/server.key",
	}
	profile, err := BuildClientProfile(input, "edge.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"server-private-key", "/secret/server.crt", "/secret/server.key"} {
		if strings.Contains(string(serialized), forbidden) {
			t.Fatalf("client profile leaked %q", forbidden)
		}
	}
	if !strings.Contains(string(serialized), "client-public-key") {
		t.Fatal("client profile omitted the Reality public key")
	}
}
