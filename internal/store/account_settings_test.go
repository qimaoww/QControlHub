package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAccountRuntimePoliciesAndRetentionAreIndependent(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	aliceUser, alice := sharedTestUser(t, db, ctx, "retention-alice")
	bobUser, bob := sharedTestUser(t, db, ctx, "retention-bob")
	first, second := sharedTestAgent(t, db, alice), sharedTestAgent(t, db, bob)
	short, long := core.DefaultPanelSettings(), core.DefaultPanelSettings()
	short.TaskRetentionDays, short.AuditRetentionDays, short.MetricRetentionDays, short.CoreLogRetentionDays = 30, 30, 7, 1
	short.AgentOfflineThresholdSeconds, short.TaskStaleTimeoutSeconds, short.TaskMaxAttempts = 45, 60, 1
	short.CoreLogMinimumLevel, short.WebhookURL = "error", "https://alice.example/hook"
	long.TaskRetentionDays, long.AuditRetentionDays, long.MetricRetentionDays, long.CoreLogRetentionDays = 0, 0, 30, 7
	long.AgentOfflineThresholdSeconds, long.TaskStaleTimeoutSeconds = 180, 600
	long.CoreLogMinimumLevel, long.WebhookURL = "info", "https://bob.example/hook"
	for _, item := range []struct {
		scope context.Context
		value core.PanelSettings
	}{{alice, short}, {bob, long}} {
		if _, err := db.SavePanelSettings(item.scope, item.value); err != nil {
			t.Fatal(err)
		}
	}
	sharedTestAllocation(t, db, ctx, bobUser.ID, first.ID, 1000, 22001)
	if _, err := db.AgentPanelSettings(bob, first.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("recipient read the host owner's runtime secrets: %v", err)
	}
	for _, item := range []struct {
		agent core.Agent
		scope context.Context
		owner string
	}{{first, alice, aliceUser.ID}, {second, bob, bobUser.ID}} {
		settings, err := db.AgentPanelSettings(ctx, item.agent.ID)
		if err != nil || settings.WebhookURL != map[string]string{aliceUser.ID: short.WebhookURL, bobUser.ID: long.WebhookURL}[item.owner] {
			t.Fatalf("agent background settings lost ownership: %+v %v", settings, err)
		}
		batchID, _ := core.NewID("log")
		if err := db.StoreCoreLogs(ctx, item.agent.ID, core.CoreLogBatch{ID: batchID, Entries: []core.CoreLogEntry{
			{Engine: core.EngineMihomo, Level: "info", Message: "information"},
			{Engine: core.EngineMihomo, Level: "error", Message: "error"},
		}}); err != nil {
			t.Fatal(err)
		}
		logs, err := db.ListCoreLogs(item.scope, CoreLogQuery{})
		want := 1
		if item.owner == bobUser.ID {
			want = 2
		}
		if err != nil || len(logs) != want {
			t.Fatalf("log ingestion ignored the owner's policy: %+v %v", logs, err)
		}
		if err := db.RecordAudit(item.scope, core.AuditLogEntry{Action: "old-entry", ActedAt: time.Now().Add(-40 * 24 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.pool.Exec(ctx, `INSERT INTO metric_samples(agent_id,sampled_at,cpu_percent,memory_percent,rx_rate_bps,tx_rate_bps)
			VALUES($1,now()-interval '10 days',1,1,1,1)`, item.agent.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.pool.Exec(ctx, `INSERT INTO tasks(id,agent_id,engine,action,status,owner_id,created_at,finished_at)
			VALUES($1,$2,'mihomo','status','succeeded',$3,now()-interval '40 days',now()-interval '40 days')`,
			"old-"+item.owner, item.agent.ID, item.owner); err != nil {
			t.Fatal(err)
		}
		task, err := db.CreateTask(item.scope, core.TaskRequest{AgentID: item.agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ClaimTask(ctx, item.agent.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.pool.Exec(ctx, `UPDATE tasks SET started_at=now()-interval '2 minutes' WHERE id=$1`, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if err := db.EnsureCoreLogPartitions(ctx, now.Add(-4*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE core_logs SET received_at=$1,logged_at=$1`, now.Add(-4*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET last_seen=$1,presence_notification_state='online'`, now.Add(-70*time.Second)); err != nil {
		t.Fatal(err)
	}
	transitions, err := db.AgentPresenceTransitions(ctx, now, 90*time.Second)
	if err != nil || len(transitions) != 1 || transitions[0].Agent.ID != first.ID || transitions[0].Online {
		t.Fatalf("presence ignored the owner's threshold: %+v %v", transitions, err)
	}
	if err := db.MaintainAccountData(ctx, now, true); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		scope context.Context
		agent core.Agent
		want  int
	}{{alice, first, 0}, {bob, second, 1}} {
		logs, err := db.ListCoreLogs(item.scope, CoreLogQuery{})
		if err != nil || (len(logs) > 0) != (item.want > 0) {
			t.Fatalf("log pruning crossed account policy: %+v %v", logs, err)
		}
		audit, err := db.ListAuditLogs(item.scope, 100)
		if err != nil || len(audit) != item.want {
			t.Fatalf("audit pruning crossed account policy: %+v %v", audit, err)
		}
		metrics, err := db.MetricSamples(item.scope, item.agent.ID, now.Add(-20*24*time.Hour), 10)
		if err != nil || len(metrics) != item.want {
			t.Fatalf("metric pruning crossed account policy: %+v %v", metrics, err)
		}
		var oldCount int
		if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE id=$1`, "old-"+item.agent.OwnerID).Scan(&oldCount); err != nil || oldCount != item.want {
			t.Fatalf("task pruning crossed account policy: %d %v", oldCount, err)
		}
		var status string
		if err := db.pool.QueryRow(ctx, `SELECT status FROM tasks WHERE agent_id=$1 AND id<>$2`, item.agent.ID, "old-"+item.agent.OwnerID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if (item.want == 0 && status != "failed") || (item.want == 1 && status != "running") {
			t.Fatalf("stale task policy crossed accounts: %s", status)
		}
	}
}

func TestSubStoreCanonicalBackendsAndIndependentLocks(t *testing.T) {
	for _, aliases := range [][]string{
		{"https://substore.example/secret", "https://SUBSTORE.example:443/secret/", "https://substore.example.:0443/secret"},
		{"http://[::1]/secret", "http://[0:0:0:0:0:0:0:1]:80/secret/"},
		{"https://xn--bcher-kva.example/secret", "https://bücher.example/secret"},
	} {
		for _, alias := range aliases {
			if subStoreBackendKey(alias) != subStoreBackendKey(aliases[0]) {
				t.Fatalf("same backend has two lock identities: %s", alias)
			}
		}
	}
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, alice := sharedTestUser(t, db, ctx, "backend-alice")
	_, bob := sharedTestUser(t, db, ctx, "backend-bob")
	if _, err := db.SaveSubStoreSyncSettings(alice, "https://substore.example/alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveSubStoreSyncSettings(bob, "https://substore.example/bob"); err != nil {
		t.Fatal(err)
	}
	release, err := db.TryLockSubStoreOperation(alice)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	otherRelease, err := db.TryLockSubStoreOperation(bob)
	if err != nil {
		t.Fatalf("independent backends blocked each other: %v", err)
	}
	otherRelease()
	if _, err := db.SaveSubStoreSyncSettings(bob, "https://SUBSTORE.example:443/alice/"); err != nil {
		t.Fatal(err)
	}
	if unlock, err := db.TryLockSubStoreOperation(bob); !errors.Is(err, ErrConflict) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("default-port alias bypassed shared backend lock: %v", err)
	}
	var ciphertext string
	if err := db.pool.QueryRow(ctx, `SELECT endpoint_ciphertext FROM user_substore_settings WHERE owner_id=$1`, scopeForConfig(alice).OwnerID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, "substore.example") {
		t.Fatal("personal Sub-Store credentials were stored in plaintext")
	}
}
