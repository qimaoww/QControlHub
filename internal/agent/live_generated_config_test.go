package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"gopkg.in/yaml.v3"
)

func TestEveryGeneratedServerConfigurationWithLiveCores(t *testing.T) {
	if os.Getenv("QCH_LIVE_CORE_VALIDATION_TEST") != "1" {
		t.Skip("QCH_LIVE_CORE_VALIDATION_TEST is not enabled")
	}
	root := os.Getenv("QCH_LIVE_CORE_ROOT")
	certificate := os.Getenv("QCH_LIVE_TLS_CERTIFICATE")
	privateKey := os.Getenv("QCH_LIVE_TLS_PRIVATE_KEY")
	if root == "" || certificate == "" || privateKey == "" {
		t.Fatal("QCH_LIVE_CORE_ROOT, QCH_LIVE_TLS_CERTIFICATE, and QCH_LIVE_TLS_PRIVATE_KEY are required")
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{
		core.EngineMihomo: {
			Binary: filepath.Join(root, "bin", "mihomo"), ConfigPath: filepath.Join(root, "configs", "mihomo", "config.yaml"), Service: "unused-mihomo.service",
		},
		core.EngineXray: {
			Binary: filepath.Join(root, "bin", "xray"), ConfigPath: filepath.Join(root, "configs", "xray", "config.json"), Service: "unused-xray.service",
		},
		core.EngineSingBox: {
			Binary: filepath.Join(root, "bin", "sing-box"), ConfigPath: filepath.Join(root, "configs", "sing-box", "config.json"), Service: "unused-sing-box.service",
		},
	}}
	if err := executor.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		for _, protocol := range serverconfig.Protocols(engine) {
			engine, protocol := engine, protocol
			t.Run(string(engine)+"/"+protocol.Key, func(t *testing.T) {
				input, err := serverconfig.NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				if input.TLSEnabled {
					input.CertificatePath = certificate
					input.PrivateKeyPath = privateKey
				}
				content, err := serverconfig.Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				output, err := executor.Execute(ctx, core.Task{Action: core.ActionValidate, Engine: engine, ConfigContent: content})
				if err != nil {
					t.Fatalf("official %s rejected generated %s configuration: %v\n%s\n%s", engine, protocol.Key, err, output, content)
				}
			})
			if protocol.SupportsRealityMLDSA {
				t.Run(string(engine)+"/"+protocol.Key+"-mldsa65", func(t *testing.T) {
					input, err := serverconfig.NewPlan(protocol)
					if err != nil {
						t.Fatal(err)
					}
					input, err = serverconfig.RegeneratePlan(protocol, input)
					if err != nil {
						t.Fatal(err)
					}
					content, err := serverconfig.Generate(engine, input)
					if err != nil {
						t.Fatal(err)
					}
					output, err := executor.Execute(ctx, core.Task{Action: core.ActionValidate, Engine: engine, ConfigContent: content})
					if err != nil {
						t.Fatalf("official %s rejected generated %s ML-DSA-65 configuration: %v\n%s\n%s", engine, protocol.Key, err, output, content)
					}
				})
			}
		}
	}
}

func TestGeneratedMihomoClientYAMLWithLiveCore(t *testing.T) {
	if os.Getenv("QCH_LIVE_CORE_VALIDATION_TEST") != "1" {
		t.Skip("QCH_LIVE_CORE_VALIDATION_TEST is not enabled")
	}
	root := os.Getenv("QCH_LIVE_CORE_ROOT")
	if root == "" {
		t.Fatal("QCH_LIVE_CORE_ROOT is required")
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{
		core.EngineMihomo: {
			Binary: filepath.Join(root, "bin", "mihomo"), ConfigPath: filepath.Join(root, "configs", "mihomo", "client.yaml"), Service: "unused-mihomo.service",
		},
	}}
	if err := executor.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{serverconfig.ProtocolSnell, serverconfig.ProtocolSudoku} {
		key := key
		t.Run(key, func(t *testing.T) {
			protocol, ok := serverconfig.FindProtocol(core.EngineMihomo, key)
			if !ok {
				t.Fatalf("Mihomo protocol %q was not published", key)
			}
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := serverconfig.BuildClientProfileNamed(input, "127.0.0.1", "", "LIVE")
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
			output, err := executor.Execute(context.Background(), core.Task{Action: core.ActionValidate, Engine: core.EngineMihomo, ConfigContent: string(content)})
			if err != nil {
				t.Fatalf("official Mihomo rejected generated %s client YAML: %v\n%s\n%s", key, err, output, content)
			}
		})
	}
}
