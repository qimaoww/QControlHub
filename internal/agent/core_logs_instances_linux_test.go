//go:build linux

package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedCoreLogSourcesCoverEveryEngine(t *testing.T) {
	for _, engine := range core.AllEngines() {
		for _, manager := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
			t.Run(string(engine)+"/"+manager, func(t *testing.T) {
				for _, share := range []string{"shr_0123456789abcdef", "shr_1123456789abcdef"} {
					spec, err := sharedInstanceSpec(engine, DefaultSpecsForServiceManager(manager)[engine], share, manager)
					if err != nil {
						t.Fatal(err)
					}
					specs := map[core.Engine]EngineSpec{engine: spec}
					if manager == ServiceManagerSystemd {
						sources := coreLogJournalSources(specs)
						if len(sources) != 2 {
							t.Fatalf("missing namespaced/fallback journals %+v", sources)
						}
						for _, source := range sources {
							if !containsArgument(source.arguments, "--unit="+spec.Service) {
								t.Fatal("template instance omitted")
							}
							entry, _, ok := decodeJournalCoreLog([]byte(fmt.Sprintf(`{"_SYSTEMD_UNIT":%q,"MESSAGE":"instance log","__CURSOR":"same-cursor"}`, spec.Service)), source.unitEngines)
							if !ok || entry.InstanceID != share || entry.Engine != engine || entry.Service != spec.Service {
								t.Fatalf("wrong attribution %+v", entry)
							}
						}
						if len(systemdFallbackCoreLogFileSources(specs)) != 1 {
							t.Fatal("missing managed fallback")
						}
					} else {
						sources := coreLogFileSources(specs)
						if len(sources) != 1 || !strings.HasSuffix(sources[0].path, spec.Service+".log") {
							t.Fatal("OpenRC instance log omitted")
						}
					}
				}
			})
		}
	}
}

func TestSharedCoreLogQueueAndHealthIsolation(t *testing.T) {
	root := NewCoreLogCollector()
	root.executor = &Executor{}
	a := NewCoreLogCollector()
	b := NewCoreLogCollector()
	a.parent = root
	a.instanceID = "shr_0123456789abcdef"
	a.instanceService = core.CoreLogService(core.EngineXray, a.instanceID, "systemd")
	b.parent = root
	b.instanceID = "shr_1123456789abcdef"
	b.instanceService = core.CoreLogService(core.EngineXray, b.instanceID, "systemd")
	root.instances = map[string]*coreLogInstanceRun{"a": {collector: a, engine: core.EngineXray, shareID: a.instanceID}, "b": {collector: b, engine: core.EngineXray, shareID: b.instanceID}}
	root.setSourceStatus(core.EngineXray, "journal", "active", "")
	a.setSourceStatus(core.EngineXray, "journal", "failed", "permission-denied")
	b.setSourceStatus(core.EngineXray, "journal", "active", "")
	states := root.InstanceStatuses()
	if states["a"].CoreLogStatus != "failed" || states["b"].CoreLogStatus != "active" || root.Status()[core.EngineXray].Status != "active" {
		t.Fatal("instance health crossed scopes")
	}
	b.appendJournal(core.CoreLogEntry{Engine: core.EngineXray, Message: "quiet recipient"}, "cursor")
	for i := range 3000 {
		a.appendJournal(core.CoreLogEntry{Engine: core.EngineXray, Message: fmt.Sprint(i)}, fmt.Sprint(i))
	}
	found := false
	count := 0
	for batch := root.NextBatch(); batch != nil; batch = root.NextBatch() {
		if repeated := root.NextBatch(); repeated.ID != batch.ID {
			t.Fatal("reconnect lost pending batch")
		}
		for _, entry := range batch.Entries {
			count++
			if entry.InstanceID == b.instanceID {
				found = true
			}
		}
		root.Acknowledge(batch.ID)
	}
	if !found || count > 65 {
		t.Fatalf("noisy instance displaced quiet recipient: found=%t count=%d", found, count)
	}
}
