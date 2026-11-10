package core

import (
	"strings"
	"testing"
)

func TestRedactedConfigurationEditPreservesSecrets(t *testing.T) {
	for _, original := range []string{`{"inbounds":[{"tag":"test","port":21001,"password":"private-test"}]}`, "proxies:\n  - name: test\n    port: 21001\n    password: private-test\n"} {
		redacted := RedactDiagnosticValue(original).(string)
		edited := strings.ReplaceAll(redacted, "21001", "21002")
		saved, err := PreserveRedactedConfig(original, edited)
		if err != nil || !strings.Contains(saved, "private-test") || !strings.Contains(saved, "21002") || strings.Contains(saved, "[redacted") {
			t.Fatalf("edit = %q, %v", saved, err)
		}
		moved := strings.ReplaceAll(edited, "test", "other")
		if _, err := PreserveRedactedConfig(original, moved); err == nil {
			t.Fatal("secret moved to a different resource")
		}
	}
	if _, err := PreserveRedactedConfig(`{"port":1}`, `{"password":"[redacted]"}`); err == nil {
		t.Fatal("new placeholder accepted")
	}
}

func TestPreserveRedactedCredentialDictionary(t *testing.T) {
	original := `{"listeners":[{"name":"tuic","type":"tuic","port":24443,"users":{"123e4567-e89b-12d3-a456-426614174000":"private-test"}}]}`
	masked := RedactDiagnosticValue(original).(string)
	edited := strings.ReplaceAll(masked, "24443", "24444")
	saved, err := PreserveRedactedConfig(original, edited)
	if err != nil || !strings.Contains(saved, "123e4567-e89b-12d3-a456-426614174000") || !strings.Contains(saved, "private-test") || !strings.Contains(saved, "24444") {
		t.Fatalf("credential dictionary was not preserved: %v", err)
	}
}
