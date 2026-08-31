package serverconfig

import (
	"net/url"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

func mihomoPresetPlan(t *testing.T, key string) Input {
	t.Helper()
	protocol, ok := FindProtocol(core.EngineMihomo, key)
	if !ok {
		t.Fatalf("Mihomo protocol %q was not published", key)
	}
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func generatedMihomoClient(t *testing.T, input Input) (string, string) {
	t.Helper()
	metadata, err := MarshalClientMetadata(input)
	if err != nil {
		t.Fatal(err)
	}
	content, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := Parse(core.EngineMihomo, content)
	if !ok {
		t.Fatalf("generated configuration did not parse:\n%s", content)
	}
	if err := ApplyClientMetadata(&parsed, metadata); err != nil {
		t.Fatal(err)
	}
	profile, err := BuildClientProfileNamed(parsed, "edge.example.com", "", "EDGE")
	if err != nil {
		t.Fatal(err)
	}
	if isSnellProtocol(input.Protocol) || input.Protocol == ProtocolSudoku {
		parsed, err := url.Parse(profile.URI)
		wantScheme := "snell"
		if input.Protocol == ProtocolSudoku {
			wantScheme = "sudoku"
		}
		if err != nil || parsed.Scheme != wantScheme || parsed.Hostname() != "edge.example.com" {
			t.Fatalf("client %s URI did not parse: %v\n%s", wantScheme, err, profile.URI)
		}
		return content, profile.URI
	}
	var proxy map[string]any
	if err := yaml.Unmarshal([]byte(profile.URI), &proxy); err != nil {
		t.Fatalf("client YAML did not parse: %v\n%s", err, profile.URI)
	}
	return content, profile.URI
}

func TestSnellCompletePresetMatrix(t *testing.T) {
	for _, test := range []struct {
		name, protocol string
		shadow         bool
	}{
		{name: "plain-snell", protocol: ProtocolSnell},
		{name: "snell-shadow-tls-v3", protocol: ProtocolSnellShadowTLS, shadow: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := mihomoPresetPlan(t, test.protocol)
			input.SnellReuse = true
			server, client := generatedMihomoClient(t, input)
			parsedClient, err := url.Parse(client)
			if err != nil {
				t.Fatalf("client profile did not parse: %v\n%s", err, client)
			}
			if test.shadow {
				if !strings.Contains(server, "shadow-tls:") || !strings.Contains(server, "version: 3") || !strings.Contains(server, "strict-mode: true") || parsedClient.Query().Get("obfs") != "shadow-tls" {
					t.Fatalf("ShadowTLS v3 pair is incomplete:\nserver:\n%s\nclient:\n%s", server, client)
				}
			} else if strings.Contains(server, "shadow-tls:") || parsedClient.Query().Get("obfs") != "" {
				t.Fatalf("plain Snell preset gained an extra carrier:\nserver:\n%s\nclient:\n%s", server, client)
			}
			if strings.Contains(server, "client-fingerprint") || strings.Contains(server, "reuse:") {
				t.Fatalf("server leaked client-only Snell settings:\n%s", server)
			}
			if parsedClient.Query().Get("reuse") != "true" {
				t.Fatalf("client-only reuse was not restored:\n%s", client)
			}
			if parsedClient.Query().Get("tfo") != "true" {
				t.Fatalf("Snell v5 client did not enable TCP Fast Open:\n%s", client)
			}
		})
	}
}

func TestSnellPresetRejectsIncompatibleAndUnsafeSettings(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Input)
		want string
	}{
		{name: "v2 udp", edit: func(input *Input) { input.SnellVersion, input.SnellUDP = 2, true }, want: "固定使用 v5"},
		{name: "v3 reuse", edit: func(input *Input) { input.SnellVersion, input.SnellReuse = 3, true }, want: "固定使用 v5"},
		{name: "plain preset rejects carrier", edit: func(input *Input) { input.SnellObfsMode = "restls" }, want: "ShadowTLS v3"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := mihomoPresetPlan(t, ProtocolSnell)
			test.edit(&input)
			if _, err := Generate(core.EngineMihomo, input); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Generate() error = %v, want %q", err, test.want)
			}
		})
	}
	shadow := mihomoPresetPlan(t, ProtocolSnellShadowTLS)
	shadow.SnellObfsHost = ""
	if _, err := Generate(core.EngineMihomo, shadow); err == nil || !strings.Contains(err.Error(), "不能为空") {
		t.Fatalf("missing ShadowTLS host error = %v", err)
	}
	shadow = mihomoPresetPlan(t, ProtocolSnellShadowTLS)
	shadow.SnellShadowTLSPassword = "short"
	if _, err := Generate(core.EngineMihomo, shadow); err == nil || !strings.Contains(err.Error(), "至少") {
		t.Fatalf("short ShadowTLS password error = %v", err)
	}
	shadow = mihomoPresetPlan(t, ProtocolSnellShadowTLS)
	shadow.SnellShadowTLSALPN = "h3"
	if _, err := Generate(core.EngineMihomo, shadow); err == nil || !strings.Contains(err.Error(), "ALPN") {
		t.Fatalf("invalid ShadowTLS ALPN error = %v", err)
	}
}

func TestSudokuCompletePresetMatrixAndPrivateKeyIsolation(t *testing.T) {
	for _, mode := range []string{"stream", "poll", "auto", "ws"} {
		t.Run("httpmask-"+mode, func(t *testing.T) {
			input := mihomoPresetPlan(t, ProtocolSudoku)
			input.SudokuHTTPMaskMode = mode
			if mode != "legacy" {
				input.SudokuHTTPMaskTLS = true
				input.SudokuHTTPMaskHost = "cdn.example.com:443"
			}
			server, client := generatedMihomoClient(t, input)
			parsedClient, err := url.Parse(client)
			if err != nil || parsedClient.Query().Get("httpmask-mode") != mode {
				t.Fatalf("HTTPMask %s was not preserved:\nserver:\n%s\nclient:\n%s", mode, server, client)
			}
			if !strings.Contains(server, "mode: auto") {
				t.Fatalf("Sudoku server HTTPMask mode was not normalized:\n%s", server)
			}
			if mode != "legacy" && (parsedClient.Query().Get("httpmask-tls") != "true" || parsedClient.Query().Get("httpmask-host") != "cdn.example.com:443") {
				t.Fatalf("modern HTTPMask client values were not restored:\n%s", client)
			}
		})
	}

	input := mihomoPresetPlan(t, ProtocolSudoku)
	protocol, _ := FindProtocol(core.EngineMihomo, ProtocolSudoku)
	var err error
	input, err = RegeneratePlan(protocol, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Method = "aes-128-gcm"
	input.SudokuTableType = "prefer_entropy"
	input.SudokuHTTPMaskMode = "stream"
	input.SudokuHTTPMaskTLS = true
	input.SudokuHTTPMaskHost = "cdn.example.com"
	input.SudokuHTTPMaskPathRoot = "/qch/"
	input.SudokuMultiplex = "on"
	server, client := generatedMihomoClient(t, input)
	if strings.Contains(server, input.SudokuClientKey) {
		t.Fatalf("Sudoku client private key leaked to server:\n%s", server)
	}
	for _, marker := range []string{input.Credential, "mode: auto"} {
		if !strings.Contains(server, marker) {
			t.Fatalf("Sudoku server omitted %q:\n%s", marker, server)
		}
	}
	parsedClient, err := url.Parse(client)
	if err != nil || parsedClient.Scheme != "sudoku" {
		t.Fatalf("Sudoku client URI did not parse: %v\n%s", err, client)
	}
	for key, want := range map[string]string{
		"key": input.SudokuClientKey, "aead-method": input.Method, "padding-min": "5", "padding-max": "15",
		"table-type": input.SudokuTableType, "multiplex": "on", "httpmask-mode": "stream", "httpmask-tls": "true",
		"httpmask-host": "cdn.example.com", "httpmask-path-root": "/qch/",
	} {
		if got := parsedClient.Query().Get(key); got != want {
			t.Errorf("Sudoku client query[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestSudokuPresetRejectsUnsafeOrMismatchedSettings(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Input)
		want string
	}{
		{name: "none is forbidden", edit: func(input *Input) { input.Method = "none" }, want: "AEAD"},
		{name: "padding range", edit: func(input *Input) { input.SudokuPaddingMin, input.SudokuPaddingMax = 20, 10 }, want: "最大值"},
		{name: "nested path", edit: func(input *Input) { input.SudokuHTTPMaskPathRoot = "one/two" }, want: "一级路径"},
		{name: "legacy HTTPMask is not live-safe", edit: func(input *Input) { input.SudokuHTTPMaskMode = "legacy" }, want: "真实流量"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := mihomoPresetPlan(t, ProtocolSudoku)
			test.edit(&input)
			if _, err := Generate(core.EngineMihomo, input); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Generate() error = %v, want %q", err, test.want)
			}
		})
	}

	input := mihomoPresetPlan(t, ProtocolSudoku)
	protocol, _ := FindProtocol(core.EngineMihomo, ProtocolSudoku)
	generated, err := RegeneratePlan(protocol, input)
	if err != nil {
		t.Fatal(err)
	}
	otherPrivate, _, err := newSudokuKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	generated.SudokuClientKey = otherPrivate
	if _, err := Generate(core.EngineMihomo, generated); err == nil || !strings.Contains(err.Error(), "不属于同一密钥对") {
		t.Fatalf("mismatched Sudoku key pair error = %v", err)
	}
}

func TestApplyClientMetadataIgnoresStaleTagFromAnotherProtocol(t *testing.T) {
	input := mihomoPresetPlan(t, ProtocolSudoku)
	metadata, err := MarshalClientMetadata(input)
	if err != nil {
		t.Fatal(err)
	}
	current := Input{Protocol: ProtocolVLESS, Tag: input.Tag}
	if err := ApplyClientMetadata(&current, metadata); err != nil {
		t.Fatalf("stale metadata made a reused tag unavailable: %v", err)
	}
	if current.SudokuClientKey != "" {
		t.Fatalf("stale Sudoku private key was applied to another protocol: %+v", current)
	}
}
