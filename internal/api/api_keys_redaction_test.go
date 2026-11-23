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
)

func TestAPIKeySettingsResponseRedactsWebhookURL(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &apiKeyResponseWriter{ResponseWriter: recorder, secret: "qch_key_probe"}
	writeJSON(writer, 200, panelSettingsResponse(core.PanelSettings{
		WebhookURL: "https://hooks.slack.com/services/T000/B000/WEBHOOKSECRET",
	}))
	writer.finish()
	body := recorder.Body.String()
	for _, secret := range []string{"WEBHOOKSECRET"} {
		if strings.Contains(body, secret) {
			t.Fatalf("API key response leaked %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, `"webhook_url":"[redacted]"`) {
		t.Fatalf("webhook URL was not redacted as a credential: %s", body)
	}
}

func TestAPIKeyResponseReadDeadline(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		cancel bool
	}{
		{"expired success", http.StatusOK, false},
		{"expired forbidden", http.StatusForbidden, false},
		{"canceled success", http.StatusOK, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			if test.cancel {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			}
			defer cancel()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/core-logs", nil).WithContext(ctx)
			recorder := httptest.NewRecorder()
			writer := &apiKeyResponseWriter{ResponseWriter: recorder, request: request, secret: "fixture-secret"}
			writer.Header().Set("ETag", `W/"original"`)
			writeJSON(writer, test.status, map[string]string{"error": "password=fixture-secret"})
			status := writer.finish()
			want := http.StatusGatewayTimeout
			if test.status >= 400 {
				want = test.status
			} else if test.cancel {
				want = 499
			}
			if status != want || recorder.Code != want || !json.Valid(recorder.Body.Bytes()) {
				t.Fatalf("status=%d/%d body=%s", status, recorder.Code, recorder.Body)
			}
			if writer.secret != "" || writer.body.Len() != 0 || recorder.Header().Get("ETag") != "" || strings.Contains(recorder.Body.String(), "fixture-secret") {
				t.Fatal("expired read retained credentials or its original representation")
			}
		})
	}
}

func TestAPIKeyResponseDeadlineDuringFinish(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/core-logs", nil)
	recorder := httptest.NewRecorder()
	writer := &apiKeyResponseWriter{ResponseWriter: recorder, request: request, secret: "fixture-secret"}
	// A valid full window containing credential markers must still stop if
	// the deadline expires during response processing.
	entries := make([]map[string]string, 8000)
	for i := range entries {
		entries[i] = map[string]string{"message": strings.Repeat("secret=fixture-secret diagnostic ", 120)}
	}
	writeJSON(writer, http.StatusOK, entries)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	writer.request = request.WithContext(ctx)
	started := time.Now()
	status := writer.finish()
	if status != http.StatusGatewayTimeout || time.Since(started) > 2*time.Second {
		t.Fatalf("redaction did not stop: status=%d elapsed=%s", status, time.Since(started))
	}
	if strings.Contains(recorder.Body.String(), "fixture-secret") || !strings.Contains(recorder.Body.String(), `"code":"read_timeout"`) || writer.body.Len() != 0 {
		t.Fatal("expired redaction emitted or retained partial data")
	}
}
