package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNotificationAdapters(t *testing.T) {
	for _, kind := range []string{"webhook", "telegram", "bark", "ntfy", "gotify"} {
		t.Run(kind, func(t *testing.T) {
			c := Channel{ID: "fixture", Type: kind, Enabled: true, Values: map[string]string{"chat_id": "-100123", "topic": "fixture", "message_thread_id": "12"}, Secrets: map[string]string{"url": "https://example.com/receiver", "token": "fixture-token", "device_key": "fixture-device", "secret": "fixture-signing", "authorization": "Bearer fixture-auth"}}
			event := Sample(TypeTaskFailed)
			event.Title = "中文标题 <&>"
			event.Message = strings.Repeat("告警😀\n", 3000)
			sender := &Sender{HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				var p map[string]any
				if json.Unmarshal(raw, &p) != nil {
					t.Fatal("invalid JSON")
				}
				if !utf8.Valid(raw) {
					t.Fatal("invalid UTF-8")
				}
				switch kind {
				case "webhook":
					if p["type"] != TypeTaskFailed || p["task_id"] != event.TaskID || p["message"] != event.Message || p["title"] != event.Title {
						t.Fatal("legacy fields lost")
					}
					mac := hmac.New(sha256.New, []byte(c.Get("secret")))
					mac.Write(raw)
					if r.Header.Get("X-QControlHub-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) || r.Header.Get("Authorization") != "Bearer fixture-auth" {
						t.Fatal("missing auth/signature")
					}
				case "telegram":
					if r.URL.Host != "api.telegram.org" || p["chat_id"] != "-100123" || p["message_thread_id"] != float64(12) || len(p["text"].(string)) > 4000 || p["parse_mode"] != nil {
						t.Fatal("Telegram mapping")
					}
				case "bark":
					if !strings.HasSuffix(r.URL.Path, "/push") || p["device_key"] != "fixture-device" || len(p["body"].(string)) > 3000 {
						t.Fatal("Bark mapping")
					}
				case "ntfy":
					if p["topic"] != "fixture" || r.Header.Get("Authorization") != "Bearer fixture-token" || len(p["message"].(string)) > 3500 {
						t.Fatal("ntfy mapping")
					}
				case "gotify":
					if !strings.HasSuffix(r.URL.Path, "/message") || r.Header.Get("X-Gotify-Key") != "fixture-token" {
						t.Fatal("Gotify mapping")
					}
				}
				success := map[string]string{"webhook": "", "telegram": `{"ok":true}`, "bark": `{"code":200}`, "ntfy": `{"id":"example","event":"message"}`, "gotify": `{"id":123}`}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(success[kind])), Header: http.Header{}}, nil
			})}}
			if err := sender.Send(context.Background(), c, event); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestNotificationPlatformFailures(t *testing.T) {
	cases := []struct {
		kind   string
		status int
		body   string
		retry  bool
	}{{"telegram", 429, `{"ok":false,"parameters":{"retry_after":45}}`, true}, {"telegram", 200, `{"ok":false,"error_code":429,"parameters":{"retry_after":45}}`, true}, {"gotify", 401, `secret`, false}, {"webhook", 429, `secret`, true}, {"webhook", 503, `secret`, true}, {"bark", 200, `{"code":400}`, false}, {"ntfy", 200, `{}`, false}, {"telegram", 200, `not json`, false}, {"webhook", 200, strings.Repeat("a", 65537), false}}
	for _, v := range cases {
		t.Run(v.kind+v.body[:min(8, len(v.body))], func(t *testing.T) {
			s := &Sender{HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: v.status, Body: io.NopCloser(strings.NewReader(v.body)), Header: http.Header{"Retry-After": []string{"30"}}}, nil
			})}}
			err := s.Send(context.Background(), Channel{Type: v.kind, Secrets: map[string]string{"url": "https://example.com"}}, Sample(TypeTaskFailed))
			var de *DeliveryError
			if v.kind == "telegram" && v.status == 429 && (!errors.As(err, &de) || de.After != 45*time.Second) {
				t.Fatalf("lost Telegram backoff: %v", err)
			}
			if !errors.As(err, &de) || de.Retry != v.retry || strings.Contains(err.Error(), "secret") {
				t.Fatalf("result %v", err)
			}
		})
	}
}
func TestNotificationNetworkPolicyAndCancellation(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("default policy accessed loopback") }))
	defer target.Close()
	err := NewSender().Send(context.Background(), Channel{Type: "webhook", Secrets: map[string]string{"url": target.URL}}, Sample(TypeAgentOnline))
	if err == nil {
		t.Fatal("loopback allowed")
	}
	t.Setenv("QCH_NOTIFICATION_ALLOWED_CIDRS", "127.0.0.0/8")
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	if err := NewSender().Send(context.Background(), Channel{Type: "webhook", Secrets: map[string]string{"url": redirect.URL}}, Sample(TypeAgentOnline)); err == nil {
		t.Fatal("redirect allowed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if NewSender().Send(ctx, Channel{Type: "webhook", Secrets: map[string]string{"url": target.URL}}, Sample(TypeAgentOnline)) == nil {
		t.Fatal("ignored cancel")
	}
}
func TestNotificationTemplatesAndConfiguration(t *testing.T) {
	for _, body := range []string{"{{env}}", "{{agent.name}}", "{{agent}", "{{exec}}", "{{.Agent}}", "{{ agent }}"} {
		if ValidateTemplate(Template{Title: "test", Body: body}) == nil {
			t.Fatal("accepted unsafe template", body)
		}
	}
	e := Sample(TypeTaskFailed)
	e.Error = `password="private-value" Bearer fixture-secret https://example.com/?token=private`
	e.Agent = "<script>&中文😀"
	e.LimitBytes = 0
	title, body, err := Render(Template{Title: "{{agent}}", Body: "{{error}} / {{limit_bytes}} / {{version}}"}, e, "面板")
	if err != nil || title != e.Agent || strings.Contains(body, "private") || strings.Contains(body, "fixture-secret") || strings.Contains(body, "{{") {
		t.Fatalf("render %s %s %v", title, body, err)
	}
	for n := 3; n < 200; n++ {
		if !utf8.ValidString(Clip(strings.Repeat("中文😀", 30), n)) {
			t.Fatal("UTF-8 split")
		}
	}
	c := Configuration{Channels: []Channel{{ID: "one", Name: "test", Type: "telegram", Enabled: true, Values: map[string]string{"chat_id": "-1"}, Secrets: map[string]string{"token": "private"}}}, Rules: []Rule{{ID: "r", Name: "test", Event: "speed.low", AgentID: "a", Channels: []string{"one"}, Duration: 30, Cooldown: 60, Threshold: 100, Recovery: 200, Direction: "upload"}}}
	if err = c.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(c.Redacted())
	if strings.Contains(string(raw), "private") || c.Channels[0].Secrets["token"] != "private" {
		t.Fatal("redaction mutates original or leaks")
	}
	c.Rules[0].Recovery = 50
	if c.Validate() == nil {
		t.Fatal("accepted invalid hysteresis")
	}
}
