package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCoreLogInstanceIsolation(t *testing.T) {
	db := openPerformanceStore(t)
	ctx := context.Background()
	admin, _ := sharedTestUser(t, db, ctx, "log-admin")
	alice, aCtx := sharedTestUser(t, db, ctx, "log-alice")
	bob, bCtx := sharedTestUser(t, db, ctx, "log-bob")
	adminCtx := WithConfigScope(ctx, admin.ID, true)
	host, _ := seedPerformanceAgent(t, db, 0)
	aNode, _ := seedPerformanceAgent(t, db, 0)
	bNode, _ := seedPerformanceAgent(t, db, 0)
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ agent, owner string }{{host, admin.ID}, {aNode, alice.ID}, {bNode, bob.ID}} {
		mustExec(`UPDATE agents SET owner_id=$2,features='["core-logs-v1","core-log-instances-v1","core-log-status-v1"]' WHERE id=$1`, item.agent, item.owner)
	}
	shares := []string{"shr_0000000000000001", "shr_0000000000000002"}
	for index, user := range []core.User{alice, bob} {
		mustExec(`INSERT INTO agent_shares(id,agent_id,user_id,status,engines) VALUES($1,$2,$3,'accepted',ARRAY['mihomo','xray','sing-box','ss-rust'])`, shares[index], host, user.ID)
		for _, engine := range core.AllEngines() {
			mustExec(`INSERT INTO agent_shared_instance_ownership(agent_id,engine,share_id,owner_id,config_id,config_version,updated_at) VALUES($1,$2,$3,$4,'fixture',1,now())`, host, engine, shares[index], user.ID)
		}
	}
	for _, engine := range core.AllEngines() {
		for _, source := range []struct{ agent, instance, message string }{{host, "main", "main"}, {host, shares[0], "alice"}, {host, shares[1], "bob"}, {aNode, "main", "alice-private"}, {bNode, "main", "bob-private"}, {host, "", "legacy"}} {
			service := ""
			if source.instance != "" {
				service = core.CoreLogService(engine, source.instance, "systemd")
			}
			id, _ := core.NewID("log")
			batch := core.CoreLogBatch{ID: id, Entries: []core.CoreLogEntry{{Engine: engine, InstanceID: source.instance, Service: service, OwnerID: "forged-owner", Level: "warning", Message: source.message}}}
			if err := db.StoreCoreLogs(ctx, source.agent, batch); err != nil {
				t.Fatal(err)
			}
			if err := db.StoreCoreLogs(ctx, source.agent, batch); err != nil {
				t.Fatal(err)
			}
		}
	}
	check := func(scope context.Context, query CoreLogQuery, want map[string]int) {
		t.Helper()
		logs, err := db.ListCoreLogs(scope, query)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int{}
		for _, entry := range logs {
			got[entry.Message]++
			if entry.Message == "alice" && entry.OwnerID != alice.ID {
				t.Fatal("agent forged ownership")
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("logs %v want %v", got, want)
		}
	}
	check(adminCtx, CoreLogQuery{}, map[string]int{"main": 4, "alice": 4, "bob": 4, "legacy": 4})
	check(aCtx, CoreLogQuery{}, map[string]int{"alice": 4, "alice-private": 4})
	check(bCtx, CoreLogQuery{}, map[string]int{"bob": 4, "bob-private": 4})
	check(aCtx, CoreLogQuery{AgentID: host, InstanceID: shares[0], Engine: core.EngineXray, Level: "warning", Search: "ali"}, map[string]int{"alice": 1})
	for _, query := range []CoreLogQuery{{AgentID: bNode}, {AgentID: host, InstanceID: shares[1]}, {UserID: bob.ID}, {AgentID: host, InstanceID: "main"}} {
		if _, err := db.ListCoreLogs(aCtx, query); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign scope accepted %+v: %v", query, err)
		}
	}
	for _, query := range []CoreLogQuery{{AgentID: aNode}, {UserID: alice.ID, AgentID: aNode}} {
		if _, err := db.ListCoreLogs(adminCtx, query); !errors.Is(err, ErrNotFound) {
			t.Fatalf("admin escaped node privacy: %v", err)
		}
	}
	logs, err := db.ListCoreLogs(aCtx, CoreLogQuery{AgentID: host, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 4 {
		t.Fatalf("per-engine window %d", len(logs))
	}
	for _, entry := range logs {
		if entry.Message != "alice" {
			t.Fatal("foreign history")
		}
	}
	check(aCtx, CoreLogQuery{AgentID: host, Before: logs[len(logs)-1].ID}, map[string]int{})
	sources, err := db.ListCoreLogSources(aCtx)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.AgentID == bNode || source.Username == bob.Username || source.AgentID == host && source.InstanceID != shares[0] {
			t.Fatalf("source directory leaked %+v", source)
		}
	}
	adminSources, err := db.ListCoreLogSources(adminCtx)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range adminSources {
		if source.AgentID != host {
			t.Fatal("admin source metadata leak")
		}
	}
	// Missing or mismatched identities must not poison another instance's batch.
	id, _ := core.NewID("log")
	if err := db.StoreCoreLogs(ctx, host, core.CoreLogBatch{ID: id, Entries: []core.CoreLogEntry{
		{Engine: core.EngineXray, InstanceID: shares[0], Service: core.CoreLogService(core.EngineXray, shares[1], "systemd"), Level: "info", Message: "forged-service"},
		{Engine: core.EngineXray, InstanceID: "shr_ffffffffffffffff", Service: core.CoreLogService(core.EngineXray, "shr_ffffffffffffffff", "systemd"), Level: "info", Message: "unknown-instance"},
		{Engine: core.EngineXray, InstanceID: shares[1], Service: core.CoreLogService(core.EngineXray, shares[1], "openrc"), Level: "info", Message: "unaffected"},
	}}); err != nil {
		t.Fatal(err)
	}
	check(bCtx, CoreLogQuery{AgentID: host, Search: "unaffected"}, map[string]int{"unaffected": 1})
	mustExec(`UPDATE agent_shares SET enabled=false WHERE id=$1`, shares[0])
	check(aCtx, CoreLogQuery{}, map[string]int{"alice-private": 4})
	if _, err := db.ListCoreLogs(aCtx, CoreLogQuery{AgentID: host}); !errors.Is(err, ErrNotFound) {
		t.Fatal("revocation ignored")
	}
	mustExec(`UPDATE panel_users SET disabled=true WHERE id=$1`, bob.ID)
	check(bCtx, CoreLogQuery{}, map[string]int{})
	// Transfers cannot hand a new owner historical diagnostics or guessed legacy ownership.
	mustExec(`UPDATE agents SET owner_id=$2 WHERE id=$1`, aNode, admin.ID)
	check(adminCtx, CoreLogQuery{AgentID: aNode}, map[string]int{})
	// An arbitrary main-service installer never changes node ownership.
	mustExec(`INSERT INTO agent_engine_ownership(agent_id,engine,owner_id,config_id,config_version,updated_at) VALUES($1,'mihomo',$2,'fixture',1,now())`, host, alice.ID)
	check(adminCtx, CoreLogQuery{AgentID: host, InstanceID: "main"}, map[string]int{"main": 4})
	// An ordinary node owner still cannot inspect another recipient's instance.
	// Even an administrator explicitly listed as recipient cannot read a
	// user-owned node through the log APIs.
	mustExec(`UPDATE agents SET owner_id=$2 WHERE id=$1`, bNode, alice.ID)
	for i, user := range []core.User{bob, admin} {
		share := fmt.Sprintf("shr_%016x", i+100)
		mustExec(`INSERT INTO agent_shares(id,agent_id,user_id,status,engines) VALUES($1,$2,$3,'accepted',ARRAY['xray'])`, share, bNode, user.ID)
		mustExec(`INSERT INTO agent_shared_instance_ownership(agent_id,engine,share_id,owner_id,config_id,config_version,updated_at) VALUES($1,'xray',$2,$3,'fixture',1,now())`, bNode, share, user.ID)
		id, _ := core.NewID("log")
		if err := db.StoreCoreLogs(ctx, bNode, core.CoreLogBatch{ID: id, Entries: []core.CoreLogEntry{{Engine: core.EngineXray, InstanceID: share, Service: core.CoreLogService(core.EngineXray, share, "systemd"), Level: "info", Message: "private recipient"}}}); err != nil {
			t.Fatal(err)
		}
	}
	// Reactivate B: neither A's owner grant
	// nor the administrator's recipient grant discloses these foreign rows.
	mustExec(`UPDATE panel_users SET disabled=false WHERE id=$1`, bob.ID)
	check(aCtx, CoreLogQuery{AgentID: bNode}, map[string]int{})
	check(bCtx, CoreLogQuery{AgentID: bNode}, map[string]int{"private recipient": 1})
	if _, err := db.ListCoreLogs(adminCtx, CoreLogQuery{AgentID: bNode}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("admin recipient bypassed user-owned node privacy: %v", err)
	}
}

func TestCoreLogAuthorizedWindowUsesInstanceIndex(t *testing.T) {
	db := openPerformanceStore(t)
	ctx := context.Background()
	user, scope := sharedTestUser(t, db, ctx, "log-plan")
	agent, _ := seedPerformanceAgent(t, db, 0)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET owner_id=$2 WHERE id=$1`, agent, user.ID); err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		id, _ := core.NewID("log")
		entries := []core.CoreLogEntry{}
		for j := range 32 {
			entries = append(entries, core.CoreLogEntry{Engine: core.EngineXray, InstanceID: "main", Service: "qagent-xray.service", Level: "info", Message: fmt.Sprintf("%d/%d", i, j)})
		}
		if err := db.StoreCoreLogs(ctx, agent, core.CoreLogBatch{ID: id, Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO core_logs(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at,instance_id,node_owner_id,owner_id)
 SELECT (SELECT id FROM core_log_batches LIMIT 1),0,$1,'xray','info','foreign pressure',now(),now()+i*interval '1 microsecond','shr_'||lpad(to_hex(i%100),16,'0'),$2,'foreign'
 FROM generate_series(1,50000) i`, agent, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `VACUUM (ANALYZE) core_logs`); err != nil {
		t.Fatal(err)
	}
	statement, args := scopedCoreLogWindowStatement(scope, CoreLogQuery{AgentID: agent, InstanceID: "main", Limit: 10}, []string{"xray"})
	plan := explainJSONOnDedicatedConnection(t, db, statement, args...)
	summary := planSummary(plan)
	var indexes []string
	if err := db.pool.QueryRow(ctx, `SELECT array_agg(child.relname) FROM pg_inherits i JOIN pg_class child ON child.oid=i.inhrelid WHERE i.inhparent='core_logs_instance_recent_idx'::regclass`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, index := range indexes {
		if strings.Contains(summary, index) {
			found = true
		}
	}
	if !found {
		t.Fatalf("instance index absent: %s", summary)
	}
	t.Log(summary)
}

func TestCoreLogUnverifiedHistoryNeverInheritsNodeOwner(t *testing.T) {
	db := openPerformanceStore(t)
	ctx := context.Background()
	node, _ := seedPerformanceAgent(t, db, 0)
	id, _ := core.NewID("log")
	if err := db.StoreCoreLogs(ctx, node, core.CoreLogBatch{ID: id, Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, Level: "info", Message: "verified legacy upload"}}}); err != nil {
		t.Fatal(err)
	}
	// Reproduce the columns of a pre-upgrade row. The migration's reserved
	// snapshot cannot equal either the unassigned owner or a later real user.
	if _, err := db.pool.Exec(ctx, `INSERT INTO core_logs(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at)
	 VALUES($1,99,$2,'mihomo','info','unverified history',now(),now())`, id, node); err != nil {
		t.Fatal(err)
	}
	logs, err := db.ListCoreLogs(WithConfigScope(ctx, "", true), CoreLogQuery{AgentID: node})
	if err != nil || len(logs) != 1 || logs[0].Message != "verified legacy upload" {
		t.Fatalf("unassigned historical provenance leaked: %+v %v", logs, err)
	}
	user, scope := sharedTestUser(t, db, ctx, "later-log-owner")
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET owner_id=$2 WHERE id=$1`, node, user.ID); err != nil {
		t.Fatal(err)
	}
	logs, err = db.ListCoreLogs(scope, CoreLogQuery{AgentID: node})
	if err != nil || len(logs) != 0 {
		t.Fatalf("transfer guessed historical attribution: %+v %v", logs, err)
	}
}
