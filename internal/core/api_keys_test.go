package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAPIKeyIPNormalizationAndMatching(t *testing.T) {
	values, err := NormalizeIPAllowlist([]string{" 192.168.1.9/24 ", "192.168.1.0/24", "2001:0db8::1/128", "2001:db8::1", "::ffff:10.0.0.1", "::ffff:10.0.0.99/120", "127.0.0.1", "::1"})
	if err != nil {
		t.Fatal(err)
	}
	want := `["192.168.1.0/24","2001:db8::1","10.0.0.1","10.0.0.0/24","127.0.0.1","::1"]`
	got, _ := json.Marshal(values)
	if string(got) != want {
		t.Fatalf("normalized %s", got)
	}
	for _, source := range []string{"192.168.1.40", "::ffff:192.168.1.40", "2001:db8::1", "10.0.0.9", "::ffff:10.0.0.1", "127.0.0.1", "::1"} {
		if !IPAllowed(values, source) {
			t.Errorf("denied %s", source)
		}
	}
	for _, source := range []string{"192.168.2.1", "2001:db8::2", "invalid", "", "127.0.0.1:42", "fe80::1%eth0"} {
		if IPAllowed(values, source) {
			t.Errorf("allowed %s", source)
		}
	}
	if !IPAllowed(nil, "") {
		t.Fatal("empty allowlist restricts source")
	}
	for _, rule := range []string{"", "bad", "example.com", "10.0.0.1:80", "10.0.0.1/33", "::/129", "::ffff:10.0.0.0/80", "fe80::1%eth0", "2001:db8::%eth0/64"} {
		if _, err := NormalizeIPAllowlist([]string{rule}); err == nil {
			t.Errorf("accepted %s", rule)
		}
	}
	for _, rules := range [][]string{{"0.0.0.0/0"}, {"::/0"}} {
		n, err := NormalizeIPAllowlist(rules)
		if err != nil || !IPAllowed(n, map[bool]string{true: "192.168.1.1", false: "fd00::1"}[rules[0] == "0.0.0.0/0"]) {
			t.Fatal("zero prefix broken")
		}
	}
}
func TestDiagnosticRedactionPreservesStructure(t *testing.T) {
	value := map[string]any{"id": "cfg_001", "version": json.Number("7"), "agent_id": "agt_123", "error": "parse config port 443 password=INLINE token=TOKEN; Bearer abc.def.xyz; https://user:URLPASS@example.test?token=QUERY",
		"content":  `{"inbounds":[{"tag":"entry","port":443,"settings":{"clients":[{"id":"11111111-2222-3333-4444-555555555555","password":"CONFIGPASS"}]},"tls":{"private_key":"PRIVATE"}}],"route":{"final":"direct"}}`,
		"fragment": "proxies:\n  - name: upstream\n    password: YAMLPASS\n    private-key: |\n      YAMLPRIVATE\n    server: example.test\nsecret: YAMLSECRET\n",
		"pem":      "private_key: -----BEGIN PRIVATE KEY-----\nPEMBODY\n-----END PRIVATE KEY-----",
		"key":      "qch_key_" + strings.Repeat("a", 43)}
	b, err := json.Marshal(RedactDiagnosticValue(value))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"INLINE", "TOKEN", "URLPASS", "QUERY", "CONFIGPASS", "PRIVATE", "YAMLPASS", "YAMLPRIVATE", "YAMLSECRET", "PEMBODY", "abc.def.xyz", strings.Repeat("a", 43), "11111111-2222-3333-4444-555555555555"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("secret %s leaked: %s", secret, b)
		}
	}
	for _, expected := range []string{"cfg_001", "agt_123", "443", "entry", "direct", "upstream", "example.test", "version"} {
		if !strings.Contains(string(b), expected) {
			t.Errorf("lost %s: %s", expected, b)
		}
	}
}

func TestDiagnosticRedactionNativeCredentialsAndFieldNames(t *testing.T) {
	for _, content := range []any{
		map[string]any{"key": "dns", "type": "sudoku", "psk": "SNELL_VALUE", "users": map[string]any{"alice": "USER_VALUE", "11111111-2222-3333-4444-555555555555": "TUIC_VALUE"}, "decryption": "DECRYPTION_VALUE", "pre_shared_key": "WIREGUARD_VALUE", "headers": map[string]any{"Cookie": "session=COOKIE_VALUE"}, "authentication-timeout": 1000},
		"key: dns\nlisteners:\n  - type: sudoku\n    key: SUDOKU_VALUE\n  - type: snell\n    psk: &ANCHOR_CREDENTIAL SNELL_VALUE\n  - type: hysteria2\n    users:\n      alice: USER_VALUE\n  - type: tuic\n    users:\n      11111111-2222-3333-4444-555555555555: TUIC_VALUE\n    authentication-timeout: 1000\n",
		`{"key":"dns","listeners":[{"type":"sudoku","key":"SUDOKU_VALUE"},{"type":"snell","psk":"SNELL_VALUE"},{"type":"tuic","users":{"11111111-2222-3333-4444-555555555555":"TUIC_VALUE"},"authentication-timeout":1000}]}`,
	} {
		b, err := json.Marshal(RedactDiagnosticValue(content))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"SUDOKU_VALUE", "SNELL_VALUE", "USER_VALUE", "TUIC_VALUE", "DECRYPTION_VALUE", "WIREGUARD_VALUE", "COOKIE_VALUE", "ANCHOR_CREDENTIAL", "11111111-2222-3333-4444-555555555555"} {
			if strings.Contains(string(b), secret) {
				t.Errorf("native credential leaked: %s", secret)
			}
		}
		if !strings.Contains(string(b), "1000") {
			t.Error("authentication timeout was removed")
		}
	}
	field := RedactDiagnosticValue(map[string]any{"key": "dns", "fragment": "nameserver: [1.1.1.1]"}).(map[string]any)
	if field["key"] != "dns" {
		t.Error("field identifier was redacted")
	}
}

func TestDiagnosticRedactionHidesWebhookURL(t *testing.T) {
	const secret = "https://hooks.slack.com/services/T000/B000/WEBHOOKSECRET"
	for _, value := range []any{
		map[string]any{"webhook_url": secret},
		"webhook_url: " + secret,
		"webhook_url=" + secret,
	} {
		encoded, err := json.Marshal(RedactDiagnosticValue(value))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "WEBHOOKSECRET") {
			t.Fatalf("webhook credential leaked: %s", encoded)
		}
	}
}
