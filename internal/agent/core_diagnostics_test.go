package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestValidationAndDeployRedactCoreEchoedInlineKeys(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			protocol, _ := serverconfig.FindProtocol(engine, serverconfig.ProtocolVLESS)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			content, err := serverconfig.Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			secret := input.RealityPrivateKey
			root := t.TempDir()
			binary := filepath.Join(root, "core")
			writeExecutable(t, binary, fmt.Sprintf("#!/bin/sh\nprintf 'invalid inline key: %%s\\n' '%s'\nexit 1\n", secret))
			executor := &Executor{Specs: map[core.Engine]EngineSpec{engine: {Binary: binary, ConfigPath: filepath.Join(root, "config"), Service: "fixture.service"}}}
			for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
				output, err := executor.Execute(context.Background(), core.Task{Engine: engine, Action: action, ConfigContent: content})
				if err == nil || strings.Contains(output, secret) || strings.Contains(err.Error(), secret) || !strings.Contains(output, "[redacted]") {
					t.Fatalf("%s exposed a core-echoed private key: output=%s error=%v", action, output, err)
				}
				if !strings.Contains(err.Error(), "configuration fields:") || !strings.Contains(err.Error(), "inbounds[0]") && !strings.Contains(err.Error(), "listeners[0]") {
					t.Fatalf("lost validation stage or configuration field location: %v", err)
				}
			}
		})
	}
}

func TestDiagnosticRedactionPreservesRealTLSPaths(t *testing.T) {
	content := "listeners:\n- type: trojan\n  private-key: /etc/qcontrolhub/tls/key.pem\n- type: vless\n  reality-config: {private-key: secret-reality}\n"
	redact := coreDiagnosticRedactor(core.EngineMihomo, content)
	got := redact("read /etc/qcontrolhub/tls/key.pem failed; invalid Reality secret-reality")
	if !strings.Contains(got, "/etc/qcontrolhub/tls/key.pem") || strings.Contains(got, "secret-reality") {
		t.Fatalf("redaction lost the real file path or leaked inline data: %s", got)
	}
}
