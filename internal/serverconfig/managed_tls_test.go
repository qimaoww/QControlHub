package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Existing core matrices intentionally exercise native TLS; managed proxy
// defaults are verified separately with their versioned auxiliary metadata.
func nativeTLSFixturePlan(protocol Protocol) (Input, error) {
	protocol.SupportsManagedTLS = false
	input, err := NewPlan(protocol)
	if input.Transport == "xhttp" {
		input.TLSServerName = "tls.example.test"
		input.CertificatePath, input.PrivateKeyPath = "/etc/qcontrolhub/tls/test.crt", "/etc/qcontrolhub/tls/test.key"
		if isXHTTPGroup(input.Protocol) {
			input.RealityServerName = "reality.example.test"
		}
	}
	return input, err
}

func TestManagedTLSHTTPAndExternalExport(t *testing.T) {
	for _, mode := range []string{"caddy", "external"} {
		for _, transport := range []string{"ws", "httpupgrade", "h2"} {
			t.Run(mode+"/"+transport, func(t *testing.T) {
				protocol, _ := FindProtocol(core.EngineSingBox, "vless-"+transport+"-tls")
				input, _ := NewPlan(protocol)
				input.TLSMode, input.TLSServerName, input.TransportHost = mode, "tls.example.test", "host.example.test"
				input.Port, input.TLSPublicPort = 24567, 8443
				if transport == "h2" {
					input.TLSALPN = "h2"
				}
				content, err := Generate(core.EngineSingBox, input)
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := MarshalClientMetadataWithPrevious(input, "")
				if err != nil {
					t.Fatal(err)
				}
				frontends, err := ManagedTLSFrontends(core.EngineSingBox, content, map[string]string{input.Tag: metadata})
				if err != nil {
					t.Fatal(err)
				}
				if mode == "external" && len(frontends) != 0 {
					t.Fatal("external proxy was sent to Agent")
				}
				if mode == "caddy" && (len(frontends) != 1 || frontends[0].Host != input.TransportHost) {
					t.Fatal("managed route lost Host")
				}
				if mode == "caddy" {
					edited := strings.ReplaceAll(content, input.TransportHost, "edited.example.test")
					changed, err := ManagedTLSFrontends(core.EngineSingBox, edited, map[string]string{input.Tag: metadata})
					if err != nil || len(changed) != 1 || changed[0].Host != "edited.example.test" {
						t.Fatal("readiness probe retained stale Host from metadata", changed, err)
					}
				}
				parsed, ok := Parse(core.EngineSingBox, content)
				if !ok {
					t.Fatal("parse")
				}
				if err := ApplyClientMetadata(&parsed, metadata); err != nil {
					t.Fatal(err)
				}
				if parsed.TLSMode != mode || parsed.TLSPublicPort != 8443 || !parsed.TLSEnabled {
					t.Fatal(parsed)
				}
				outbound, err := BuildClientOutbound(core.EngineSingBox, parsed, "127.0.0.1", "")
				if err != nil {
					t.Fatal(err)
				}
				var out struct {
					Port int `json:"server_port"`
					TLS  struct {
						Enabled bool   `json:"enabled"`
						SNI     string `json:"server_name"`
					} `json:"tls"`
				}
				if err := json.Unmarshal(outbound, &out); err != nil || out.Port != 8443 || !out.TLS.Enabled || out.TLS.SNI != input.TLSServerName {
					t.Fatal(string(outbound), err)
				}
			})
		}
	}
}

func TestManagedTLSLifecycle(t *testing.T) {
	for _, mode := range []string{"caddy", "nginx"} {
		for _, base := range []string{"vmess", "vless", "trojan"} {
			for _, transport := range []string{"ws", "httpupgrade"} {
				t.Run(mode+"/"+base+"/"+transport, func(t *testing.T) {
					protocol, _ := FindProtocol(core.EngineSingBox, base+"-"+transport+"-tls")
					input, err := NewPlan(protocol)
					if err != nil {
						t.Fatal(err)
					}
					if input.TLSPublicPort != 443 || input.TLSMode != "caddy" || input.Listen != "127.0.0.1" || input.CertificatePath != "" {
						t.Fatalf("bad defaults: %+v", input)
					}
					input.TLSALPN = "http/1.1"
					input.TLSMode = mode
					if mode == "caddy" || input.Protocol == "vless-ws-tls" {
						input.TLSACMEEmail = "certs@example.test"
					}
					input.TLSPublicPort = 8443
					input.TLSServerName = "tls.example.test"
					input.TransportHost = "host.example.test"
					content, err := Generate(core.EngineSingBox, input)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(content, `"tls"`) || strings.Contains(content, "server.crt") {
						t.Fatal("backend references TLS certificate")
					}
					encoded, err := MarshalClientMetadataWithPrevious(input, "")
					if err != nil {
						t.Fatal(err)
					}
					frontends, err := ManagedTLSFrontends(core.EngineSingBox, content, map[string]string{input.Tag: encoded})
					if err != nil || len(frontends) != 1 {
						t.Fatalf("frontend: %v %v", frontends, err)
					}
					parsed, ok := Parse(core.EngineSingBox, content)
					if !ok {
						t.Fatal("parse")
					}
					if err = ApplyClientMetadata(&parsed, encoded); err != nil {
						t.Fatal(err)
					}
					if parsed.TLSACMEEmail != input.TLSACMEEmail || frontends[0].ACMEEmail != input.TLSACMEEmail {
						t.Fatal("ACME account lost on save/read")
					}
					if parsed.Protocol != input.Protocol || parsed.TLSMode != mode || parsed.TLSPublicPort != 8443 || !parsed.TLSEnabled || parsed.TLSServerName != input.TLSServerName || parsed.TLSALPN != input.TLSALPN || parsed.TransportHost != input.TransportHost {
						t.Fatalf("lost metadata: %+v", parsed)
					}
					out, err := BuildClientOutbound(core.EngineSingBox, parsed, "127.0.0.1", "")
					if err != nil {
						t.Fatal(err)
					}
					var outbound map[string]any
					_ = json.Unmarshal(out, &outbound)
					if outbound["server_port"] != float64(8443) {
						t.Fatal(string(out))
					}
					if strings.Contains(string(out), "certs@example.test") || strings.Contains(content, "certs@example.test") {
						t.Fatal("ACME account leaked into core or client config")
					}
					if strings.Contains(string(out), "PRIVATE KEY") {
						t.Fatal("server key leaked")
					}
					if mode == "nginx" && input.Protocol != "vless-ws-tls" && (!strings.Contains(string(out), "CERTIFICATE") || parsed.TLSClientCertificate == "") {
						t.Fatal("trust certificate absent")
					}
					if mode == "nginx" && input.Protocol == "vless-ws-tls" {
						f := frontends[0]
						if !f.AutomaticCertificate || !f.UsesACME() || f.Certificate != "" || f.PrivateKey != "" || parsed.TLSClientCertificate != "" || strings.Contains(string(out), "CERTIFICATE") {
							t.Fatal("automatic Nginx certificate retained private material or custom trust")
						}
						clientTLS := outbound["tls"].(map[string]any)
						if clientTLS["server_name"] != input.TLSServerName || clientTLS["insecure"] == true || clientTLS["certificate"] != nil {
							t.Fatal("automatic Nginx client does not use hostname and system trust", string(out))
						}
					}
					encodedAgain, err := MarshalClientMetadataWithPrevious(parsed, encoded)
					if err != nil {
						t.Fatal(err)
					}
					if encodedAgain != encoded {
						t.Fatal("unchanged save rotated certificate")
					}
					parsed.Credential, _ = NewCredential(parsed.Protocol, parsed.Method)
					changed, err := Generate(core.EngineSingBox, parsed)
					if err != nil {
						t.Fatal(err)
					}
					merged, err := MutateGenerated(core.EngineSingBox, content, changed, input.Tag, "modify")
					if err != nil {
						t.Fatal(err)
					}
					if _, err = ManagedTLSFrontends(core.EngineSingBox, merged, map[string]string{input.Tag: encodedAgain}); err != nil {
						t.Fatal(err)
					}
					if _, err = ManagedTLSFrontends(core.EngineSingBox, strings.Replace(content, "127.0.0.1", "0.0.0.0", 1), map[string]string{input.Tag: encoded}); err == nil {
						t.Fatal("plaintext public backend accepted")
					}
				})
			}
		}
	}
}

func TestManagedTLSAuthoritySharedAcrossInbounds(t *testing.T) {
	protocol, _ := FindProtocol(core.EngineSingBox, "vmess-ws-tls")
	a, _ := NewPlan(protocol)
	a.TLSMode = "nginx"
	a.TLSServerName = "one.example.test"
	first, err := MarshalClientMetadataWithPrevious(a, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewPlan(protocol)
	b.TLSMode = "nginx"
	b.TLSServerName = "two.example.test"
	previous := PreviousManagedTLSMetadata(b, map[string]string{a.Tag: first}, "")
	second, err := MarshalClientMetadataWithPrevious(b, previous)
	if err != nil {
		t.Fatal(err)
	}
	var ma, mb clientMetadata
	_ = json.Unmarshal([]byte(first), &ma)
	_ = json.Unmarshal([]byte(second), &mb)
	if ma.TLSClientCertificate != mb.TLSClientCertificate || ma.TLSAuthorityKey != mb.TLSAuthorityKey {
		t.Fatal("workspace inbounds have incompatible trust roots")
	}
	if ma.Frontend.Certificate == mb.Frontend.Certificate || ma.Frontend.PrivateKey == mb.Frontend.PrivateKey {
		t.Fatal("different domains reused leaf identity")
	}
	snapshot, err := core.EncodeExecutionSnapshot("{}", []core.TLSFrontend{*ma.Frontend, *mb.Frontend})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot, strings.ReplaceAll(ma.TLSAuthorityKey, "\n", `\n`)) {
		t.Fatal("CA signing key sent to Agent")
	}
}

func TestManagedTLSModeSwitchClearsGeneratedNginxTrust(t *testing.T) {
	protocol, _ := FindProtocol(core.EngineSingBox, "vmess-ws-tls")
	nginx, _ := NewPlan(protocol)
	nginx.TLSMode, nginx.TLSServerName = "nginx", "tls.example.test"
	nginxMetadata, err := MarshalClientMetadataWithPrevious(nginx, "")
	if err != nil {
		t.Fatal(err)
	}
	var original clientMetadata
	if err = json.Unmarshal([]byte(nginxMetadata), &original); err != nil || original.TLSClientCertificate == "" || original.TLSAuthorityKey == "" {
		t.Fatalf("Nginx trust material missing: %v", err)
	}

	// Reconstruct a pre-upgrade VLESS Nginx preset using the legacy signer.
	original.Protocol, nginx.Protocol = "vless-ws-tls", "vless-ws-tls"
	legacy, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	nginxMetadata = string(legacy)
	for _, mode := range []string{"nginx", "caddy", "external", "native"} {
		t.Run(mode, func(t *testing.T) {
			next := nginx
			next.TLSMode = mode
			next.TLSClientCertificate = original.TLSClientCertificate
			if mode == "native" {
				next.Listen = "0.0.0.0"
				next.CertificatePath, next.PrivateKeyPath = "/etc/qcontrolhub/tls/server.crt", "/etc/qcontrolhub/tls/server.key"
			}
			encoded, err := MarshalClientMetadataWithPrevious(next, nginxMetadata)
			if err != nil {
				t.Fatal(err)
			}
			var metadata clientMetadata
			if err = json.Unmarshal([]byte(encoded), &metadata); err != nil {
				t.Fatal(err)
			}
			if metadata.TLSClientCertificate != "" || metadata.TLSAuthorityKey != "" {
				t.Fatal("previous Nginx trust material survived a frontend mode change")
			}
			switch mode {
			case "nginx":
				if metadata.Frontend == nil || !metadata.Frontend.AutomaticCertificate || metadata.Frontend.Certificate != "" || metadata.Frontend.PrivateKey != "" {
					t.Fatal("legacy certificate survived ACME migration")
				}
			case "caddy":
				if metadata.Frontend == nil || metadata.Frontend.Mode != "caddy" || metadata.ExternalFrontend != nil {
					t.Fatalf("Caddy metadata mismatch: %+v", metadata)
				}
			case "external":
				if metadata.ExternalFrontend == nil || metadata.ExternalFrontend.Mode != "external" || metadata.Frontend != nil {
					t.Fatalf("external metadata mismatch: %+v", metadata)
				}
			case "native":
				if metadata.Frontend != nil || metadata.ExternalFrontend != nil {
					t.Fatalf("native TLS retained a proxy frontend: %+v", metadata)
				}
			}
		})
	}
	customTrust, _, err := newFrontendAuthority()
	if err != nil {
		t.Fatal(err)
	}
	native := nginx
	native.TLSMode, native.TLSClientCertificate = "native", customTrust
	native.Listen = "0.0.0.0"
	native.CertificatePath, native.PrivateKeyPath = "/etc/qcontrolhub/tls/server.crt", "/etc/qcontrolhub/tls/server.key"
	encoded, err := MarshalClientMetadataWithPrevious(native, nginxMetadata)
	if err != nil {
		t.Fatal(err)
	}
	var preserved clientMetadata
	if err = json.Unmarshal([]byte(encoded), &preserved); err != nil || preserved.TLSClientCertificate != customTrust {
		t.Fatalf("custom native TLS trust was removed: %v", err)
	}
}
