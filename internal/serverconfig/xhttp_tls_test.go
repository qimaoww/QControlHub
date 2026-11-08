package serverconfig

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestXHTTPTLSModesRoundTripAndExports(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineMihomo} {
		protocol, ok := FindProtocol(engine, ProtocolXHTTPTLS)
		if !ok || !protocol.SupportsManagedTLS {
			t.Fatal("missing TLS catalog entry", engine)
		}
		plan, err := NewPlan(protocol)
		if err != nil {
			t.Fatal(err)
		}
		if plan.TLSMode != "caddy" || plan.TLSServerName != "" || plan.CertificatePath != "" || plan.Listen != "127.0.0.1" {
			t.Fatal("new plan must require a real domain", engine)
		}
		var previous string
		for _, mode := range []string{"caddy", "nginx", "native", "nginx", "caddy", "native"} {
			t.Run(string(engine)+"/"+mode, func(t *testing.T) {
				plan.TLSMode, plan.TLSServerName = mode, "tls.example.test"
				plan.Port, plan.TLSPublicPort = 23456, 8443
				plan.TLSStaticHTML = "<html><body>owned site</body></html>"
				plan.Listen = "127.0.0.1"
				if mode == "native" {
					plan.Listen, plan.TLSPublicPort, plan.TLSStaticHTML = "::", 0, ""
					plan.CertificatePath, plan.PrivateKeyPath = "/etc/qcontrolhub/tls/test.crt", "/etc/qcontrolhub/tls/test.key"
				} else {
					plan.CertificatePath, plan.PrivateKeyPath = "", ""
				}
				content, err := Generate(engine, plan)
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := MarshalClientMetadataWithPrevious(plan, previous)
				if err != nil {
					t.Fatal(err)
				}
				previous = metadata
				parsed, ok := Parse(engine, content)
				if !ok {
					t.Fatal("cannot parse generated source")
				}
				if err = ApplyClientMetadata(&parsed, metadata); err != nil {
					t.Fatal(err)
				}
				if parsed.Protocol != ProtocolXHTTPTLS || parsed.TLSServerName != plan.TLSServerName || !parsed.TLSEnabled || parsed.TLSStaticHTML != plan.TLSStaticHTML || parsed.TLSPublicPort != plan.TLSPublicPort {
					t.Fatalf("lost TLS mode or endpoint: %+v", parsed)
				}
				frontends, err := ManagedTLSFrontends(engine, content, map[string]string{plan.Tag: metadata})
				if err != nil {
					t.Fatal(err)
				}
				if mode == "native" {
					if len(frontends) != 0 || parsed.TLSMode != "" {
						t.Fatal("native configuration migrated to a managed mode")
					}
				} else if len(frontends) != 1 || frontends[0].LocalOnly || frontends[0].Transport != "xhttp" || frontends[0].AutomaticCertificate != (mode == "nginx") || parsed.TLSMode != mode {
					t.Fatalf("wrong frontend: %+v", frontends)
				}
				profile, err := BuildClientProfile(parsed, "203.0.113.10", "")
				if err != nil {
					t.Fatal(err)
				}
				uri, err := url.Parse(profile.URI)
				if err != nil {
					t.Fatal(err)
				}
				wantPort := "8443"
				if mode == "native" {
					wantPort = "23456"
				}
				if uri.Port() != wantPort || uri.Query().Get("sni") != plan.TLSServerName || uri.Query().Get("path") != plan.TransportPath || uri.Query().Get("security") != "tls" {
					t.Fatal("wrong public URI", profile.URI)
				}
				if strings.Contains(profile.Xray, `"allowInsecure": true`) || strings.Contains(profile.Mihomo, "skip-cert-verify: true") {
					t.Fatal("TLS validation disabled")
				}
				if mode != "native" && (strings.Contains(profile.Xray, "23456") || strings.Contains(profile.Mihomo, "23456")) {
					t.Fatal("backend port exported")
				}
				if engine == core.EngineXray {
					outbound, err := BuildClientOutbound(engine, parsed, "203.0.113.10", "")
					if err != nil || mode != "native" && strings.Contains(string(outbound), "23456") {
						t.Fatal("outbound exported backend port", err)
					}
				}
				if mode != "native" {
					for _, changed := range []string{strings.ReplaceAll(content, "127.0.0.1", "0.0.0.0"), strings.ReplaceAll(content, plan.TransportPath, "/edited"), strings.ReplaceAll(content, plan.Credential, "new-credential"), strings.ReplaceAll(content, plan.Tag, "renamed")} {
						if _, err := ManagedTLSFrontends(engine, changed, map[string]string{plan.Tag: metadata}); err == nil {
							t.Fatal("detached source was accepted")
						}
					}
					if _, err := Generate(engine, func() Input { x := plan; x.Listen = "0.0.0.0"; return x }()); err == nil {
						t.Fatal("public plaintext backend was accepted")
					}
				}
				regenerated, err := RegeneratePlan(protocol, plan)
				if err != nil || regenerated.TLSMode != mode || regenerated.TLSServerName != plan.TLSServerName || regenerated.TLSStaticHTML != plan.TLSStaticHTML || regenerated.Credential == plan.Credential || regenerated.TransportPath == plan.TransportPath {
					t.Fatal("regeneration lost customizations or reused credentials", err)
				}
			})
		}
	}
}

func TestXHTTPFiveRequiresRealTLSResources(t *testing.T) {
	protocol, _ := FindProtocol(core.EngineXray, ProtocolXHTTPFive)
	plan, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TLSMode != "" || plan.RealityServerName != "" || plan.TLSServerName != "" || plan.CertificatePath != "" || plan.PrivateKeyPath != "" || len(plan.RealityShortID) != 16 || len(plan.TransportPath) != 33 {
		t.Fatal("five-in-one must not invent external resources")
	}
	if _, err = Generate(core.EngineXray, plan); err == nil {
		t.Fatal("incomplete plan was usable")
	}
	plan.TLSServerName, plan.RealityServerName = "tls.example.test", "reality.example.test"
	plan.CertificatePath, plan.PrivateKeyPath = "/etc/qcontrolhub/tls/test.crt", "/etc/qcontrolhub/tls/test.key"
	content, err := Generate(core.EngineXray, plan)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal([]byte(content), &root)
	if len(root["inbounds"].([]any)) != 2 {
		t.Fatal("lost shared backend")
	}
	profile, err := BuildClientProfile(plan, "203.0.113.10", "")
	if err != nil || len(profile.Variants) != 5 {
		t.Fatal("lost five clients", err)
	}
}

func TestMihomoXHTTPServerOptionsAreNativeAndRoundTrip(t *testing.T) {
	protocol, _ := FindProtocol(core.EngineMihomo, ProtocolXHTTPTLS)
	plan, _ := NewPlan(protocol)
	plan.TLSServerName = "tls.example.test"
	plan.XHTTPServerExtra = `{"noSSEHeader":true,"scMaxEachPostBytes":"500000-1000000","xPaddingBytes":"100-1000"}`
	content, err := Generate(core.EngineMihomo, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"no-sse-header: true", "sc-max-each-post-bytes: 500000-1000000", "x-padding-bytes: 100-1000"} {
		if !strings.Contains(content, field) {
			t.Fatal("not a native server option", field)
		}
	}
	parsed, ok := Parse(core.EngineMihomo, content)
	if !ok || parsed.XHTTPServerExtra != plan.XHTTPServerExtra {
		t.Fatal("lost advanced server options", parsed.XHTTPServerExtra)
	}
	plan.XHTTPServerExtra = `{"unsupportedOption":true}`
	if _, err = Generate(core.EngineMihomo, plan); err == nil {
		t.Fatal("unsupported server field silently discarded")
	}
}
