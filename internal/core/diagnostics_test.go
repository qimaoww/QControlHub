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
}
