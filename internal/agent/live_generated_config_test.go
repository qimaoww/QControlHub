package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
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
	executor := &Executor{DryRun: false, Specs: map[core.Engine]EngineSpec{
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
		}
	}
}
