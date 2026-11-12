package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/networkdiag"
)

func TestNetworkDiagnosticsPreservesExistingTaskSpool(t *testing.T) {
	c := &Client{config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "agent.json")}}
	const id = "tsk_0123456789abcdef"
	result := core.TaskResultRequest{Output: "existing deployment diagnostic"}
	if err := c.persistDiagnostic(id, &result); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.initializeNetworkDiagnostics(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.persistDiagnostic(id, &result); err != nil {
		t.Fatalf("network diagnostics blocked existing task spool: %v", err)
	}
	b, err := os.ReadFile(c.config.StatePath + ".diagnostics/" + id + ".json")
	if err != nil || len(b) == 0 {
		t.Fatalf("existing task spool lost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.config.StatePath+".network-diagnostics", "journal.json")); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledCapabilityRefreshPreservesLastReport(t *testing.T) {
	c := &Client{diagnosticCapabilities: core.DiagnosticCapabilities{Tools: map[string]core.DiagnosticTool{"nexttrace": {Version: "last-verified"}}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.refreshDiagnosticCapabilities(ctx)
	if got := c.diagnosticCapabilities.Tools["nexttrace"]; got.Version != "last-verified" || got.Error != "" {
		t.Fatalf("canceled task poisoned heartbeat: %+v", got)
	}
}

func TestCapabilityRefreshRecoversPriorProbeError(t *testing.T) {
	c := &Client{diagnosticCapabilities: core.DiagnosticCapabilities{Tools: map[string]core.DiagnosticTool{"nexttrace": {Error: "old external CLI failure"}}}}
	c.refreshDiagnosticCapabilities(context.Background())
	want := networkdiag.Capabilities(context.Background()).Tools["nexttrace"]
	if got := c.diagnosticCapabilities.Tools["nexttrace"]; got != want {
		t.Fatalf("old report persisted: got %+v want %+v", got, want)
	}
}
