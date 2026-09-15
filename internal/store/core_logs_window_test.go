package store

import (
	"context"
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCoreLogsLargeWindowsPreserveEveryEngine(t *testing.T) {
	s := openPerformanceStore(t)
	agentID, _ := seedPerformanceAgent(t, s, 0)
	ctx := context.Background()
	engines := []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust}
	for _, engine := range engines {
		for offset := 0; offset < 2000; offset += core.MaxCoreLogBatchEntries {
			entries := make([]core.CoreLogEntry, min(core.MaxCoreLogBatchEntries, 2000-offset))
			for index := range entries {
				entries[index] = core.CoreLogEntry{Engine: engine, Level: "warning", Message: "large-window fixture"}
			}
			id, err := core.NewID("log")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.StoreCoreLogs(ctx, agentID, core.CoreLogBatch{ID: id, Entries: entries}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, limit := range []int{0, 1000, 2000} {
		got, err := s.ListCoreLogs(ctx, CoreLogQuery{AgentID: agentID, Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		want := limit
		if want == 0 {
			want = 1000
		}
		if len(got) != 4*want {
			t.Fatalf("limit %d returned %d rows, want %d", limit, len(got), 4*want)
		}
		counts := map[core.Engine]int{}
		for index, entry := range got {
			counts[entry.Engine]++
			if index > 0 && entry.ID >= got[index-1].ID {
				t.Fatal("large result is not strictly newest first")
			}
		}
		for _, engine := range engines {
			if counts[engine] != want {
				t.Fatalf("%s count = %d, want %d", engine, counts[engine], want)
			}
		}
	}
	if _, err := s.ListCoreLogs(ctx, CoreLogQuery{Limit: 2001}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbounded query accepted: %v", err)
	}
}
