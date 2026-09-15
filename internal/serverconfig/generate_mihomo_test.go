package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMihomoSnellAndSudokuPlansGenerateRoundTripAndExportClientProfiles(t *testing.T) {
	t.Parallel()
	for _, key := range []string{ProtocolSnell, ProtocolSnellShadowTLS, ProtocolSudoku} {
		key := key
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			protocol, ok := FindProtocol(core.EngineMihomo, key)
			if !ok {
				t.Fatalf("Mihomo protocol %q was not published", key)
			}
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			content, err := Generate(core.EngineMihomo, input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(core.EngineMihomo, content)
			if !ok || parsed.Protocol != key || parsed.Credential != input.Credential {
				t.Fatalf("Parse(Mihomo/%s) = %+v, %v\n%s", key, parsed, ok, content)
			}
			metadata, err := MarshalClientMetadata(input)
			if err != nil {
				t.Fatal(err)
			}
			if err := ApplyClientMetadata(&parsed, metadata); err != nil {
				t.Fatal(err)
			}
			profile, err := BuildClientProfileNamed(parsed, "edge.example.com", "", "Tokyo")
			if err != nil {
				t.Fatal(err)
			}
			if key == ProtocolSudoku {
				if !profile.SubscriptionCompatible || profile.Format != "Mihomo Sudoku YAML" || !strings.HasPrefix(profile.URI, "{aead-method:") || !strings.Contains(profile.URI, "type: sudoku") {
					t.Fatalf("Sudoku client YAML = %+v", profile)
				}
				return
			}
			if !profile.SubscriptionCompatible || !strings.HasPrefix(profile.URI, "Tokyo = snell, edge.example.com,") {
				t.Fatalf("Snell Surge client config = %+v", profile)
			}
		})
	}
}
