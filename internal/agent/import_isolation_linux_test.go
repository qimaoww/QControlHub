//go:build linux

package agent

import (
	"context"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"os"
	"strings"
	"testing"
)

func TestExternalSourceFailureDoesNotChangeManagedReadValidationOrRuntime(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	if err := os.WriteFile(fixture.managed.Binary, existingDiscoveryCoreHelper, 0700); err != nil {
		t.Fatal(err)
	}
	issue := "外部服务配置不可导入：protected path validation failed"
	fixture.executor.ExistingDiscoveryIssues = map[core.Engine]string{core.EngineXray: issue}
	before, _ := os.ReadFile(fixture.existing.ConfigPath)
	for _, action := range []core.Action{core.ActionReadManagedConfig, core.ActionValidate, core.ActionStatus} {
		task := core.Task{Action: action, Engine: core.EngineXray, ConfigContent: `{"inbounds":[{"tag":"manual","listen":"127.0.0.1","port":21000,"protocol":"socks","settings":{"auth":"noauth"}}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`}
		if _, err := fixture.executor.Execute(context.Background(), task); err != nil {
			t.Fatalf("ordinary %s depends on import discovery: %v", action, err)
		}
	}
	state := fixture.executor.Runtime(context.Background())[core.EngineXray]
	if !state.Installed || state.ServiceStatus != "inactive" || state.ExistingConfigUnsupportedReason != issue || state.ManagedOperationBlockedReason != "" {
		t.Fatalf("external service contaminated managed runtime: %+v", state)
	}
	for _, action := range []core.Action{core.ActionReadConfig, core.ActionImportExisting} {
		if _, err := fixture.executor.Execute(context.Background(), core.Task{Action: action, Engine: core.EngineXray}); err == nil || !strings.Contains(err.Error(), issue) {
			t.Fatalf("source safety was bypassed for %s: %v", action, err)
		}
	}
	after, _ := os.ReadFile(fixture.existing.ConfigPath)
	if string(before) != string(after) {
		t.Fatal("ordinary operations modified external source")
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled")
}

func TestUnreadableMigrationStateBlocksOnlyItsEngine(t *testing.T) {
	fixture := newExistingCoreMigrationFixture(t, false)
	fixture.executor.Specs[core.EngineMihomo] = EngineSpec{Binary: fixture.managed.Binary, ConfigPath: fixture.managed.ConfigPath, Service: "qagent-mihomo.service"}
	if err := os.WriteFile(coreMigrationMarkerPath(fixture.markerPrefix, core.EngineXray), []byte("invalid migration state"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := fixture.executor.LoadCoreMigrationState(); err == nil {
		t.Fatal("corrupt migration state was ignored")
	}
	if fixture.executor.ManagedSafetyIssues[core.EngineXray] == "" || fixture.executor.ManagedSafetyIssues[core.EngineMihomo] != "" {
		t.Fatal("migration safety scope was lost")
	}
}
