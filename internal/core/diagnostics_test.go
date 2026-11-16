package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticRedactionMasksKeyAssignmentsPEMAndHistoricalPaths(t *testing.T) {
	for _, raw := range []string{
		`invalid "privateKey": secret-value`,
		`reality-config: {"private-key":"secret-value"}`,
		"private_key=secret-value",
		"证书错误 private-key: secret-value",
		"-----BEGIN PRIVATE KEY-----\nsecret-value\n-----END PRIVATE KEY-----",
		"read certificate/key /var/lib/qcontrolhub-mihomo/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA as qcontrolhub-core: exit status 1",
		"read certificate/key /var/lib/qcontrolhub-mihomo-shr_0000000000000001/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA as qcontrolhub-core: exit status 1",
	} {
		got := RedactDiagnostic(raw)
		if strings.Contains(got, "secret-value") || strings.Contains(got, strings.Repeat("A", 43)) || !strings.Contains(got, "[redacted") {
			t.Fatalf("secret survived diagnostic redaction: %s", got)
		}
	}
	path := "certificate file access at listeners[1].private-key (/etc/qcontrolhub/tls/server.key): permission denied"
	if got := RedactDiagnostic(path); got != path {
		t.Fatalf("lost actionable field or file location: %s", got)
	}
	path = "certificate file access at listeners[1].private-key (/var/lib/qcontrolhub-mihomo/" + strings.Repeat("A", 43) + "): permission denied"
	if got := RedactDiagnostic(path); got != path {
		t.Fatalf("a real file was mistaken for a historical inline key: %s", got)
	}
}

// Keep the previous replacement sequence as an independent output oracle.
// This covers replacement expansion, cascading matches, malformed encodings
// and Unicode case folding, which a literal credential prefilter could miss.
func FuzzDiagnosticRedactionEquivalent(f *testing.F) {
	for _, seed := range []string{
		"ordinary diagnostic line", "", "\xff\x00",
		`private_key="secret" token=abc api_key='xyz'`,
		"BeArEr abc123\nqch_key_" + strings.Repeat("A", 43),
		"https://user:pass@example.invalid/?token=abc&password=def",
		"-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----",
		"-----BEGIN EC PRIVATE KEY-----\nunclosed secret",
		"a1234567-1234-1234-1234-123456789abc",
		"read certificate/key /var/lib/qcontrolhub-mihomo/" + strings.Repeat("A", 43),
		"PRIVATE_KEY=secret\nmldſa65_ſeed: secret",
		"AUTHORIZATION: hidden\nCoOkIe: hidden\npre-ſhared-Key=hidden",
		"paſſword=hidden\nBearer ſecret\nhttps://user:pass@host.test/",
		"a1234567-1234-1234-1234-123456789abc safe-------diagnostic",
		`{"short_ids":["abcd"],"authentication":{"password":"secret"}}`,
		"[TCP] 192.0.2.1:443 --> probe.invalid:8443 using DIRECT",
		"webhook_url=https://hooks.example.test/opaque-endpoint",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		want := diagnosticPrivatePEM.ReplaceAllString(redactDiagnosticContainers(raw), "[redacted private PEM]")
		want = diagnosticBearer.ReplaceAllString(want, "Bearer [redacted]")
		want = diagnosticHeaders.ReplaceAllString(want, "${1}[redacted]")
		want = diagnosticAssignments.ReplaceAllString(want, "${1}[redacted]")
		want = diagnosticAPIKey.ReplaceAllString(want, "[redacted]")
		want = diagnosticUUID.ReplaceAllString(want, "[redacted UUID]")
		want = diagnosticURLCredentials.ReplaceAllString(want, "${1}[redacted]@")
		want = diagnosticURLSecret.ReplaceAllString(want, "${1}[redacted]")
		want = diagnosticKeyAssignment.ReplaceAllString(want, "${1}[redacted]")
		want = diagnosticLegacyRealityPath.ReplaceAllString(want, "${1}[redacted]${2}")
		if got := RedactDiagnostic(raw); got != want {
			t.Fatalf("redaction differs for %q: got %q, want %q", raw, got, want)
		}
	})
}

func TestDiagnosticRedactionContextDiscardsCanceledResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value, err := RedactDiagnosticValueContext(ctx, map[string]any{"message": "password=secret"})
	if value != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled redaction returned %v, %v", value, err)
	}
}

func TestDiagnosticRedactionContextStopsTraversal(t *testing.T) {
	entries := make([]any, 8000)
	for i := range entries {
		entries[i] = map[string]any{"message": strings.Repeat("password=fixture-secret diagnostic ", 100)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	value, err := RedactDiagnosticValueContext(ctx, entries)
	if value != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("traversal did not discard expired data: err=%v elapsed=%s", err, time.Since(started))
	}
	if safe, err := RedactDiagnosticValueContext(context.Background(), "password=fixture-secret"); err != nil || strings.Contains(safe.(string), "fixture-secret") {
		t.Fatal("fresh read did not recover safely")
	}
}

func BenchmarkDiagnosticPlainLogRedaction(b *testing.B) {
	message := strings.Repeat("[TCP] 192.0.2.1:443 --> probe.invalid:8443 using DIRECT ", 75)
	b.SetBytes(int64(len(message)))
	for b.Loop() {
		if got := RedactDiagnostic(message); got != message {
			b.Fatal("plain log changed")
		}
	}
}

func TestDiagnosticRedactionIsIdempotentAndMasksWholeCredentials(t *testing.T) {
	raw := "operation failed\nAuthorization: Bearer secret-token\nCookie: session=secret-cookie; csrf=second-cookie\npassword=secret-password\nroot cause: permission denied"
	once := DiagnosticText(raw)
	if strings.Contains(once, "secret-") || strings.Contains(once, "second-cookie") || !strings.Contains(once, "root cause: permission denied") {
		t.Fatalf("redaction lost safety or cause: %q", once)
	}
	if again := DiagnosticText(once); again != once {
		t.Fatalf("redaction is not idempotent: %q", again)
	}
}

func TestDiagnosticCredentialContainersPreserveSurroundingCauses(t *testing.T) {
	raw := `validation failed at inbound.authentication: {"passwords":["first-secret","second-secret"],"tokens":{"a":"third-secret"}}; nested cause: bind failed`
	got := DiagnosticText(raw)
	if strings.Contains(got, "-secret") || !strings.Contains(got, "nested cause: bind failed") || !strings.Contains(got, "inbound.authentication") {
		t.Fatalf("unsafe or incomplete diagnostic: %s", got)
	}
	if next := DiagnosticText(got); next != got {
		t.Fatalf("container redaction changed twice: %s", next)
	}
}
