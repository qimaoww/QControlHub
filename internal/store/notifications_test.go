package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/notify"
)

func notificationFixture(t *testing.T) (context.Context, *Store, core.Agent, notify.Configuration) {
	t.Helper()
	ctx, s := optionalTrafficTestStore(t)
	a, _ := enrollTaskTestAgent(t, ctx, s)
	if err := s.Heartbeat(ctx, a.ID, core.HeartbeatRequest{}); err != nil {
		t.Fatal(err)
	}
	c := notify.Configuration{Revision: 1, Channels: []notify.Channel{{ID: "hook", Name: "fixture", Type: "webhook", Enabled: true, Values: map[string]string{}, Secrets: map[string]string{"url": "https://example.com/notify", "secret": "private-signature"}}}, Rules: []notify.Rule{{ID: "offline", Name: "offline", Event: notify.TypeAgentOffline, Enabled: true, Channels: []string{"hook"}, Duration: 10, Cooldown: 60}}, Templates: map[string]notify.Template{}}
	saved, err := s.SaveNotificationConfiguration(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, s, a, saved
}
func forceNotificationScan(t *testing.T, ctx context.Context, s *Store) {
	t.Helper()
	if _, err := s.pool.Exec(ctx, `UPDATE notification_configs SET scanned_at='epoch'`); err != nil {
		t.Fatal(err)
	}
	if err := s.ScanNotifications(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestNotificationPresenceOutboxRetryRestartAndCancellation(t *testing.T) {
	ctx, s, a, c := notificationFixture(t)
	var attempts atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "private remote diagnostic", 503)
			return
		}
		w.WriteHeader(204)
	}))
	defer endpoint.Close()
	t.Setenv("QCH_NOTIFICATION_ALLOWED_CIDRS", "127.0.0.0/8")
	c.Channels[0].Secrets["url"] = endpoint.URL
	overlapping := c.Rules[0]
	overlapping.ID = "overlapping-offline"
	c.Rules = append(c.Rules, overlapping)
	c, err := s.SaveNotificationConfiguration(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AgentPresenceTransitions(ctx, time.Now(), 45*time.Second); err != nil {
		t.Fatal(err)
	}
	forceNotificationScan(t, ctx, s)
	_, err = s.pool.Exec(ctx, `UPDATE agents SET last_seen=now()-interval '2 minutes' WHERE id=$1`, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.AgentPresenceTransitions(ctx, time.Now(), 45*time.Second); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	forceNotificationScan(t, ctx, s)
	forceNotificationScan(t, ctx, s)
	rows, err := s.NotificationDeliveries(ctx)
	if err != nil || len(rows) != 1 {
		var diagnostic string
		_ = s.pool.QueryRow(ctx, `SELECT jsonb_build_object('sources',(SELECT jsonb_agg(e) FROM notification_events e),'states',(SELECT jsonb_agg(st) FROM notification_rule_states st))::text`).Scan(&diagnostic)
		t.Fatalf("durable dedupe %+v %v %s agent=%+v", rows, err, diagnostic, a)
	}
	sender := notify.NewSender()
	if ok, err := s.DeliverNotification(ctx, sender, ""); err != nil || !ok {
		t.Fatalf("attempt %v %v", ok, err)
	}
	rows, _ = s.NotificationDeliveries(ctx)
	if rows[0].Status != "pending" || rows[0].Attempts != 1 || strings.Contains(rows[0].Error, "private") {
		t.Fatalf("retry %+v", rows)
	}
	// An expired lease models a process that died after claim; the same row survives.
	_, err = s.pool.Exec(ctx, `UPDATE notification_deliveries SET status='sending',lease_until=now()-interval '1 second',next_at=now()-interval '1 minute',updated_at=now()-interval '1 minute'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeliverNotification(ctx, sender, ""); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.NotificationDeliveries(ctx)
	if rows[0].Status != "accepted" || rows[0].Attempts != 2 {
		t.Fatalf("restart %+v", rows)
	}
	id, err := s.QueueNotificationTest(ctx, "hook", c.Revision)
	if err != nil {
		t.Fatal(err)
	}
	c.Channels[0].Enabled = false
	if _, err = s.SaveNotificationConfiguration(ctx, c); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = s.pool.QueryRow(ctx, `SELECT status FROM notification_deliveries WHERE id=$1`, id).Scan(&status)
	if status != "canceled" {
		t.Fatal("revoked target still queued", status)
	}
	if attempts.Load() != 2 {
		t.Fatal("unexpected sends", attempts.Load())
	}
}
func TestNotificationSpeedWindowMissingDataAndRecovery(t *testing.T) {
	ctx, s, a, c := notificationFixture(t)
	c.Rules = []notify.Rule{{ID: "speed", Name: "speed", Event: "speed.high", Enabled: true, AgentID: a.ID, Channels: []string{"hook"}, Direction: "upload", Threshold: 1000, Recovery: 500, Duration: 10, Cooldown: 60}}
	if _, err := s.SaveNotificationConfiguration(ctx, c); err != nil {
		t.Fatal(err)
	}
	sample := func(rate uint64, valid bool) {
		t.Helper()
		m := core.HostMetrics{NetworkSampledAt: time.Now().UTC(), CollectedAt: time.Now().UTC(), NetworkAvailable: valid, NetworkRateAvailable: &valid, NetworkTXBPS: rate}
		b, _ := json.Marshal(m)
		_, err := s.pool.Exec(ctx, `INSERT INTO agent_live_state(agent_id,metrics) VALUES($1,$2) ON CONFLICT(agent_id) DO UPDATE SET metrics=EXCLUDED.metrics`, a.ID, b)
		if err != nil {
			t.Fatal(err)
		}
		forceNotificationScan(t, ctx, s)
	}
	sample(2000, true)
	sample(2000, true)
	r, _ := s.NotificationDeliveries(ctx)
	if len(r) != 0 {
		t.Fatal("spike notified")
	}
	sample(0, false)
	sample(2000, true)
	r, _ = s.NotificationDeliveries(ctx)
	if len(r) != 0 {
		t.Fatal("missing sample caused alert")
	}
	sample(2000, true)
	_, err := s.pool.Exec(ctx, `UPDATE notification_rule_states SET state=jsonb_set(state,'{since}',to_jsonb(now()-interval '20 seconds')) WHERE rule_id='speed'`)
	if err != nil {
		t.Fatal(err)
	}
	sample(2000, true)
	r, _ = s.NotificationDeliveries(ctx)
	if len(r) != 1 || r[0].Event.Type != "speed.high" || r[0].Event.Current != "2000 B/s" {
		t.Fatalf("sustained %+v", r)
	}
	sample(0, false)
	sample(0, true)
	r, _ = s.NotificationDeliveries(ctx)
	if len(r) != 1 {
		t.Fatal("missing data emitted recovery")
	}
	// Restore active state across the first valid sample after a gap, then recover over a full window.
	_, err = s.pool.Exec(ctx, `UPDATE notification_rule_states SET state=jsonb_set(state,'{active}','true') WHERE rule_id='speed'`)
	if err != nil {
		t.Fatal(err)
	}
	sample(100, true)
	_, err = s.pool.Exec(ctx, `UPDATE notification_rule_states SET state=jsonb_set(state,'{since}',to_jsonb(now()-interval '20 seconds')) WHERE rule_id='speed'`)
	if err != nil {
		t.Fatal(err)
	}
	sample(100, true)
	r, _ = s.NotificationDeliveries(ctx)
	if len(r) != 2 || r[0].Event.Type != "speed.recovered" {
		t.Fatalf("recovery %+v", r)
	}
}
func TestNotificationSecretsOwnershipAndUpdateCache(t *testing.T) {
	ctx, s, _, c := notificationFixture(t)
	c = c.Redacted()
	c.Channels[0].Values["unused"] = "x"
	if _, err := s.SaveNotificationConfiguration(ctx, c); err == nil {
		t.Fatal("unknown field accepted")
	}
	delete(c.Channels[0].Values, "unused")
	saved, err := s.SaveNotificationConfiguration(ctx, c)
	if err != nil || saved.Channels[0].Get("secret") != "private-signature" {
		t.Fatalf("secret retention %+v %v", saved.Redacted(), err)
	}
	other := WithConfigScope(ctx, "token_other", false)
	theirs, err := s.NotificationConfiguration(other)
	if err != nil || len(theirs.Channels) != 0 {
		t.Fatal("cross account config")
	}
	calls := atomic.Int64{}
	fetch := func(context.Context) (string, time.Time, error) {
		calls.Add(1)
		return strings.Repeat("a", 40), time.Now().UTC(), nil
	}
	for i := 0; i < 5; i++ {
		if _, err = s.RefreshNotificationUpdate(ctx, true, fetch); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate upstream checks", calls.Load())
	}
}

func TestNotificationMigrationLegacyCompatibilityAndRollback(t *testing.T) {
	ctx, s := optionalTrafficTestStore(t)
	settings := core.DefaultPanelSettings()
	settings.WebhookURL = "https://example.com/legacy?token=fixture"
	settings.NotifyTaskFailed = true
	if _, err := s.SavePanelSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DROP TABLE notification_rule_states,notification_deliveries,notification_events,notification_update_cache,notification_configs;
 DROP INDEX notification_failed_tasks; ALTER TABLE tasks DROP COLUMN notification_failure_attempt;
 DELETE FROM qcontrolhub_schema_migrations WHERE version>90; INSERT INTO qcontrolhub_schema_migrations(version) VALUES(90) ON CONFLICT DO NOTHING; CREATE TABLE notification_events(id integer);`); err != nil {
		t.Fatal(err)
	}
	database := s.pool.Config().ConnString()
	if broken, err := Open(ctx, database, true); err == nil {
		broken.Close()
		t.Fatal("migration fault was ignored")
	}
	var version int
	var present bool
	if err := s.pool.QueryRow(ctx, `SELECT max(version),EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='tasks' AND column_name='notification_failure_attempt') FROM qcontrolhub_schema_migrations`).Scan(&version, &present); err != nil || version != 90 || present {
		t.Fatalf("migration did not roll back %d %v %v", version, present, err)
	}
	if _, err := s.pool.Exec(ctx, `DROP TABLE notification_events`); err != nil {
		t.Fatal(err)
	}
	migrated, err := OpenWithConfigKey(ctx, database, true, strings.Repeat("n", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	c, err := migrated.NotificationConfiguration(ctx)
	if err != nil || len(c.Channels) != 1 || c.Channels[0].ID != "legacy-webhook" || c.Channels[0].Get("url") != settings.WebhookURL {
		t.Fatalf("legacy migration %+v %v", c.Redacted(), err)
	}
	c, err = migrated.SaveNotificationConfiguration(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = migrated.pool.QueryRow(ctx, `SELECT content FROM notification_configs WHERE owner_id=''`).Scan(&raw); err != nil || strings.Contains(raw, "example.com") {
		t.Fatal("notification secrets not encrypted", err)
	}
	unchanged, err := migrated.PanelSettings(ctx)
	if err != nil || unchanged.WebhookURL != settings.WebhookURL || !unchanged.NotifyTaskFailed {
		t.Fatal("legacy settings lost", err)
	}
}

func TestNotificationSharedAllocationAndRevocation(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "notification-node-owner")
	aliceUser, alice := sharedTestUser(t, db, ctx, "notification-shared-alice")
	bobUser, bob := sharedTestUser(t, db, ctx, "notification-shared-bob")
	a := sharedTestAgent(t, db, owner)
	aliceAccess := sharedTestAllocation(t, db, ctx, aliceUser.ID, a.ID, 1000, 21001)
	sharedTestAllocation(t, db, ctx, bobUser.ID, a.ID, 9000, 21002)
	for _, account := range []struct {
		scope context.Context
		port  int
	}{{alice, 21001}, {bob, 21002}} {
		conf := sharedTestConfig(t, db, account.scope, a.ID, account.port)
		task, er := db.CreateTask(account.scope, core.TaskRequest{AgentID: a.ID, Engine: conf.Engine, ConfigID: conf.ID, Action: core.ActionDeploy})
		if er != nil {
			t.Fatal(er)
		}
		claimed, er := db.ClaimTask(ctx, a.ID)
		if er != nil || claimed == nil {
			t.Fatalf("claim %+v %v", claimed, er)
		}
		if er = completeSharedTestTask(db, ctx, a.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); er != nil {
			t.Fatal(er)
		}
	}
	policies, err := db.ListPortTrafficPolicies(alice)
	if err != nil || len(policies) != 1 {
		t.Fatalf("shared policy %+v %v", policies, err)
	}
	policy := policies[0]
	config := notify.Configuration{Revision: 1, Channels: []notify.Channel{{ID: "hook", Name: "shared", Type: "webhook", Enabled: true, Secrets: map[string]string{"url": "https://example.com/shared"}}}, Rules: []notify.Rule{{ID: "warning", Name: "warning", Event: "traffic.warning", Enabled: true, PolicyID: policy.ID, Channels: []string{"hook"}, Threshold: 80, Duration: 10, Cooldown: 60}, {ID: "quota", Name: "quota", Event: notify.TypeTrafficQuota, Enabled: true, Channels: []string{"hook"}, Duration: 10, Cooldown: 60}}}
	if _, err = db.SaveNotificationConfiguration(alice, config); err != nil {
		t.Fatal(err)
	}
	// Borrowing a node never grants host metrics or another recipient's usage.
	bad := config
	bad.Rules = []notify.Rule{{ID: "speed", Name: "speed", Event: "speed.high", Enabled: true, AgentID: a.ID, Direction: "upload", Threshold: 100, Recovery: 50, Duration: 10, Cooldown: 60, Channels: []string{"hook"}}}
	bad.Revision = 2
	if _, err = db.SaveNotificationConfiguration(alice, bad); err == nil {
		t.Fatal("shared user gained host metrics")
	}
	bobPolicies, err := db.ListPortTrafficPolicies(bob)
	if err != nil || len(bobPolicies) != 1 {
		t.Fatal(err)
	}
	bad.Rules = config.Rules
	bad.Rules[0].PolicyID = bobPolicies[0].ID
	if _, err = db.SaveNotificationConfiguration(alice, bad); err == nil {
		t.Fatal("foreign shared policy accepted")
	}
	config.Rules[0].PolicyID = policy.ID
	if _, err = db.pool.Exec(ctx, `UPDATE port_traffic_policies SET last_collected_at=now() WHERE id=$1`, policy.ID); err != nil {
		t.Fatal(err)
	}
	forceNotificationScan(t, ctx, db)
	if _, err = db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=850 WHERE id=$1`, aliceAccess.Shares[0].ID); err != nil {
		t.Fatal(err)
	}
	forceNotificationScan(t, ctx, db)
	rows, err := db.NotificationDeliveries(alice)
	if err != nil || len(rows) != 1 || rows[0].Event.UsedBytes != 850 || rows[0].Event.LimitBytes != 1000 || rows[0].Event.Port != 0 || rows[0].Event.Period != "分配累计（不按月重置）" {
		t.Fatalf("allocation data %+v %v", rows, err)
	}
	if _, err = db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=1000 WHERE id=$1`, aliceAccess.Shares[0].ID); err != nil {
		t.Fatal(err)
	}
	forceNotificationScan(t, ctx, db)
	rows, err = db.NotificationDeliveries(alice)
	if err != nil || len(rows) != 2 || rows[0].Event.Type != notify.TypeTrafficQuota {
		t.Fatalf("allocation quota %+v %v", rows, err)
	}
	// Revoke through the business API and verify both reads and queued delivery recheck it.
	current, err := db.UserAgentAccess(ctx, aliceUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SetUserAgentAccess(ctx, aliceUser.ID, core.AgentAccessRequest{Isolated: true, Revision: current.Revision}); err != nil {
		t.Fatal(err)
	}
	rows, err = db.NotificationDeliveries(alice)
	if err != nil || len(rows) != 0 {
		t.Fatal("revoked records exposed", err)
	}
	if _, err = db.DeliverNotification(ctx, notify.NewSender(), ""); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.pool.QueryRow(ctx, `SELECT status FROM notification_deliveries ORDER BY id LIMIT 1`).Scan(&status); err != nil || status != "canceled" {
		t.Fatal("revoked message sent", status, err)
	}
}

func TestNotificationUpdatesDeduplicateFullVersionsAndKeepFailures(t *testing.T) {
	ctx, s, _, c := notificationFixture(t)
	c.Rules = []notify.Rule{{ID: "update", Name: "update", Event: "panel.update", Enabled: true, Channels: []string{"hook"}, Duration: 10, Cooldown: 60, Interval: 3600}}
	if _, err := s.SaveNotificationConfiguration(ctx, c); err != nil {
		t.Fatal(err)
	}
	forceNotificationScan(t, ctx, s)
	for _, version := range []string{strings.Repeat("a", 39) + "1", strings.Repeat("a", 39) + "1", strings.Repeat("a", 39) + "2"} {
		if _, err := s.pool.Exec(ctx, `UPDATE notification_update_cache SET version=$1,error='',checked_at=now()+interval '1 second';`, version); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE notification_rule_states SET state=jsonb_set(state,'{last}',to_jsonb(now()-interval '1 second')) WHERE rule_id='update'`); err != nil {
			t.Fatal(err)
		}
		if err := s.SetNotificationUpdateComparison(ctx, version, true); err != nil {
			t.Fatal(err)
		}
		forceNotificationScan(t, ctx, s)
	}
	rows, err := s.NotificationDeliveries(ctx)
	if err != nil || len(rows) != 2 || rows[0].Event.Version == rows[1].Event.Version {
		t.Fatalf("full-version dedupe %+v %v", rows, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE notification_update_cache SET checked_at=now()-interval '2 minutes',next_at='epoch'`); err != nil {
		t.Fatal(err)
	}
	result, err := s.RefreshNotificationUpdate(ctx, true, func(context.Context) (string, time.Time, error) { return "", time.Time{}, context.DeadlineExceeded })
	if err != nil || result.Error == "" || result.Version != strings.Repeat("a", 39)+"2" {
		t.Fatalf("upstream failure lost context %+v %v", result, err)
	}
	forceNotificationScan(t, ctx, s)
	rows, err = s.NotificationDeliveries(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("upstream failure notified %+v %v", rows, err)
	}
}

func TestNotificationRateSamplesRejectStaleInitializationAndForgedTime(t *testing.T) {
	now := time.Now().UTC()
	valid := true
	for _, age := range []time.Duration{0, 3 * time.Minute, -time.Minute} {
		in := &core.HostMetrics{CollectedAt: now.Add(-age), NetworkSampledAt: now.Add(time.Hour), NetworkAvailable: true, NetworkRateAvailable: &valid}
		raw, err := encodeHeartbeatMetrics(in, now)
		if err != nil {
			t.Fatal(err)
		}
		var got core.HostMetrics
		if err = json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if age == 0 {
			if !got.NetworkSampledAt.Equal(now) || !*got.NetworkRateAvailable {
				t.Fatal("valid sample lost")
			}
		} else if !got.NetworkSampledAt.IsZero() || *got.NetworkRateAvailable {
			t.Fatal("stale/future sample trusted")
		}
	}
	raw, err := encodeHeartbeatMetrics(&core.HostMetrics{CollectedAt: now, NetworkAvailable: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	var legacy core.HostMetrics
	if err = json.Unmarshal(raw, &legacy); err != nil || legacy.NetworkRateAvailable != nil || !legacy.NetworkSampledAt.IsZero() {
		t.Fatal("legacy initialization became a valid zero", err)
	}
}

func TestNotificationTokenRevocationCancelsQueuedTest(t *testing.T) {
	ctx, s := optionalTrafficTestStore(t)
	owner := "token_notification_fixture"
	scoped := WithConfigScope(ctx, owner, false)
	permissions := []core.Permission{core.PermissionSettingsRead, core.PermissionSettingsManage}
	s.SetNotificationTokenPermissions(map[string][]core.Permission{owner: permissions})
	c, err := s.NotificationConfiguration(scoped)
	if err != nil {
		t.Fatal(err)
	}
	c.Channels = []notify.Channel{{ID: "hook", Name: "test", Type: "webhook", Enabled: true, Secrets: map[string]string{"url": "https://example.com"}}}
	c, err = s.SaveNotificationConfiguration(scoped, c)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.QueueNotificationTest(scoped, "hook", c.Revision)
	if err != nil {
		t.Fatal(err)
	}
	s.SetNotificationTokenPermissions(nil)
	if _, err = s.DeliverNotification(ctx, notify.NewSender(), ""); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = s.pool.QueryRow(ctx, `SELECT status FROM notification_deliveries WHERE id=$1`, id).Scan(&status); err != nil || status != "canceled" {
		t.Fatal("revoked token delivery", status, err)
	}
}

func TestNotificationPublicationOrdersCommittedSourceIDs(t *testing.T) {
	ctx, s, a, _ := notificationFixture(t)
	if _, err := s.AgentPresenceTransitions(ctx, time.Now(), 45*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET last_seen=now()-interval '2 minutes' WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	first, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	if _, err = first.Exec(ctx, `SELECT pg_advisory_xact_lock(91823411)`); err != nil {
		t.Fatal(err)
	}
	e := notify.Event{Type: notify.TypeAgentOffline, Time: time.Now().UTC(), AgentID: a.ID, Agent: a.Name}
	raw, _ := json.Marshal(e)
	if _, err = first.Exec(ctx, `INSERT INTO notification_events(source_key,owner_id,event) VALUES('delayed-first','',$1)`, raw); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.AgentPresenceTransitions(ctx, time.Now(), 45*time.Second); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("later publisher committed before first: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// A subscriber scanning while the first publication is open must not skip it.
	forceNotificationScan(t, ctx, s)
	if err = first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publisher did not resume")
	}
	forceNotificationScan(t, ctx, s)
	rows, err := s.NotificationDeliveries(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("source cursor lost a committed event: %+v %v", rows, err)
	}
}

func TestNotificationTestSendHonorsQueueCapacity(t *testing.T) {
	ctx, s, _, c := notificationFixture(t)
	if _, err := s.pool.Exec(ctx, `INSERT INTO notification_deliveries(owner_id,rule_id,channel_id,dedupe,event) SELECT '','offline','hook',n::text,'{}'::jsonb FROM generate_series(1,1000) n`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueueNotificationTest(ctx, "hook", c.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("unbounded test queue: %v", err)
	}
	if err := s.CleanNotificationRecords(ctx); err != nil {
		t.Fatal(err)
	}
}
