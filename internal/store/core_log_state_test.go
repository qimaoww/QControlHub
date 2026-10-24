package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// The heartbeat is the only writer of per-source state. It must accept the
// contract statuses (including not-configured), reject fields it cannot
// attribute, and clamp impossible clock readings instead of dropping the whole
// heartbeat.
func TestHeartbeatValidatesCoreLogState(t *testing.T) {
	db := openPerformanceStore(t)
	ctx := context.Background()
	agent, _ := seedPerformanceAgent(t, db, 0)
	status := func() core.SharedInstanceStatus {
		return core.SharedInstanceStatus{
			Engine: core.EngineXray, ShareID: "shr_0123456789abcdef", Status: "active",
			CoreLogStatus: core.CoreLogStatusWaiting, CoreLogSource: core.CoreLogSourceJournal,
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*core.HeartbeatRequest)
	}{
		{"unknown instance status", func(h *core.HeartbeatRequest) { h.SharedInstances[0].CoreLogStatus = "unknown" }},
		{"unknown instance source", func(h *core.HeartbeatRequest) { h.SharedInstances[0].CoreLogSource = "syslog" }},
		{"unknown main status", func(h *core.HeartbeatRequest) {
			h.Runtime = map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, CoreLogStatus: "collecting"}}
		}},
		{"unknown main source", func(h *core.HeartbeatRequest) {
			h.Runtime = map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, CoreLogStatus: core.CoreLogStatusActive, CoreLogSource: "syslog"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			heartbeat := core.HeartbeatRequest{SharedInstances: []core.SharedInstanceStatus{status()}}
			test.mutate(&heartbeat)
			if err := db.Heartbeat(ctx, agent, heartbeat); !errors.Is(err, ErrInvalid) {
				t.Fatalf("heartbeat error = %v, want ErrInvalid", err)
			}
		})
	}
	// Every contract status, including not-configured, is accepted.
	accepted := core.HeartbeatRequest{SharedInstances: []core.SharedInstanceStatus{status()}}
	accepted.SharedInstances[0].CoreLogStatus = core.CoreLogStatusNotConfigured
	accepted.SharedInstances[0].CoreLogSource = core.CoreLogSourceFile
	accepted.Runtime = map[core.Engine]core.RuntimeState{
		core.EngineXray:    {Installed: true, CoreLogStatus: core.CoreLogStatusNotConfigured, CoreLogSource: core.CoreLogSourceFile},
		core.EngineSingBox: {Installed: true, CoreLogStatus: core.CoreLogStatusWaiting, CoreLogSource: core.CoreLogSourceJournal, CoreLogLastAt: ptrTime(time.Now().UTC().Add(time.Hour))},
	}
	if err := db.Heartbeat(ctx, agent, accepted); err != nil {
		t.Fatalf("contract heartbeat: %v", err)
	}
	var stored *time.Time
	if err := db.pool.QueryRow(ctx, `SELECT (runtime->'sing-box'->>'core_log_last_at')::timestamptz FROM agents WHERE id=$1`, agent).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.After(time.Now().UTC().Add(5*time.Minute)) {
		t.Fatalf("future last line was not clamped: %v", stored)
	}
}

// The directory joins the main runtime snapshot with the shared instance state
// row. A status change must replace the reason (no stale failure text) and a
// quiet or clamped source must still be attributable.
func TestCoreLogSourceDirectoryReportsTransportAndClearsStaleError(t *testing.T) {
	db := openPerformanceStore(t)
	ctx := context.Background()
	owner, scope := sharedTestUser(t, db, ctx, "source-directory-owner")
	agent, _ := seedPerformanceAgent(t, db, 0)
	share := "shr_00000000000000a1"
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`UPDATE agents SET owner_id=$2,
		features='["core-logs-v1","core-log-status-v1","core-log-instances-v1","shared-core-instances-v1"]' WHERE id=$1`, agent, owner.ID)
	mustExec(`INSERT INTO agent_shares(id,agent_id,user_id,status,engines) VALUES($1,$2,$3,'accepted',ARRAY['xray'])`, share, agent, owner.ID)
	mustExec(`INSERT INTO agent_shared_instance_ownership(agent_id,engine,share_id,owner_id,config_id,config_version,updated_at)
		VALUES($1,'xray',$2,$3,'fixture',1,now())`, agent, share, owner.ID)
	past := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	heartbeat := func(status, reason, source string, lastAt *time.Time) {
		t.Helper()
		if err := db.Heartbeat(ctx, agent, core.HeartbeatRequest{
			Features: []string{core.AgentFeatureCoreLogs, core.AgentFeatureCoreLogStatus, core.AgentFeatureCoreLogInstances,
				core.AgentFeatureSharedCoreInstances},
			Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {
				Installed: true, CoreLogStatus: status, CoreLogError: reason, CoreLogSource: source, CoreLogLastAt: lastAt,
			}},
			SharedInstances: []core.SharedInstanceStatus{{
				Engine: core.EngineXray, ShareID: share, Status: "active",
				CoreLogStatus: status, CoreLogError: reason, CoreLogSource: source, CoreLogLastAt: lastAt,
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	directory := func() (CoreLogSource, CoreLogSource) {
		t.Helper()
		sources, err := db.ListCoreLogSources(scope)
		if err != nil {
			t.Fatal(err)
		}
		main, shared := CoreLogSource{}, CoreLogSource{}
		for _, source := range sources {
			if source.AgentID != agent || source.Engine != core.EngineXray {
				continue
			}
			if source.InstanceID == "main" {
				main = source
			}
			if source.InstanceID == share {
				shared = source
			}
		}
		if main.AgentID == "" || shared.AgentID == "" {
			t.Fatalf("directory missing sources: %+v", sources)
		}
		return main, shared
	}

	heartbeat(core.CoreLogStatusFailed, "permission-denied", core.CoreLogSourceJournal, &past)
	main, shared := directory()
	for _, source := range []CoreLogSource{main, shared} {
		if source.Status != core.CoreLogStatusFailed || source.Error != "permission-denied" ||
			source.Source != core.CoreLogSourceJournal || source.LastLogAt == nil || source.LastLogAt.Sub(past).Abs() > time.Second {
			t.Fatalf("failed source = %+v", source)
		}
	}

	// A non-failed status must clear the old failure text even when the Agent
	// sends a reason code for the new state, and not-configured must pass
	// through unchanged rather than becoming failed.
	heartbeat(core.CoreLogStatusNotConfigured, "config-missing", core.CoreLogSourceFile, &past)
	main, shared = directory()
	for _, source := range []CoreLogSource{main, shared} {
		if source.Status != core.CoreLogStatusNotConfigured || source.Error != "" ||
			source.Source != core.CoreLogSourceFile || source.LastLogAt == nil {
			t.Fatalf("not-configured source = %+v", source)
		}
	}
	var storedStatus, storedError, storedSource string
	var storedLastAt *time.Time
	if err := db.pool.QueryRow(ctx, `SELECT status,error,source,last_at FROM core_log_source_states
		WHERE agent_id=$1 AND engine='xray' AND instance_id=$2`, agent, share).Scan(&storedStatus, &storedError, &storedSource, &storedLastAt); err != nil {
		t.Fatal(err)
	}
	if storedStatus != core.CoreLogStatusNotConfigured || storedError != "" || storedSource != core.CoreLogSourceFile || storedLastAt == nil {
		t.Fatalf("persisted state = %s/%q/%s/%v", storedStatus, storedError, storedSource, storedLastAt)
	}

	// A status older than 45 seconds is stale, but its transport and last line
	// stay attributable for the panel.
	mustExec(`UPDATE core_log_source_states SET updated_at=now()-interval '46 seconds' WHERE agent_id=$1`, agent)
	_, shared = directory()
	if shared.Status != core.CoreLogStatusPending || shared.Source != core.CoreLogSourceFile || shared.LastLogAt == nil {
		t.Fatalf("stale source = %+v", shared)
	}

	// A future last-line timestamp cannot move the directory into the future.
	heartbeat(core.CoreLogStatusActive, "", core.CoreLogSourceJournalHost, ptrTime(time.Now().UTC().Add(time.Hour)))
	main, shared = directory()
	for _, source := range []CoreLogSource{main, shared} {
		if source.Status != core.CoreLogStatusActive || source.Error != "" || source.Source != core.CoreLogSourceJournalHost {
			t.Fatalf("active source = %+v", source)
		}
		if source.LastLogAt == nil || source.LastLogAt.After(time.Now().UTC().Add(5*time.Minute)) {
			t.Fatalf("future last line leaked: %v", source.LastLogAt)
		}
	}
	if err := db.pool.QueryRow(ctx, `SELECT last_at FROM core_log_source_states WHERE agent_id=$1`, agent).Scan(&storedLastAt); err != nil {
		t.Fatal(err)
	}
	if storedLastAt == nil || storedLastAt.After(time.Now().UTC().Add(5*time.Minute)) {
		t.Fatalf("future state timestamp leaked: %v", storedLastAt)
	}

	// Revoking the grant removes the row a later grant could mistake for a
	// fresh observation.
	mustExec(`UPDATE agent_shares SET enabled=false WHERE id=$1`, share)
	if err := db.Heartbeat(ctx, agent, core.HeartbeatRequest{SharedInstances: []core.SharedInstanceStatus{{
		Engine: core.EngineXray, ShareID: share, Status: "active", CoreLogStatus: core.CoreLogStatusActive,
	}}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM core_log_source_states WHERE agent_id=$1`, agent).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("revoked state rows = %d", count)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
