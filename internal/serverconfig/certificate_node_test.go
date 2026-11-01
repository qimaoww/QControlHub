package serverconfig

import (
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestNodeTLSReferencesOnlyOrdinaryServerPairs(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			input := Input{Protocol: ProtocolTrojan, Tag: "native", Listen: "0.0.0.0", Port: 24443, Username: "fixture", Credential: "node-tls-test-password", TLSEnabled: true, TLSServerName: "edge.example.test", CertificatePath: "/etc/tls/server.pem", PrivateKeyPath: "/etc/tls/server.key"}
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			refs := NodeTLSCertificateReferences(engine, content, nil)
			if len(refs) != 1 || refs[0].CertificatePath != input.CertificatePath || refs[0].PrivateKeyPath != input.PrivateKeyPath {
				t.Fatalf("server pair was not recognized for %s", engine)
			}
			metadata, err := MarshalClientMetadata(input)
			if err != nil {
				t.Fatal(err)
			}
			bound := NodeTLSCertificateReferences(engine, content, map[string]string{input.Tag: metadata})
			if len(bound) != 1 || bound[0].Domain != input.TLSServerName {
				t.Fatal("native domain metadata did not follow the exact file pair")
			}
			if engine == core.EngineMihomo {
				stale := input
				stale.CertificatePath = "/etc/tls/changed.pem"
				changed, err := Generate(engine, stale)
				if err != nil {
					t.Fatal(err)
				}
				refs := NodeTLSCertificateReferences(engine, changed, map[string]string{input.Tag: metadata})
				if len(refs) != 1 || refs[0].Domain != "" {
					t.Fatal("stale metadata supplied a domain for another certificate pair")
				}
			}
			input.CertificatePath = core.PanelCertificatePrefix + "crt_0123456789abcdef/cert.pem"
			input.PrivateKeyPath = core.PanelCertificatePrefix + "crt_0123456789abcdef/key.pem"
			content, err = Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			if len(NodeTLSCertificateReferences(engine, content, nil)) != 0 {
				t.Fatal("panel certificate re-enrolled as node files")
			}
		})
	}
	for _, tc := range []struct {
		engine  core.Engine
		content string
	}{
		{core.EngineSingBox, `{"inbounds":[{"type":"vless","tag":"reality","listen_port":443,"tls":{"enabled":true,"reality":{"enabled":true,"private_key":"/etc/private.key"},"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem"}}]}`},
		{core.EngineSingBox, `{"outbounds":[{"type":"trojan","tls":{"enabled":true,"certificate_path":"/etc/client.pem","key_path":"/etc/client.key"}}]}`},
		{core.EngineXray, `{"inbounds":[{"protocol":"vless","streamSettings":{"security":"reality","realitySettings":{"privateKey":"/etc/key.pem"}}}]}`},
		{core.EngineMihomo, "proxies:\n - name: client\n   type: trojan\n   certificate: /etc/client.pem\n   private-key: /etc/client.key\n"},
	} {
		if len(NodeTLSCertificateReferences(tc.engine, tc.content, nil)) != 0 {
			t.Fatal("non-server material became eligible")
		}
	}
}

func TestNodeTLSSharedPathBoundary(t *testing.T) {
	share := "shr_0123456789abcdef"
	if !core.ValidAgentShareID(share) {
		t.Fatal("invalid fixture share")
	}
	root := "/etc/qagent/sing-box/shares/" + share + "/"
	if !NodeTLSCertificatePathAllowed(core.EngineSingBox, share, root+"tls/key.pem") {
		t.Fatal("own instance file refused")
	}
	for _, path := range []string{"/etc/private/key.pem", root + "../foreign/key.pem", "/etc/qagent/sing-box/shares/shr_fedcba9876543210/key.pem", core.PanelCertificatePrefix + "crt_0123456789abcdef/key.pem", "relative/key.pem"} {
		if NodeTLSCertificatePathAllowed(core.EngineSingBox, share, path) {
			t.Fatal("shared path escaped instance")
		}
	}
	if NodeTLSCertificatePathAllowed(core.EngineSingBox, "", root+"key.pem") {
		t.Fatal("base instance exported shared user file")
	}
}
