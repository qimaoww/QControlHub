package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/notify"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestNotificationRegionUsesExistingCountryCache(t *testing.T) {
	calls := 0
	server := New(nil, Config{GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return regionTestResponse(http.StatusOK, `{"country_code":"SG","country":"Singapore"}`), nil
	})}})
	agent := core.Agent{Metrics: core.HostMetrics{PublicIPv4: "8.8.8.8"}}
	for range 2 {
		if code := server.notificationRegion(context.Background(), agent); code != "SG" {
			t.Fatal("existing GeoIP country result lost", code)
		}
	}
	if calls != 1 {
		t.Fatal("notification region did not reuse GeoIP cache", calls)
	}
	agent.Metrics.PublicIPv4 = "192.168.1.1"
	if code := server.notificationRegion(context.Background(), agent); code != "" || calls != 1 {
		t.Fatal("private address generated a fake flag or external lookup", code, calls)
	}
}

func TestNotificationAPIConfigurationOwnershipPreviewAndTests(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	a, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "通知节点")
	var calls int
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }))
	defer endpoint.Close()
	t.Setenv("QCH_NOTIFICATION_ALLOWED_CIDRS", "127.0.0.0/8")
	for _, client := range []configScopeAPIClient{alice, bob} {
		var loaded struct {
			Configuration            notify.Configuration `json:"configuration"`
			WebhookSigningConfigured *bool                `json:"webhook_signing_configured"`
		}
		client.call("GET", "/settings/notifications", nil, 200, &loaded)
		if loaded.WebhookSigningConfigured == nil {
			t.Fatal("notification page cannot read the legacy Webhook signing status")
		}
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

func TestNotificationAPISavedTemplateTest(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	var received notify.Event
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.WriteHeader(204)
	}))
	defer endpoint.Close()
	t.Setenv("QCH_NOTIFICATION_ALLOWED_CIDRS", "127.0.0.0/8")
	var loaded struct {
		Configuration notify.Configuration `json:"configuration"`
	}
	alice.call("GET", "/settings/notifications", nil, 200, &loaded)
	c := loaded.Configuration
	c.Channels = []notify.Channel{{ID: "hook", Name: "自己的目标", Type: "webhook", Enabled: true, Secrets: map[string]string{"url": endpoint.URL}}}
	c.Templates[notify.TypeTaskFailed] = notify.Template{Title: "任务测试 {{agent}}", Body: "{{error}}\n中文 <>& 😀\n{{version}}"}
	alice.call("PUT", "/settings/notifications", c, 200, &c)
	for event := range notify.EventNames {
		var preview notify.Rendered
		alice.call("POST", "/settings/notifications/preview", map[string]any{"event": event, "template": notify.DefaultTemplate(event)}, 200, &preview)
		if preview.Title == "" || preview.Body == "" || strings.Contains(preview.Body, "{{") {
			t.Fatal("invalid preview", event, preview)
		}
		if event != "panel.update" && !strings.Contains(preview.Body, "🇸🇬 示例节点") {
			t.Fatal("node flag missing from API preview", event, preview)
		}
	}
	alice.call("POST", "/settings/notifications/test", map[string]any{"channel_id": "hook", "revision": c.Revision, "event": "unknown"}, 400, nil)
	alice.call("POST", "/settings/notifications/test", map[string]any{"channel_id": "hook", "revision": c.Revision - 1, "event": notify.TypeTaskFailed}, 409, nil)
	bob.call("POST", "/settings/notifications/test", map[string]any{"channel_id": "hook", "revision": c.Revision, "event": notify.TypeTaskFailed}, 409, nil)
	alice.call("POST", "/settings/notifications/test", map[string]any{"channel_id": "hook", "revision": c.Revision, "event": notify.TypeTaskFailed}, 202, nil)
	if ok, err := db.DeliverNotification(ctx, notify.NewSender(), ""); err != nil || !ok {
		t.Fatalf("delivery %t %v", ok, err)
	}
	if !received.Test || received.Type != notify.TypeTaskFailed || !strings.Contains(received.Title, "任务测试 示例节点") || !strings.Contains(received.Message, "示例错误（已脱敏）\n中文 <>& 😀\n—") {
		t.Fatalf("custom template was not delivered: %+v", received)
	}
	var records []store.NotificationDelivery
	bob.call("GET", "/settings/notifications/deliveries", nil, 200, &records)
	if len(records) != 0 {
		t.Fatal("template test leaked to another account")
	}
	alice.call("GET", "/settings/notifications/deliveries", nil, 200, &records)
	if len(records) != 1 || records[0].Status != "accepted" || !records[0].Event.Test || records[0].Event.Type != notify.TypeTaskFailed {
		t.Fatal("template test record missing", records)
	}
}
