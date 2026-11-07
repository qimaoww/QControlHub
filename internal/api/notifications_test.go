package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/notify"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestNotificationAPIConfigurationOwnershipPreviewAndTests(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	a, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "通知节点")
	var calls int
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }))
	defer endpoint.Close()
	t.Setenv("QCH_NOTIFICATION_ALLOWED_CIDRS", "127.0.0.0/8")
	for _, client := range []configScopeAPIClient{alice, bob} {
		var loaded struct {
			Configuration notify.Configuration `json:"configuration"`
		}
		client.call("GET", "/settings/notifications", nil, 200, &loaded)
		c := loaded.Configuration
		c.Channels = []notify.Channel{{ID: "hook", Name: "通知目标", Type: "webhook", Enabled: true, Values: map[string]string{}, Secrets: map[string]string{"url": endpoint.URL, "secret": "notification-private-fixture"}}}
		c.Rules = []notify.Rule{{ID: "offline", Name: "离线提醒", Event: notify.TypeAgentOffline, Enabled: true, Channels: []string{"hook"}, Duration: 30, Cooldown: 60}}
		payload := c
		c = notify.Configuration{}
		client.call("PUT", "/settings/notifications", payload, 200, &c)
		raw, _ := json.Marshal(c)
		if strings.Contains(string(raw), endpoint.URL) || strings.Contains(string(raw), "notification-private-fixture") {
			t.Fatal("API disclosed secrets")
		}
		client.call("POST", "/settings/notifications/test", map[string]any{"channel_id": "hook", "revision": c.Revision - 1}, 409, nil)
		if client.userID == alice.userID {
			client.call("POST", "/settings/notifications/test", map[string]any{"channel_id": "hook", "revision": c.Revision}, 202, nil)
			if _, err := db.DeliverNotification(ctx, notify.NewSender(), ""); err != nil {
				t.Fatal(err)
			}
			client.call("POST", "/settings/notifications/preview", map[string]any{"event": "agent.offline", "template": notify.Template{Title: "{{agent}}", Body: "{{message}}\n{{error}}"}}, 200, nil)
			client.call("POST", "/settings/notifications/preview", map[string]any{"event": "agent.offline", "template": notify.Template{Title: "{{env}}", Body: "no"}}, 400, nil)
		} else {
			c.Rules[0].Event = "speed.high"
			c.Rules[0].AgentID = a.ID
			c.Rules[0].Threshold = 100
			c.Rules[0].Recovery = 50
			c.Rules[0].Direction = "upload"
			client.call("PUT", "/settings/notifications", c, 404, nil)
		}
	}
	var deliveries []store.NotificationDelivery
	bob.call("GET", "/settings/notifications/deliveries", nil, 200, &deliveries)
	if len(deliveries) != 0 {
		t.Fatal("cross-account delivery exposed")
	}
	alice.call("GET", "/settings/notifications/deliveries", nil, 200, &deliveries)
	if len(deliveries) != 1 || deliveries[0].Status != "accepted" || calls != 1 {
		t.Fatalf("test delivery %+v calls %d", deliveries, calls)
	}
	if _, err := db.AgentPresenceTransitions(ctx, time.Now(), 45*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AgentPresenceTransitions(ctx, time.Now().Add(2*time.Minute), 45*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := db.ScanNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	alice.call("GET", "/settings/notifications/deliveries", nil, 200, &deliveries)
	if len(deliveries) != 2 || deliveries[0].Event.Type != notify.TypeAgentOffline {
		t.Fatalf("real offline event %+v", deliveries)
	}
	bob.call("GET", "/settings/notifications/deliveries", nil, 200, &deliveries)
	if len(deliveries) != 0 {
		t.Fatal("other user's node event exposed")
	}
}
