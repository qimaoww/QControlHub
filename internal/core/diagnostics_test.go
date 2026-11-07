package core

import (
	"strings"
	"testing"
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
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		want := diagnosticPrivatePEM.ReplaceAllString(raw, "[redacted private PEM]")
		want = diagnosticAssignments.ReplaceAllString(want, "${1}[redacted]")
		want = diagnosticBearer.ReplaceAllString(want, "Bearer [redacted]")
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
