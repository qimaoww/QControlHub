package core

import "testing"

func TestExecutionSnapshotDoesNotInterpretNativeJSONAsMetadata(t *testing.T) {
	native := `{"qcontrolhub_deployment":1,"content":"different","tls_frontends":[]}`
	decoded, frontends, err := DecodeExecutionSnapshot(native)
	if err != nil || decoded != native || len(frontends) != 0 {
		t.Fatal("native user configuration interpreted as trusted task metadata")
	}
	encoded, err := EncodeExecutionSnapshot(native, nil)
	if err != nil {
		t.Fatal(err)
	}
	decoded, frontends, err = DecodeExecutionSnapshot(encoded)
	if err != nil || decoded != native || len(frontends) != 0 {
		t.Fatal("snapshot was decoded recursively")
	}
}

func TestTLSFrontendAutomaticNginxValidation(t *testing.T) {
	f := TLSFrontend{Tag: "ws", Mode: "nginx", AutomaticCertificate: true, Domain: "tls.example.test", PublicPort: 443, BackendPort: 23456, Transport: "websocket", Path: "/ws"}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*TLSFrontend){
		"IP":                     func(f *TLSFrontend) { f.Domain = "127.0.0.1" },
		"single label":           func(f *TLSFrontend) { f.Domain = "localhost" },
		"challenge public port":  func(f *TLSFrontend) { f.PublicPort = 80 },
		"challenge backend port": func(f *TLSFrontend) { f.BackendPort = 80 },
		"inline certificate":     func(f *TLSFrontend) { f.Certificate, f.PrivateKey = "certificate", "key" },
		"local target":           func(f *TLSFrontend) { f.LocalOnly = true },
		"other provider":         func(f *TLSFrontend) { f.Mode = "caddy" },
		"other transport":        func(f *TLSFrontend) { f.Transport = "httpupgrade" },
		"bad email":              func(f *TLSFrontend) { f.ACMEEmail = "bad email" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := f
			change(&bad)
			if err := bad.Validate(); err == nil {
				t.Fatal("invalid automatic frontend accepted")
			}
		})
	}
}
