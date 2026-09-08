package agent

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func accountingCacheFixtures() map[core.Engine]string {
	return map[core.Engine]string{
		core.EngineXray:            `{"inbounds":[{"tag":"a","port":1080,"protocol":"vless"}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"custom_integer":9007199254740993}`,
		core.EngineSingBox:         `{"inbounds":[{"tag":"a","listen_port":1080,"type":"vless"}],"outbounds":[{"tag":"direct","type":"direct"}]}`,
		core.EngineMihomo:          "listeners:\n  - {name: a, type: http, port: 1080}\nrules:\n  - MATCH,DIRECT\n",
		core.EngineShadowsocksRust: `{"servers":[{"server_port":1080,"password":"test","method":"aes-256-gcm"}]}`,
	}
}

func TestAccountingConfigurationCache(t *testing.T) {
	executor := &Executor{}
	for engine, source := range accountingCacheFixtures() {
		t.Run(string(engine), func(t *testing.T) {
			plan, err := serverconfig.PrepareAccounting(engine, source)
			if err != nil {
				t.Fatal(err)
			}
			first, err := executor.accountingConfiguration(engine, plan.Content)
			if err != nil {
				t.Fatal(err)
			}
			cached, err := executor.accountingConfiguration(engine, strings.Clone(plan.Content))
			if err != nil || !reflect.DeepEqual(first, cached) || &first.Plan.Ports[0] != &cached.Plan.Ports[0] {
				t.Fatalf("unchanged content was not reused: %v", err)
			}
			// Per-request live fields must never leak into the cached template.
			cached.ProcessEpoch = "old-process"
			cached.Counters = map[string]uint64{"old": 123}
			again, err := executor.accountingConfiguration(engine, plan.Content)
			if err != nil || again.ProcessEpoch != "" || again.Counters != nil {
				t.Fatalf("cached live statistics: %+v, %v", again, err)
			}
			changedPlan, err := serverconfig.PrepareAccounting(engine, strings.ReplaceAll(source, "1080", "1081"))
			if err != nil {
				t.Fatal(err)
			}
			changed, err := executor.accountingConfiguration(engine, changedPlan.Content)
			if err != nil || changed.Plan.Ports[0].Port != 1081 || changed.ListenerProtocols[1080] != "" {
				t.Fatalf("changed configuration retained old mapping: %+v, %v", changed, err)
			}
			// Both syntactically invalid and valid-but-unmigrated files must
			// fail closed rather than serving the previous successful plan.
			for _, invalid := range []string{"{", source} {
				bad, firstErr := executor.accountingConfiguration(engine, invalid)
				_, secondErr := executor.accountingConfiguration(engine, invalid)
				if firstErr == nil || secondErr != firstErr || len(bad.Plan.Ports) != 0 {
					t.Fatalf("invalid configuration was not cached fail-closed: %+v, %v, %v", bad, firstErr, secondErr)
				}
			}
			rollback, err := executor.accountingConfiguration(engine, plan.Content)
			if err != nil || !reflect.DeepEqual(first, rollback) {
				t.Fatalf("rollback did not restore metadata: %v", err)
			}
		})
	}
	if len(executor.accountingConfigs) != len(accountingCacheFixtures()) {
		t.Fatalf("cache retained historical revisions: %d entries", len(executor.accountingConfigs))
	}
	if _, err := executor.accountingConfiguration("invalid", "{}"); err == nil {
		t.Fatal("invalid engine accepted")
	}
}

func TestAccountingConfigurationCacheMarkedSingBox(t *testing.T) {
	plan, err := serverconfig.PrepareMarkedSingBoxAccounting(accountingCacheFixtures()[core.EngineSingBox])
	if err != nil {
		t.Fatal(err)
	}
	executor := &Executor{}
	for range 2 {
		got, err := executor.accountingConfiguration(core.EngineSingBox, plan.Content)
		if err != nil || got.Plan.Source != "nft-dual" || got.ListenerProtocols[1080] != core.TrafficProtocolTCP {
			t.Fatalf("marked sing-box cache: %+v, %v", got, err)
		}
	}
}

func TestAccountingConfigurationCacheConcurrent(t *testing.T) {
	plan, err := serverconfig.PrepareAccounting(core.EngineXray, accountingCacheFixtures()[core.EngineXray])
	if err != nil {
		t.Fatal(err)
	}
	executor := &Executor{}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 20 {
				got, err := executor.accountingConfiguration(core.EngineXray, plan.Content)
				if err != nil || got.Plan.API != plan.API || got.ListenerProtocols[1080] != core.TrafficProtocolTCP {
					t.Errorf("concurrent cache lookup: %+v, %v", got, err)
				}
			}
		})
	}
	workers.Wait()
}

func TestCloneTrafficAccounting(t *testing.T) {
	if cloneTrafficAccounting(nil) != nil {
		t.Fatal("nil accounting was not preserved")
	}
	original := &core.TrafficAccounting{
		Source: "core-api", Inbound: "in", Outbounds: []string{"out"}, Mark: 123,
		ProcessEpoch: "boot:pid:start", ClientReceived: math.MaxInt64, ClientSent: 2,
		TargetReceived: 3, TargetSent: 4, Counters: map[string]uint64{"large": math.MaxInt64},
	}
	clone := cloneTrafficAccounting(original)
	if !reflect.DeepEqual(clone, original) || clone == original {
		t.Fatalf("clone lost fields or precision: %+v", clone)
	}
	original.Outbounds[0], original.Counters["large"], original.ClientReceived = "changed", 1, 1
	if clone.Outbounds[0] != "out" || clone.Counters["large"] != math.MaxInt64 || clone.ClientReceived != math.MaxInt64 {
		t.Fatalf("clone aliases source: %+v", clone)
	}
	clone.Outbounds[0], clone.Counters["large"] = "clone", 2
	if original.Outbounds[0] != "changed" || original.Counters["large"] != 1 {
		t.Fatal("source aliases clone")
	}
}

func BenchmarkAccountingConfiguration(b *testing.B) {
	for engine, source := range accountingCacheFixtures() {
		plan, err := serverconfig.PrepareAccounting(engine, source)
		if err != nil {
			b.Fatal(err)
		}
		for _, cached := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/cached=%t", engine, cached), func(b *testing.B) {
				executor := &Executor{}
				if _, err := executor.accountingConfiguration(engine, plan.Content); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					var err error
					if cached {
						_, err = executor.accountingConfiguration(engine, plan.Content)
					} else {
						_, err = compileAccountingConfiguration(engine, plan.Content)
					}
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkTrafficAccountingCopy(b *testing.B) {
	accounting := &core.TrafficAccounting{Source: "core-api", Inbound: "in", ProcessEpoch: "boot:pid:start", Counters: make(map[string]uint64)}
	for i := range 64 {
		tag := fmt.Sprintf("out-%d", i)
		accounting.Outbounds = append(accounting.Outbounds, tag)
		accounting.Counters[tag+"-rx"] = uint64(i)
		accounting.Counters[tag+"-tx"] = uint64(i)
	}
	for _, direct := range []bool{false, true} {
		b.Run(fmt.Sprintf("direct=%t", direct), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				var clone *core.TrafficAccounting
				if direct {
					clone = cloneTrafficAccounting(accounting)
				} else {
					data, err := json.Marshal(accounting)
					if err != nil {
						b.Fatal(err)
					}
					if err := json.Unmarshal(data, &clone); err != nil {
						b.Fatal(err)
					}
				}
				if len(clone.Counters) != len(accounting.Counters) {
					b.Fatal("lost counters")
				}
			}
		})
	}
}
