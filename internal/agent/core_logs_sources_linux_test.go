//go:build linux

package agent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMissingSingBoxInstallationDoesNotReportCollectionFailure(t *testing.T) {
	for _, kind := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		t.Run(kind, func(t *testing.T) {
			manager, err := NewServiceManager(kind)
			if err != nil {
				t.Fatal(err)
			}
			service := "qagent-sing-box.service"
			if kind == ServiceManagerOpenRC {
				service = "qagent-sing-box"
			}
			executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: {
				Binary: filepath.Join(t.TempDir(), "missing-sing-box"), ConfigPath: filepath.Join(t.TempDir(), "missing-config.json"),
				Service: service,
			}}, Services: manager}
			collector := NewCoreLogCollectorForExecutor(executor)
			if status, exists := collector.Status()[core.EngineSingBox]; exists {
				t.Fatalf("missing sing-box installation reported source status: %+v", status)
			}
		})
	}
}

func TestCoreLogSourcesMixManagedAndExactGenericUnits(t *testing.T) {
	t.Parallel()
	sources := coreLogJournalSources(
		map[core.Engine]EngineSpec{
			core.EngineMihomo:          {Service: "qagent-mihomo.service"},
			core.EngineXray:            {Service: "qagent-xray.service"},
			core.EngineSingBox:         {Service: "qagent-sing-box.service"},
			core.EngineShadowsocksRust: {Service: "qagent-shadowsocks-rust.service"},
		},
		map[core.Engine]EngineSpec{
			core.EngineXray:            {Service: "xray.service"},
			core.EngineSingBox:         {Service: "sing-box.service"},
			core.EngineShadowsocksRust: {Service: "shadowsocks-rust.service"},
		},
	)
	if len(sources) != 2 {
		t.Fatalf("source count = %d, want 2", len(sources))
	}
	managed, generic := sources[0], sources[1]
	if !containsArgument(managed.arguments, "--namespace=qagent-cores") {
		t.Fatalf("managed journal namespace arguments = %v", managed.arguments)
	}
	for _, unit := range []string{
		"qagent-mihomo.service", "qagent-xray.service", "qagent-sing-box.service", "qagent-shadowsocks-rust.service",
	} {
		if !containsArgument(managed.arguments, "--unit="+unit) {
			t.Fatalf("managed journal arguments omit %s: %v", unit, managed.arguments)
		}
	}
	cursorArguments := journalCursorArguments(managed.arguments)
	if !containsArgument(cursorArguments, "--namespace=qagent-cores") ||
		!containsArgument(cursorArguments, "--show-cursor") ||
		containsArgument(cursorArguments, "--unit=qagent-sing-box.service") {
		t.Fatalf("managed journal tail-position arguments = %v", cursorArguments)
	}
	followArguments := journalFollowArguments(managed.arguments, "--since=@1787583894.445332")
	if !containsArgument(followArguments, "--follow") ||
		!containsArgument(followArguments, "--unit=qagent-sing-box.service") ||
		!containsArgument(followArguments, "--since=@1787583894.445332") ||
		containsArgument(followArguments, "--lines=0") {
		t.Fatalf("managed journal bounded follower arguments = %v", followArguments)
	}
	if containsArgument(generic.arguments, "--namespace=qagent-cores") ||
		!containsArgument(generic.arguments, "--unit=shadowsocks-rust.service") ||
		!containsArgument(generic.arguments, "--unit=xray.service") ||
		!containsArgument(generic.arguments, "--unit=sing-box.service") ||
		!containsArgument(generic.arguments, "--unit=qagent-xray.service") ||
		!containsArgument(generic.arguments, "--unit=qagent-sing-box.service") {
		t.Fatalf("generic journal arguments = %v", generic.arguments)
	}
	if engine, ok := coreLogEngineForUnit("xray.service", generic.unitEngines); !ok || engine != core.EngineXray {
		t.Fatalf("generic Xray mapping = %q, %t", engine, ok)
	}
	if _, ok := coreLogEngineForUnit("ssh.service", generic.unitEngines); ok {
		t.Fatal("unrelated default-namespace unit was accepted")
	}
}

func TestCoreLogSourcesRejectArbitraryCustomUnitsAndDeduplicateCursors(t *testing.T) {
	t.Parallel()
	sources := coreLogJournalSources(map[core.Engine]EngineSpec{
		core.EngineMihomo:          {Service: "qagent-xray.service"},
		core.EngineXray:            {Service: "qagent-xray.service"},
		core.EngineSingBox:         {Service: "singbox.service"},
		core.EngineShadowsocksRust: {Service: "custom-ss.service"},
	})
	if len(sources) != 1 || containsArgument(sources[0].arguments, "--unit=qagent-xray.service") ||
		containsArgument(sources[0].arguments, "--unit=custom-ss.service") {
		t.Fatalf("custom source filtering = %+v", sources)
	}
	collector := NewCoreLogCollector(map[core.Engine]EngineSpec{})
	entry := core.CoreLogEntry{Engine: core.EngineSingBox, Level: "info", Message: "once", LoggedAt: time.Now()}
	collector.appendJournal(entry, "cursor-1")
	collector.appendJournal(entry, "cursor-1")
	batch := collector.NextBatch()
	if batch == nil || len(batch.Entries) != 1 {
		t.Fatalf("deduplicated batch = %+v", batch)
	}
}

func TestCoreLogFileSourcesMapOnlyManagedServices(t *testing.T) {
	sources := coreLogFileSources(map[core.Engine]EngineSpec{
		core.EngineXray:            {Service: "qagent-xray"},
		core.EngineSingBox:         {Service: "qagent-sing-box"},
		core.EngineMihomo:          {Service: "qagent-mihomo"},
		core.EngineShadowsocksRust: {Service: "qagent-shadowsocks-rust"},
	})
	if len(sources) != 4 {
		t.Fatalf("managed file sources = %+v", sources)
	}
	found := map[core.Engine]string{}
	for _, source := range sources {
		found[source.engine] = source.path
	}
	if found[core.EngineXray] != filepath.Join(openRCCoreLogRoot, "qagent-xray.log") ||
		found[core.EngineMihomo] != filepath.Join(openRCCoreLogRoot, "qagent-mihomo.log") ||
		found[core.EngineSingBox] != filepath.Join(openRCCoreLogRoot, "qagent-sing-box.log") ||
		found[core.EngineShadowsocksRust] != filepath.Join(openRCCoreLogRoot, "qagent-shadowsocks-rust.log") {
		t.Fatalf("managed file source paths = %+v", found)
	}
	if unmanaged := coreLogFileSources(map[core.Engine]EngineSpec{
		core.EngineSingBox: {Service: "sing-box"},
	}); len(unmanaged) != 0 {
		t.Fatalf("unmanaged service produced file sources: %+v", unmanaged)
	}
}

func TestCoreLogFileSourcesSkipAmbiguousServices(t *testing.T) {
	sources := coreLogFileSources(map[core.Engine]EngineSpec{
		core.EngineXray:    {Service: "qagent-xray"},
		core.EngineSingBox: {Service: "qagent-xray"},
	})
	if len(sources) != 0 {
		t.Fatalf("ambiguous file sources = %+v", sources)
	}
}
