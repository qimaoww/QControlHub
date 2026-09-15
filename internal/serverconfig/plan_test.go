package serverconfig

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestNewPlanRandomizesSensitiveValues(t *testing.T) {
	t.Parallel()
	protocol, ok := FindProtocol(core.EngineMihomo, ProtocolSS2022)
	if !ok {
		t.Fatal("SS2022 protocol not found")
	}
	first, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	if first.Port < 20000 || first.Port > 49151 || second.Port < 20000 || second.Port > 49151 || first.Credential == second.Credential || first.Tag == second.Tag {
		t.Fatalf("plans were not independently randomized: first=%+v second=%+v", first, second)
	}
	if !strings.HasPrefix(first.Username, "qch-") || !strings.HasPrefix(second.Username, "qch-") {
		t.Fatalf("plans use an unexpected username prefix: first=%q second=%q", first.Username, second.Username)
	}
}

func TestRegeneratePlanPreservesCurrentSelections(t *testing.T) {
	t.Parallel()
	protocol, ok := FindProtocol(core.EngineXray, ProtocolVMess)
	if !ok {
		t.Fatal("VMess protocol not found")
	}
	credential, err := NewCredential(ProtocolVMess, "")
	if err != nil {
		t.Fatal(err)
	}
	current := Input{
		Protocol: ProtocolVMess, Tag: "unsaved-tag", Listen: "::", Port: 10443,
		Username: "unsaved-user", Credential: credential, Transport: "grpc", TransportPath: "unsaved-service",
		TLSEnabled: true, CertificatePath: "/custom/certificate.pem", PrivateKeyPath: "/custom/private-key.pem",
	}

	regenerated, err := RegeneratePlan(protocol, current)
	if err != nil {
		t.Fatal(err)
	}
	if regenerated.Listen != current.Listen || regenerated.Transport != current.Transport || !regenerated.TLSEnabled || regenerated.CertificatePath != current.CertificatePath || regenerated.PrivateKeyPath != current.PrivateKeyPath {
		t.Fatalf("regeneration lost current selections: current=%+v regenerated=%+v", current, regenerated)
	}
	if regenerated.Tag == current.Tag || regenerated.Port == current.Port || regenerated.Username == current.Username || regenerated.Credential == current.Credential || regenerated.TransportPath == current.TransportPath {
		t.Fatalf("regeneration did not replace every randomized VMess value: current=%+v regenerated=%+v", current, regenerated)
	}
	if strings.HasPrefix(regenerated.TransportPath, "/") {
		t.Fatalf("gRPC service name used a WebSocket path: %q", regenerated.TransportPath)
	}
	if _, err := Generate(core.EngineXray, regenerated); err != nil {
		t.Fatalf("regenerated current plan is invalid: %v", err)
	}
}

func TestRegeneratePlanUsesCurrentEncryptionMethod(t *testing.T) {
	t.Parallel()
	protocol, ok := FindProtocol(core.EngineShadowsocksRust, ProtocolSS2022)
	if !ok {
		t.Fatal("SS2022 protocol not found")
	}
	current := Input{
		Protocol: ProtocolSS2022, Listen: "127.0.0.1", Username: "unsaved-user",
		Transport: "raw",
	}
	for method, keyBytes := range map[string]int{
		"2022-blake3-aes-128-gcm": 16,
		"2022-blake3-aes-256-gcm": 32,
	} {
		current.Method = method
		regenerated, err := RegeneratePlan(protocol, current)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.StdEncoding.DecodeString(regenerated.Credential)
		if err != nil {
			t.Fatal(err)
		}
		if regenerated.Method != method || len(decoded) != keyBytes {
			t.Fatalf("regeneration ignored current method %q: %+v", method, regenerated)
		}
	}
	current.Method = "not-a-method"
	if _, err := RegeneratePlan(protocol, current); !errors.Is(err, ErrInvalidPlanInput) {
		t.Fatalf("invalid current method error = %v", err)
	}
}
