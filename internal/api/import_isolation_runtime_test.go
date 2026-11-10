package api

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentexec "github.com/qimaoww/qcontrolhub/internal/agent"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestImportIsolationRealSystemdNode(t *testing.T) {
	if os.Getenv("QCH_IMPORT_REAL_NODE") != "1" {
		t.Skip("requires disposable systemd node with installed Xray")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires disposable Docker node")
	}
	db, ctx, _, owner, _ := newConfigScopeAPIFixtureWithin(t, 3*time.Minute)
	executor := &agentexec.Executor{Specs: agentexec.DefaultSpecs(), Updater: agentexec.NewCoreUpdater()}
	spec := executor.Specs[core.EngineXray]
	binary, err := os.ReadFile(spec.Binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"/etc/xray/bin", "/var/lib/qch-import-boundary"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("/etc/xray/bin/xray", binary, 0755); err != nil {
		t.Fatal(err)
	}
	source := []byte(`{"inbounds":[{"tag":"external","listen":"127.0.0.1","port":28600,"protocol":"socks","settings":{"auth":"noauth"}}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`)
	if err := os.WriteFile("/etc/xray/config.json", source, 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod("/etc/xray/config.json", 0666); err != nil {
		t.Fatal(err)
	}
	unit := []byte("[Unit]\nDescription=Disposable external Xray\n[Service]\nExecStart=/etc/xray/bin/xray run -config /etc/xray/config.json\nRestart=no\n")
	if err := os.WriteFile("/etc/systemd/system/xray.service", unit, 0644); err != nil {
		t.Fatal(err)
	}
	command := func(arguments ...string) string {
		t.Helper()
		output, err := exec.CommandContext(ctx, "systemctl", arguments...).CombinedOutput()
		if err != nil {
			t.Fatalf("systemctl %v: %v %s", arguments, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	command("daemon-reload")
	command("start", "xray.service")
	t.Cleanup(func() { _ = exec.Command("systemctl", "stop", "xray.service", spec.Service).Run() })
	pid := command("show", "xray.service", "--property=MainPID", "--value")
	if command("is-active", "xray.service") != "active" {
		t.Fatal("external source is not actually running")
	}
	state := filepath.Join("/var/lib/qch-import-boundary", "discovery.json")
	executor.ExistingSpecs, executor.ExistingDiscoveryIssues, err = agentexec.RefreshExistingCoreDiscovery(ctx, state, filepath.Join(filepath.Dir(state), "migration"), executor.Specs, nil)
	if err != nil || executor.ExistingDiscoveryIssues[core.EngineXray] == "" {
		t.Fatalf("unsafe active source was not discovered: %v %+v", err, executor.ExistingDiscoveryIssues)
	}
	runtime := executor.Runtime(ctx)
	if !runtime[core.EngineXray].Installed || runtime[core.EngineXray].ManagedOperationBlockedReason != "" || runtime[core.EngineXray].ManagedHostBlockedReason != "" {
		t.Fatalf("source changed managed runtime: %+v", runtime[core.EngineXray])
	}
	node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "real import boundary", core.EngineXray)
	features := append(node.Features, core.AgentFeatureImportIsolation)
	if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features, Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	base := "/agents/" + node.ID + "/configs/xray/source"
	manual := strings.ReplaceAll(string(source), "28600", "28601")
	var mutation configMutationResult
	owner.call("POST", base, map[string]any{"name": "real manual", "content": manual, "intent": "deploy", "version": 0, "expected_instance_id": ""}, http.StatusOK, &mutation)
	task, err := db.ClaimTask(ctx, node.ID)
	if err != nil || task == nil {
		t.Fatalf("claim: %v", err)
	}
	output, executionErr := executor.Execute(ctx, *task)
	if executionErr != nil {
		t.Fatalf("manual deployment blocked by unrelated unsafe import: %v\n%s", executionErr, output)
	}
	if err := db.CompleteTask(ctx, node.ID, task.ID, core.TaskResultRequest{LeaseID: task.LeaseID, Success: true, Output: output, TrafficSettled: true}); err != nil {
		t.Fatal(err)
	}
	if command("is-active", spec.Service) != "active" || command("is-active", "xray.service") != "active" {
		t.Fatal("coexisting services did not remain active")
	}
	actual, err := os.ReadFile("/etc/xray/config.json")
	if err != nil || !bytes.Equal(actual, source) || pid != command("show", "xray.service", "--property=MainPID", "--value") {
		t.Fatal("manual deployment modified or restarted external source")
	}
	// A genuine socket collision fails and restores the managed service. It
	// never stops or adopts the independently running system service.
	owner.call("POST", base, map[string]any{"name": "real conflict", "content": string(source), "intent": "deploy", "version": mutation.Config.Version, "expected_instance_id": ""}, http.StatusOK, &mutation)
	task, err = db.ClaimTask(ctx, node.ID)
	if err != nil || task == nil {
		t.Fatalf("conflict claim: %v", err)
	}
	output, executionErr = executor.Execute(ctx, *task)
	if executionErr == nil {
		t.Fatal("real external port conflict was silently accepted")
	}
	if err := db.CompleteTask(ctx, node.ID, task.ID, core.TaskResultRequest{LeaseID: task.LeaseID, Error: executionErr.Error(), Output: output, TrafficSettled: true}); err != nil {
		t.Fatal(err)
	}
	if pid != command("show", "xray.service", "--property=MainPID", "--value") || command("is-active", "xray.service") != "active" {
		t.Fatal("port conflict changed external service")
	}
	actual, err = os.ReadFile("/etc/xray/config.json")
	if err != nil || !bytes.Equal(actual, source) {
		t.Fatal("port conflict changed external configuration")
	}
	if command("is-active", spec.Service) != "active" {
		t.Fatal("port conflict did not recover previous managed service")
	}
	t.Logf("real active unsafe source rejected for import; manual managed deployment and coexistence succeeded; port conflict rejected and managed rollback verified; cause=%s", core.DiagnosticText(executionErr.Error()))
}
