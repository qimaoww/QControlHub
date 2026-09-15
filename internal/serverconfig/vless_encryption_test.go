package serverconfig

import (
	"net/url"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestVLESSEncryptionRealityVisionPlansRoundTripAndExportClientKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		protocol  string
		transport string
		uriType   string
	}{
		{ProtocolVLESSEncTCP, "raw", "tcp"},
		{ProtocolVLESSEncXHTTP, "xhttp", "xhttp"},
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray} {
		engine := engine
		for _, test := range tests {
			test := test
			t.Run(string(engine)+"/"+test.protocol, func(t *testing.T) {
				t.Parallel()
				protocol, ok := FindProtocol(engine, test.protocol)
				if !ok || !protocol.UsesVLESSEncryption {
					t.Fatalf("%s protocol %q does not advertise VLESS Encryption", engine, test.protocol)
				}
				input, err := NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				if input.Transport != test.transport || input.Flow != "xtls-rprx-vision" || input.VLESSDecryption == "" || input.VLESSEncryption == "" {
					t.Fatalf("VLESS-ENC plan = %+v", input)
				}
				if err := validateVLESSEncryptionPair(input.VLESSDecryption, input.VLESSEncryption); err != nil {
					t.Fatal(err)
				}
				content, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(content, input.VLESSDecryption) || strings.Contains(content, input.VLESSEncryption) || !strings.Contains(content, "xtls-rprx-vision") {
					t.Fatalf("%s server configuration mixed up VLESS-ENC key roles:\n%s", engine, content)
				}
				parsed, ok := Parse(engine, content)
				if !ok || parsed.Protocol != test.protocol || parsed.VLESSDecryption != input.VLESSDecryption || parsed.VLESSEncryption != input.VLESSEncryption {
					t.Fatalf("Parse(%s/%s) = %+v, %v", engine, test.protocol, parsed, ok)
				}
				profile, err := BuildClientProfile(parsed, "edge.example.com", "")
				if err != nil {
					t.Fatal(err)
				}
				parsedURI, err := url.Parse(profile.URI)
				if err != nil {
					t.Fatal(err)
				}
				query := parsedURI.Query()
				if query.Get("type") != test.uriType || query.Get("flow") != "xtls-rprx-vision" || query.Get("encryption") != input.VLESSEncryption || strings.Contains(profile.URI, input.VLESSDecryption) {
					t.Fatalf("VLESS-ENC client URI = %q", profile.URI)
				}
			})
		}
	}
}

func TestVLESSEncryptionRejectsMismatchedClientKey(t *testing.T) {
	t.Parallel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray} {
		protocol, _ := FindProtocol(engine, ProtocolVLESSEncTCP)
		input, err := NewPlan(protocol)
		if err != nil {
			t.Fatal(err)
		}
		_, otherEncryption, err := newVLESSEncryptionPair()
		if err != nil {
			t.Fatal(err)
		}
		input.VLESSEncryption = otherEncryption
		if _, err := Generate(engine, input); err == nil || !strings.Contains(err.Error(), "不属于同一密钥对") {
			t.Fatalf("%s mismatched VLESS-ENC pair error = %v", engine, err)
		}
	}
}

// The plain preset runs VLESS Encryption directly over native TCP. Xray allows
// security "none" once VLESS Encryption is on, and Mihomo treats decryption as
// equivalent to a certificate or Reality config; sing-box supports neither, so
// it must keep advertising no such preset.
func TestPlainVLESSEncryptionPresetRunsWithoutTLSOrReality(t *testing.T) {
	t.Parallel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray} {
		engine := engine
		t.Run(string(engine), func(t *testing.T) {
			t.Parallel()
			protocol, ok := FindProtocol(engine, ProtocolVLESSEncPlain)
			if !ok || !protocol.UsesVLESSEncryption || protocol.UsesReality || protocol.DefaultTLS {
				t.Fatalf("%s plain VLESS-ENC preset = %+v (%v)", engine, protocol, ok)
			}
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			if input.Transport != "raw" || input.Flow != "" || input.RealityEnabled || input.TLSEnabled ||
				input.VLESSDecryption == "" || input.VLESSEncryption == "" {
				t.Fatalf("%s plain VLESS-ENC plan = %+v", engine, input)
			}
			if err := validateVLESSEncryptionPair(input.VLESSDecryption, input.VLESSEncryption); err != nil {
				t.Fatal(err)
			}
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(content, input.VLESSDecryption) || strings.Contains(content, input.VLESSEncryption) {
				t.Fatalf("%s plain VLESS-ENC configuration mixed up the key roles:\n%s", engine, content)
			}
			for _, forbidden := range []string{"reality", "xtls-rprx-vision", "certificate", "private-key"} {
				if strings.Contains(content, forbidden) {
					t.Fatalf("%s plain VLESS-ENC configuration leaked %q:\n%s", engine, forbidden, content)
				}
			}
			parsed, ok := Parse(engine, content)
			if !ok || parsed.Protocol != ProtocolVLESSEncPlain || parsed.VLESSDecryption != input.VLESSDecryption ||
				parsed.RealityEnabled || parsed.TLSEnabled {
				t.Fatalf("Parse(%s/%s) = %+v (%v)", engine, ProtocolVLESSEncPlain, parsed, ok)
			}
			profile, err := BuildClientProfile(parsed, "edge.example.com", "")
			if err != nil {
				t.Fatal(err)
			}
			parsedURI, err := url.Parse(profile.URI)
			if err != nil {
				t.Fatal(err)
			}
			query := parsedURI.Query()
			if query.Get("type") != "tcp" || query.Get("encryption") != input.VLESSEncryption ||
				query.Get("flow") != "" || query.Get("security") != "" || strings.Contains(profile.URI, input.VLESSDecryption) {
				t.Fatalf("plain VLESS-ENC client URI = %q", profile.URI)
			}
		})
	}
	if _, ok := FindProtocol(core.EngineSingBox, ProtocolVLESSEncPlain); ok {
		t.Fatal("sing-box has no VLESS Encryption support and must not advertise the plain preset")
	}
}
