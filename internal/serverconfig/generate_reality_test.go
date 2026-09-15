package serverconfig

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestVLESSXHTTPRealityPlansMatchBothCoreShapes(t *testing.T) {
	t.Parallel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray} {
		engine := engine
		t.Run(string(engine), func(t *testing.T) {
			t.Parallel()
			protocol, ok := FindProtocol(engine, ProtocolVLESSXHTTP)
			if !ok {
				t.Fatalf("%s XHTTP preset was not published", engine)
			}
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			if input.Flow != "" || input.Transport != "xhttp" || !strings.HasPrefix(input.TransportPath, "/") {
				t.Fatalf("XHTTP plan = %+v", input)
			}
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			for _, marker := range []string{"xhttp", input.TransportPath, input.RealityPrivateKey} {
				if !strings.Contains(content, marker) {
					t.Fatalf("%s XHTTP config omitted %q:\n%s", engine, marker, content)
				}
			}
			parsed, ok := Parse(engine, content)
			if !ok || parsed.Protocol != ProtocolVLESSXHTTP || parsed.Transport != "xhttp" || parsed.TransportPath != input.TransportPath || parsed.Flow != "" {
				t.Fatalf("Parse(%s/XHTTP) = %+v, %v", engine, parsed, ok)
			}
			profile, err := BuildClientProfile(parsed, "edge.example.com", "")
			if err != nil {
				t.Fatal(err)
			}
			parsedURI, err := url.Parse(profile.URI)
			if err != nil || parsedURI.Query().Get("type") != "xhttp" || parsedURI.Query().Get("path") != input.TransportPath || parsedURI.Query().Get("flow") != "" {
				t.Fatalf("XHTTP client URI = %q, err = %v", profile.URI, err)
			}
		})
	}
}

func TestXrayRealityMLDSASeedPersistsAndDerivesClientVerify(t *testing.T) {
	t.Parallel()
	protocol, _ := FindProtocol(core.EngineXray, ProtocolVLESSXHTTP)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	input.RealityMLDSA65Seed = base64.RawURLEncoding.EncodeToString(seed)
	verify, err := mldsa65VerifyFromSeed(input.RealityMLDSA65Seed)
	if err != nil {
		t.Fatal(err)
	}
	input.RealityMLDSA65Verify = verify
	content, err := Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, `"mldsa65Seed"`) || strings.Contains(content, `"mldsa65Verify"`) {
		t.Fatalf("server config must persist only the ML-DSA seed:\n%s", content)
	}
	parsed, ok := Parse(core.EngineXray, content)
	if !ok || parsed.RealityMLDSA65Seed != input.RealityMLDSA65Seed || parsed.RealityMLDSA65Verify != verify {
		t.Fatalf("Parse(Xray/ML-DSA) = %+v, %v", parsed, ok)
	}
	profile, err := BuildClientProfile(parsed, "edge.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile.URI, "pqv=") || strings.Contains(profile.URI, input.RealityMLDSA65Seed) {
		t.Fatalf("client profile did not export only the ML-DSA verify value: %+v", profile)
	}
}

func TestRegenerateXrayRealityProvidesOptionalMLDSAKeyPair(t *testing.T) {
	t.Parallel()
	protocol, ok := FindProtocol(core.EngineXray, ProtocolVLESSXHTTP)
	if !ok || !protocol.SupportsRealityMLDSA {
		t.Fatal("Xray XHTTP preset does not advertise ML-DSA support")
	}
	current, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	if current.RealityMLDSA65Seed != "" || current.RealityMLDSA65Verify != "" {
		t.Fatal("new plans must not enable optional ML-DSA automatically")
	}
	regenerated, err := RegeneratePlan(protocol, current)
	if err != nil {
		t.Fatal(err)
	}
	if !validRawURLBase64Size(regenerated.RealityMLDSA65Seed, 32) || !validRawURLBase64Size(regenerated.RealityMLDSA65Verify, 1952) {
		t.Fatalf("regenerated ML-DSA material is invalid: seed=%d verify=%d", len(regenerated.RealityMLDSA65Seed), len(regenerated.RealityMLDSA65Verify))
	}
	if _, err := Generate(core.EngineXray, regenerated); err != nil {
		t.Fatalf("generated ML-DSA pair was rejected: %v", err)
	}
}

func TestRegeneratePlanReplacesRealityMaterialTogether(t *testing.T) {
	t.Parallel()
	protocol, ok := FindProtocol(core.EngineXray, ProtocolVLESS)
	if !ok {
		t.Fatal("VLESS protocol not found")
	}
	current, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	current.RealityServerName = "current.example.com"
	regenerated, err := RegeneratePlan(protocol, current)
	if err != nil {
		t.Fatal(err)
	}
	if regenerated.RealityPrivateKey == current.RealityPrivateKey || regenerated.RealityPublicKey == current.RealityPublicKey || regenerated.RealityShortID == current.RealityShortID {
		t.Fatalf("Reality material was not replaced together: current=%+v regenerated=%+v", current, regenerated)
	}
	if regenerated.RealityServerName != current.RealityServerName {
		t.Fatalf("Reality server name = %q, want current %q", regenerated.RealityServerName, current.RealityServerName)
	}
	if _, err := Generate(core.EngineXray, regenerated); err != nil {
		t.Fatalf("regenerated Reality key pair is invalid: %v", err)
	}
}

func TestVLESSRealityPlansGenerateAndRoundTripKeys(t *testing.T) {
	t.Parallel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		protocol, _ := FindProtocol(engine, ProtocolVLESS)
		input, err := NewPlan(protocol)
		if err != nil {
			t.Fatal(err)
		}
		if !input.RealityEnabled || input.RealityPrivateKey == "" || input.RealityPublicKey == "" || len(input.RealityShortID) != 16 {
			t.Fatalf("NewPlan(%s) did not create Reality material: %+v", engine, input)
		}
		if strings.Contains(strings.ToLower(input.RealityServerName), "cloudflare") {
			t.Fatalf("NewPlan(%s) selected a Cloudflare Reality target: %s", engine, input.RealityServerName)
		}
		content, err := Generate(engine, input)
		if err != nil {
			t.Fatalf("Generate(%s): %v", engine, err)
		}
		if engine == core.EngineXray {
			var root map[string]any
			if err := json.Unmarshal([]byte(content), &root); err != nil {
				t.Fatal(err)
			}
			inbound := firstMap(root["inbounds"])
			reality := mapValue(mapValue(inbound["streamSettings"])["realitySettings"])
			if got := stringValue(reality["minClientVer"]); got != "0.0.0" {
				t.Fatalf("Xray Reality minClientVer = %q", got)
			}
		}
		parsed, ok := Parse(engine, content)
		if !ok || !parsed.RealityEnabled || parsed.RealityPublicKey != input.RealityPublicKey || parsed.RealityShortID != input.RealityShortID {
			t.Errorf("Parse(%s) lost Reality material: %+v, %v", engine, parsed, ok)
		}
	}
}

func TestXrayRealityMinClientVersionIsCustomizableWithoutChangingLegacyDefault(t *testing.T) {
	t.Parallel()
	protocol, _ := FindProtocol(core.EngineXray, ProtocolVLESSXHTTP)
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	if input.RealityMinClientVer != "0.0.0" {
		t.Fatalf("legacy minClientVer default = %q", input.RealityMinClientVer)
	}
	input.RealityMinClientVer = "26.3.27"
	content, err := Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := Parse(core.EngineXray, content)
	if !ok || parsed.RealityMinClientVer != "26.3.27" {
		t.Fatalf("custom minClientVer round trip = %+v, %v", parsed, ok)
	}
}
