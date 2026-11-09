package store

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/notify"
)

func TestNotificationNodeFlagDeliveryUsesCurrentRegion(t *testing.T) {
	ctx, s, a, c := notificationFixture(t)
	var received notify.Event
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = notify.Event{}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.WriteHeader(204)
	}))
	defer endpoint.Close()
	t.Setenv("QCH_NOTIFICATION_ALLOWED_CIDRS", "127.0.0.0/8")
	c.Channels[0].Secrets["url"] = endpoint.URL
	var err error
	c, err = s.SaveNotificationConfiguration(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ manual, lookup, code, flag string }{{"SG", "US", "SG", "🇸🇬"}, {"JP", "US", "JP", "🇯🇵"}, {"", "US", "US", "🇺🇸"}, {"", "invalid", "", ""}, {"", "", "", ""}} {
		tx, er := s.pool.Begin(ctx)
		if er != nil {
			t.Fatal(er)
		}
		e := notify.AgentOfflineEvent(a)
		if er = s.queueNotification(ctx, tx, "", c, c.Rules[0], e, "region:"+tc.manual+":"+tc.lookup, a.OwnerID); er != nil {
			t.Fatal(er)
		}
		if er = tx.Commit(ctx); er != nil {
			t.Fatal(er)
		}
		// Read the latest explicit region at delivery, even after enqueue.
		if er = s.SetAgentRegionCode(ctx, a.ID, tc.manual); er != nil {
			t.Fatal(er)
		}
		lookups := 0
		resolver := func(lookupCtx context.Context, agent core.Agent) string {
			lookups++
			if _, ok := lookupCtx.Deadline(); !ok || agent.ID != a.ID {
				t.Fatal("unbounded lookup or wrong node")
			}
			check, er := s.pool.Begin(ctx)
			if er != nil {
				t.Fatal(er)
			}
			defer check.Rollback(ctx)
			var unlocked bool
			if er = check.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended(':hook',91))`).Scan(&unlocked); er != nil || !unlocked {
				t.Fatal("GeoIP lookup held the delivery transaction", er)
			}
			return tc.lookup
		}
		if ok, er := s.DeliverNotification(ctx, notify.NewSender(), "", resolver); er != nil || !ok {
			t.Fatal(ok, er)
		}
		if received.Agent != a.Name || received.RegionCode != tc.code || !strings.HasPrefix(received.Message, strings.TrimSpace(tc.flag+" "+a.Name)+"\n") || tc.manual != "" && lookups != 0 {
			t.Fatal("wrong region, raw name or manual precedence", tc, received, lookups)
		}
		if _, er := s.pool.Exec(ctx, `UPDATE notification_deliveries SET updated_at='epoch' WHERE status='accepted'`); er != nil {
			t.Fatal(er)
		}
	}
}

func TestNotificationNodeRegionRespectsMetricsAndSharedScope(t *testing.T) {
	s, ctx, _ := isolatedConfigScopeStore(t)
	ownerUser, owner := sharedTestUser(t, s, ctx, "flag-owner")
	sharedUser, _ := sharedTestUser(t, s, ctx, "flag-shared")
	adminUser, _ := sharedTestUser(t, s, ctx, "flag-admin")
	a := sharedTestAgent(t, s, owner)
	sharedTestAllocation(t, s, ctx, sharedUser.ID, a.ID, 1000, 21001)
	if _, err := s.pool.Exec(ctx, `UPDATE panel_users SET permissions='["settings.read","agents.read","metrics.read"]'::jsonb WHERE id=ANY($1::text[])`, []string{ownerUser.ID, sharedUser.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE panel_users SET role='admin' WHERE id=$1`, adminUser.ID); err != nil {
		t.Fatal(err)
	}
	e := notify.AgentOfflineEvent(a)
	for _, tc := range []struct {
		principal string
		auto      bool
	}{{ownerUser.ID, true}, {sharedUser.ID, false}, {adminUser.ID, true}} {
		code, agent, err := s.notificationAgentRegion(ctx, s.pool, tc.principal, e, true)
		if err != nil || code != "" || (agent != nil) != tc.auto {
			t.Fatal("host region crossed a permission boundary", tc, code, agent, err)
		}
	}
	if _, err := s.pool.Exec(ctx, `UPDATE panel_users SET permissions='["settings.read","agents.read"]'::jsonb WHERE id=$1`, ownerUser.ID); err != nil {
		t.Fatal(err)
	}
	if _, agent, err := s.notificationAgentRegion(ctx, s.pool, ownerUser.ID, e, true); err != nil || agent != nil {
		t.Fatal("metrics permission was bypassed", agent, err)
	}
	if err := s.SetAgentRegionCode(owner, a.ID, "HK"); err != nil {
		t.Fatal(err)
	}
	if code, agent, err := s.notificationAgentRegion(ctx, s.pool, sharedUser.ID, e, true); err != nil || code != "HK" || agent != nil {
		t.Fatal("shared display region lost or host metrics exposed", code, agent, err)
	}
}
