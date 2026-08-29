package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"gopkg.in/yaml.v3"
)

type liveMihomoPresetCase struct {
	name  string
	input serverconfig.Input
}

func liveMihomoPresetCases(t *testing.T) []liveMihomoPresetCase {
	t.Helper()
	newPlan := func(key string) serverconfig.Input {
		protocol, ok := serverconfig.FindProtocol(core.EngineMihomo, key)
		if !ok {
			t.Fatalf("Mihomo protocol %q was not published", key)
		}
		input, err := serverconfig.NewPlan(protocol)
		if err != nil {
			t.Fatal(err)
		}
		return input
	}
	cases := []liveMihomoPresetCase{{name: "snell-v5", input: newPlan(serverconfig.ProtocolSnell)}}
	cases = append(cases, liveMihomoPresetCase{name: "snell-shadow-tls-v3", input: newPlan(serverconfig.ProtocolSnellShadowTLS)})
	for _, mode := range []string{"stream", "poll", "auto", "ws"} {
		input := newPlan(serverconfig.ProtocolSudoku)
		input.SudokuHTTPMaskMode = mode
		input.SudokuHTTPMaskTLS = true
		input.SudokuHTTPMaskHost = "cdn.example.com:443"
		cases = append(cases, liveMihomoPresetCase{name: "sudoku-httpmask-" + mode, input: input})
	}
	cases = append(cases, liveMihomoPresetCase{name: "sudoku-ed25519", input: newPlan(serverconfig.ProtocolSudoku)})
	multiplex := newPlan(serverconfig.ProtocolSudoku)
	multiplex.SudokuMultiplex = "on"
	cases = append(cases, liveMihomoPresetCase{name: "sudoku-native-multiplex", input: multiplex})
	return cases
}

func liveMihomoExecutor(t *testing.T, configName string) *Executor {
	t.Helper()
	root := os.Getenv("QCH_LIVE_CORE_ROOT")
	if root == "" {
		t.Fatal("QCH_LIVE_CORE_ROOT is required")
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{
		core.EngineMihomo: {
			Binary: filepath.Join(root, "bin", "mihomo"), ConfigPath: filepath.Join(root, "configs", "mihomo", configName), Service: "unused-mihomo.service",
		},
	}}
	if err := executor.Validate(); err != nil {
		t.Fatal(err)
	}
	return executor
}

func TestCompleteMihomoPresetMatrixWithLiveCore(t *testing.T) {
	if os.Getenv("QCH_LIVE_CORE_VALIDATION_TEST") != "1" {
		t.Skip("QCH_LIVE_CORE_VALIDATION_TEST is not enabled")
	}
	executor := liveMihomoExecutor(t, "preset-server.yaml")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, test := range liveMihomoPresetCases(t) {
		t.Run(test.name, func(t *testing.T) {
			content, err := serverconfig.Generate(core.EngineMihomo, test.input)
			if err != nil {
				t.Fatal(err)
			}
			output, err := executor.Execute(ctx, core.Task{Action: core.ActionValidate, Engine: core.EngineMihomo, ConfigContent: content})
			if err != nil {
				t.Fatalf("official Mihomo rejected server preset: %v\n%s\n%s", err, output, content)
			}
		})
	}
}

func TestCompleteMihomoPresetClientMatrixWithLiveCore(t *testing.T) {
	if os.Getenv("QCH_LIVE_CORE_VALIDATION_TEST") != "1" {
		t.Skip("QCH_LIVE_CORE_VALIDATION_TEST is not enabled")
	}
	executor := liveMihomoExecutor(t, "preset-client.yaml")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, test := range liveMihomoPresetCases(t) {
		t.Run(test.name, func(t *testing.T) {
			profile, err := serverconfig.BuildClientProfileNamed(test.input, "127.0.0.1", "", "LIVE")
			if err != nil {
				t.Fatal(err)
			}
			var proxy map[string]any
			if err := yaml.Unmarshal([]byte(profile.URI), &proxy); err != nil {
				t.Fatal(err)
			}
			content, err := yaml.Marshal(map[string]any{
				"log-level": "info", "proxies": []any{proxy},
				"proxy-groups": []any{map[string]any{"name": "TEST", "type": "select", "proxies": []string{"LIVE"}}},
				"rules":        []string{"MATCH,TEST"},
			})
			if err != nil {
				t.Fatal(err)
			}
			output, err := executor.Execute(ctx, core.Task{Action: core.ActionValidate, Engine: core.EngineMihomo, ConfigContent: string(content)})
			if err != nil {
				t.Fatalf("official Mihomo rejected client preset: %v\n%s\n%s", err, output, content)
			}
			if test.input.Protocol == serverconfig.ProtocolSudoku && strings.Contains(string(content), test.input.Credential) {
				t.Fatalf("client YAML contains Sudoku server public key instead of only the private key:\n%s", content)
			}
		})
	}
}
