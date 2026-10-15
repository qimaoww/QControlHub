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
	return NewPlan(protocol)
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
					if strings.Contains(string(out), "PRIVATE KEY") {
						t.Fatal("server key leaked")
					}
					if mode == "nginx" && (!strings.Contains(string(out), "CERTIFICATE") || parsed.TLSClientCertificate == "") {
						t.Fatal("trust certificate absent")
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
	protocol, _ := FindProtocol(core.EngineSingBox, "vless-ws-tls")
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
