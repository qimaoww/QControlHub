package serverconfig

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSSRustReadableAccountingSource(t *testing.T) {
	for _, source := range []string{
		`{"server":"::","server_port":21001,"method":"aes-256-gcm","password":"fixture\\\"password","fast_open":true,"ipv6_first":false,"timeout":300,"mode":"tcp_and_udp","unknown":{"n":9007199254740993,"decimal":1.234567890123456789,"array":[false,true,"\\path"]}}`,
		`{"servers":[{"id":"one","server":"::1","server_port":21001,"password":"fixture-one","method":"aes-256-gcm"},{"id":"two","server":"127.0.0.1","server_port":21002,"password":"fixture-two","method":"aes-256-gcm"}],"acl":"/very/long/path/with/no/spaces/allow.acl","fast_open":false,"ipv6_first":true,"unknown":[{"n":9007199254740993},null]}`,
		`{"servers":[],"timeout":300}`,
	} {
		original := decodeFormatFixture(t, source)
		for round := 0; round < 3; round++ {
			plan, err := PlanPresetAccounting(core.EngineShadowsocksRust, source)
			if err != nil {
				t.Fatal(err)
			}
			editable, err := PresetAccountingSource(core.EngineShadowsocksRust, plan.Content)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(editable, "\n  \"") || !strings.HasSuffix(editable, "\n") {
				t.Fatal("source is not indented with a final newline")
			}
			if decodeFormatFixture(t, editable) != original {
				t.Fatalf("configuration semantics changed: %s", editable)
			}
			again, err := PresetAccountingSource(core.EngineShadowsocksRust, editable)
			if err != nil || again != editable {
				t.Fatal("formatting is not idempotent", err)
			}
			if round > 0 && source != editable {
				t.Fatal("save/read format oscillates")
			}
			if plan.Source == "disabled" {
				source = editable
				continue
			}
			updated, err := AccountingUpdateSource(core.EngineShadowsocksRust, plan.Content, plan.Content)
			if err != nil || decodeFormatFixture(t, updated) != original || !strings.Contains(updated, "\n  \"") {
				t.Fatal("accounting update lost content or format", err)
			}
			source = editable
		}
	}
}

func decodeFormatFixture(t *testing.T, content string) string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}

func TestFormatSSRustSourcePreservesTokens(t *testing.T) {
	const source = `{"unknown":9007199254740993,"decimal":1.2300e+90,"escaped":"\u4e2d\/\\\"","list":[3,2,1]}`
	formatted, err := FormatSSRustSource(source)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(formatted)); err != nil || compact.String() != source {
		t.Fatal("formatting changed JSON tokens", err)
	}
	for _, invalid := range []string{"", "{", `{"a":1} trailing`, `{"a":"unterminated}`} {
		if _, err := FormatSSRustSource(invalid); err == nil {
			t.Fatalf("accepted invalid JSON %q", invalid)
		}
	}
}
