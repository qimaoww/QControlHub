//go:build linux

package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestExistingCoreDiscoveryClearsStateWhenNoCandidateRemains(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	if _, _, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil); err != nil {
		t.Fatal(err)
	}
	fixture.writeStatus(t, "sing-box.service", "inactive")
	specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil {
		t.Fatalf("clear absent discovery: %v", err)
	}
	if len(specs) != 0 || len(issues) != 0 {
		t.Fatalf("absent discovery = specs %+v issues %+v", specs, issues)
	}
	state, err := loadExistingCoreDiscoveryState(fixture.discoveryStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Specs) != 0 || len(state.Issues) != 0 {
		t.Fatalf("stale persisted discovery = %+v", state)
	}
}

func TestExistingCoreDiscoveryKeepsMappingForInterruptedMigrationRecovery(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	specs, _, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := specs[core.EngineSingBox]
	if err := writeCoreMigrationMarker(
		fixture.markerPrefix, core.EngineSingBox, coreMigrationInProgress,
		coreMigrationConfigDigest(`{"inbounds":[]}`), coreMigrationSourceDigest(spec), "enabled", "disabled",
	); err != nil {
		t.Fatal(err)
	}
	fixture.writeStatus(t, "sing-box.service", "inactive")
	specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil {
		t.Fatalf("retain interrupted discovery: %v", err)
	}
	if specs[core.EngineSingBox] != spec || len(issues) != 0 {
		t.Fatalf("interrupted discovery = specs %+v issues %+v", specs, issues)
	}
}

func TestExistingCoreDiscoveryKeepsCompletedMappingForRestartSafetyGate(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	specs, _, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := specs[core.EngineSingBox]
	if err := writeCoreMigrationMarker(
		fixture.markerPrefix, core.EngineSingBox, coreMigrationComplete,
		coreMigrationConfigDigest(`{"inbounds":[]}`), coreMigrationSourceDigest(spec), "enabled", "disabled",
	); err != nil {
		t.Fatal(err)
	}
	fixture.writeStatus(t, "sing-box.service", "active")
	fixture.writeStatus(t, "qagent-sing-box.service", "inactive")
	fixture.writeEnableState(t, "sing-box.service", "disabled")
	fixture.writeEnableState(t, "qagent-sing-box.service", "enabled")
	specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
	if err != nil {
		t.Fatalf("retain completed discovery for restart gate: %v", err)
	}
	if specs[core.EngineSingBox] != spec || len(issues) != 0 {
		t.Fatalf("completed discovery = specs %+v issues %+v", specs, issues)
	}
	executor := &Executor{
		Specs: fixture.managedSpecs, ExistingSpecs: specs, ExistingDiscoveryIssues: issues,
		MigrationMarkerPrefix: fixture.markerPrefix,
	}
	if err := executor.LoadCoreMigrationState(); err != nil {
		t.Fatalf("load completed automatic discovery: %v", err)
	}
	if _, pending := executor.ExistingSpecs[core.EngineSingBox]; !pending {
		t.Fatal("unsafe automatic discovery mapping was suppressed")
	}
	if issue := executor.ExistingDiscoveryIssues[core.EngineSingBox]; !strings.Contains(issue, "迁移状态不再安全") {
		t.Fatalf("automatic restart safety issue = %q", issue)
	}
	if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionStart, Engine: core.EngineSingBox}); err == nil || !strings.Contains(err.Error(), "core tasks are disabled") {
		t.Fatalf("automatic restart start-task error = %v", err)
	}
}

func TestExistingCoreDiscoveryManualMappingWinsAndStatePermissionsFailClosed(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	manual := EngineSpec{Binary: "/manual/sing-box", ConfigPath: "/manual/config.json", Service: "sing-box.service"}
	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, map[core.Engine]EngineSpec{core.EngineSingBox: manual},
	)
	if err != nil {
		t.Fatal(err)
	}
	if specs[core.EngineSingBox] != manual || len(issues) != 0 {
		t.Fatalf("manual precedence = specs %+v issues %+v", specs, issues)
	}
	state, err := loadExistingCoreDiscoveryState(fixture.discoveryStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Specs) != 0 || len(state.Issues) != 0 {
		t.Fatalf("manual mapping was persisted as automatic state: %+v", state)
	}
	if err := os.Chmod(fixture.discoveryStatePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil); err == nil || !strings.Contains(err.Error(), "protected regular file") {
		t.Fatalf("unsafe persisted discovery permissions error = %v", err)
	}
}
