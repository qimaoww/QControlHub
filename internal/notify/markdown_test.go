package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

func TestNotificationMarkdownLiteralVariablesAndTelegramEntities(t *testing.T) {
	e := Sample(TypeTaskFailed)
	e.Agent = "节点😀 **不能注入** _name_ <>&"
	e.Error = "`literal` **error**\n<script>literal</script>"
	template := Template{Title: "面板😀 {{agent}}", Format: "markdown", Body: "**节点** {{agent}}\n*斜体* ~~删除~~ `{{task_id}}`\n```text\n{{error}}\n```\n[详情](https://example.com/details)"}
	rendered, err := RenderFormatted(template, e, "panel", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Body, e.Agent) || !strings.Contains(rendered.Body, e.Error) || !strings.Contains(rendered.Body, "斜体 删除 tsk_example") || !strings.Contains(rendered.Body, "详情") {
		t.Fatalf("literal variables or markup lost: %+v", rendered)
	}
	want := []string{"bold", "bold", "italic", "strikethrough", "code", "pre", "text_link"}
	if len(rendered.Entities) != len(want) {
		t.Fatalf("markup injection changed entities: %+v", rendered.Entities)
	}
	all := utf16.Encode([]rune(rendered.Title + "\n\n" + rendered.Body))
	for i, entity := range rendered.Entities {
		if entity.Type != want[i] || entity.Offset+entity.Length > len(all) {
			t.Fatal("invalid UTF-16 entity", entity)
		}
		value := string(utf16.Decode(all[entity.Offset : entity.Offset+entity.Length]))
		if i == 1 && value != "节点" || entity.Type == "pre" && value != e.Error || entity.Type == "text_link" && (value != "详情" || entity.URL != "https://example.com/details") {
			t.Fatal("wrong entity span", entity, value)
		}
	}
	for _, kind := range []string{"telegram", "webhook"} {
		c := Channel{Type: kind, Values: map[string]string{"chat_id": "fixture"}, Secrets: map[string]string{"url": "https://example.com", "token": "fixture"}}
		e.Title, e.Message, e.Entities = rendered.Title, rendered.Body, rendered.Entities
		sender := &Sender{HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			var p map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Fatal(err)
			}
			response := ""
			if kind == "telegram" {
				if len(p["entities"]) == 0 || len(p["parse_mode"]) != 0 || !strings.Contains(string(p["link_preview_options"]), "true") {
					t.Fatal("missing native formatting or unsafe parse mode", p)
				}
				response = `{"ok":true}`
			} else if p["entities"] != nil || p["message"] == nil || p["task_id"] == nil {
				t.Fatal("Webhook compatibility lost", p)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(response))}, nil
		})}}
		if err = sender.Send(context.Background(), c, e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNotificationMarkdownLimitsAndReadableDefaults(t *testing.T) {
	for event := range EventNames {
		rendered, err := RenderFormatted(DefaultTemplate(event), Sample(event), "QControlHub", "Asia/Shanghai")
		if err != nil || len(rendered.Entities) == 0 || strings.Contains(rendered.Body, "**") || !strings.Contains(rendered.Body, "2026-01-01 20:00:00 +08:00") {
			t.Fatal("default format or timezone lost", event, rendered, err)
		}
		if strings.HasPrefix(event, "traffic.") && (!strings.Contains(rendered.Body, "GiB") || strings.Contains(rendered.Body, "107374182400")) {
			t.Fatal("raw byte counts in default", rendered)
		}
	}
	e := Sample(TypeTaskFailed)
	e.Agent = strings.Repeat("😀中文", 3000)
	r, err := RenderFormatted(Template{Title: "标题😀", Body: "**{{agent}}**", Format: "markdown"}, e, "panel", "browser")
	if err != nil {
		t.Fatal(err)
	}
	text := Clip(r.Title+"\n\n"+r.Body, 4000)
	entities := clippedEntities(r.Entities, text)
	if !utf8.ValidString(text) || len(text) > 4000 || len(entities) != 2 {
		t.Fatal("invalid long Unicode message")
	}
	for _, entity := range entities {
		if entity.Offset+entity.Length > utf16Length(strings.TrimSuffix(text, "…")) {
			t.Fatal("formatting extends past truncation", entity)
		}
	}
	plain, err := RenderFormatted(Template{Title: "{{agent}}", Body: "**literal**", Format: "text"}, Sample(TypeTaskFailed), "panel", "UTC")
	if err != nil || plain.Body != "**literal**" || len(plain.Entities) != 0 {
		t.Fatal("plain text compatibility lost")
	}
	if ValidateTemplate(Template{Title: "title", Body: "body", Format: "html"}) == nil {
		t.Fatal("accepted unknown template format")
	}
	jsonCode, err := RenderFormatted(Template{Title: "JSON", Body: "```json\n{\"ok\": true}\n```\n<script>literal</script>", Format: "markdown"}, e, "panel", "UTC")
	if err != nil || !strings.Contains(jsonCode.Body, "{\"ok\": true}") || !strings.Contains(jsonCode.Body, "<script>literal</script>") {
		t.Fatal("literal JSON or HTML changed", jsonCode, err)
	}
	text, entities = markdownText(strings.Repeat("**中文😀** ", 1000), func(s string) string { return s })
	if len(entities) > 99 || !utf8.ValidString(text) {
		t.Fatal("unbounded formatting entities")
	}
}

func TestNotificationReadableRateKeepsRawUnitsAndRecoveryMeaning(t *testing.T) {
	e := Sample("speed.recovered")
	e.Current, e.Threshold = "5000000 B/s", "8000000 B/s"
	r, err := RenderFormatted(DefaultTemplate(e.Type), e, "panel", "UTC")
	if err != nil || !strings.Contains(r.Body, "当前：5.00 MB/s") || !strings.Contains(r.Body, "恢复阈值：8.00 MB/s") {
		t.Fatal("recovery message lost key values", r, err)
	}
	_, raw, err := Render(Template{Title: "raw", Body: "{{current}} / {{threshold}}"}, e, "panel")
	if err != nil || raw != "5000000 B/s / 8000000 B/s" {
		t.Fatal("raw variables changed", raw, err)
	}
	for input, expected := range map[string]string{"0 B/s": "0 B/s", "500 B/s": "500 B/s", "1000 B/s": "1.00 KB/s", "1000000 B/s": "1.00 MB/s", "NaN B/s": "NaN B/s", "10 bit/s": "10 bit/s"} {
		if got := humanRate(input); got != expected {
			t.Fatal(input, got, expected)
		}
	}
}

func TestNotificationTrafficScopeUsesAuthorizedAllocationContext(t *testing.T) {
	for _, tc := range []struct {
		share, engine string
		port          int
		want          string
	}{{"", "xray", 443, "xray:443"}, {"", "", 443, "端口 443"}, {"share_example", "", 0, "共享分配"}, {"", "", 0, "—"}} {
		e := Sample(TypeTrafficQuota)
		e.ShareID, e.Engine, e.Port = tc.share, tc.engine, tc.port
		r, err := RenderFormatted(Template{Title: "title", Body: "{{traffic_scope}}"}, e, "panel", "UTC")
		if err != nil || r.Body != tc.want {
			t.Fatal("incorrect traffic context", tc, r, err)
		}
		e.LimitBytes = 0
		r, err = RenderFormatted(DefaultTemplate(e.Type), e, "panel", "UTC")
		if err != nil || strings.Contains(r.Body, "—:—") || strings.Contains(r.Body, "%") {
			t.Fatal("missing scope or limit produced fabricated values", r, err)
		}
	}
}

func TestNotificationNodeFlagsPreserveRawNameAndEntityOffsets(t *testing.T) {
	for _, tc := range []struct{ code, flag string }{{" sg ", "🇸🇬"}, {"US", "🇺🇸"}, {"HK", "🇭🇰"}, {"TW", "🇨🇳"}, {"ZZ", ""}, {"", ""}} {
		e := Sample(TypeTaskFailed)
		e.Agent, e.RegionCode = "节点😀 **literal**", tc.code
		r, err := RenderFormatted(Template{Title: "标题", Body: "**{{agent_display}}**\n{{agent}}\n{{agent_flag}} / {{region_code}}", Format: "markdown"}, e, "panel", "UTC")
		if err != nil {
			t.Fatal(err)
		}
		want := e.Agent
		if tc.flag != "" {
			want = tc.flag + " " + want
		}
		all := utf16.Encode([]rune(r.Title + "\n\n" + r.Body))
		span := r.Entities[1]
		if got := string(utf16.Decode(all[span.Offset : span.Offset+span.Length])); got != want || !strings.Contains(r.Body, "\n"+e.Agent+"\n") {
			t.Fatal("flag changed the raw name or entity boundaries", tc, r)
		}
		if tc.flag == "" && strings.Contains(r.Body, tc.code+" / ") && tc.code != "" {
			t.Fatal("invalid country leaked into display", r)
		}
		e.Agent = tc.flag + " 节点"
		r, err = RenderFormatted(Template{Title: "title", Body: "{{agent_display}}"}, e, "panel", "UTC")
		if err != nil || tc.flag != "" && strings.Count(r.Body, tc.flag) != 1 {
			t.Fatal("duplicate flag", r, err)
		}
	}
	for event := range EventNames {
		r, err := RenderFormatted(DefaultTemplate(event), Sample(event), "panel", "UTC")
		want := 1
		if event == "panel.update" {
			want = 0
		}
		if err != nil || strings.Count(r.Body, "🇸🇬") != want {
			t.Fatal("default node flag missing or unrelated flag added", event, r, err)
		}
	}
}
