package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAPIKeySettingsResponseRedactsWebhookURL(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &apiKeyResponseWriter{ResponseWriter: recorder, secret: "qch_key_probe"}
	writeJSON(writer, 200, panelSettingsResponse(core.PanelSettings{
		WebhookURL:   "https://hooks.slack.com/services/T000/B000/WEBHOOKSECRET",
		KomariAPIKey: "komari-secret",
	}))
	writer.finish()
	body := recorder.Body.String()
	for _, secret := range []string{"WEBHOOKSECRET", "komari-secret"} {
		if strings.Contains(body, secret) {
			t.Fatalf("API key response leaked %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, `"webhook_url":"[redacted]"`) {
		t.Fatalf("webhook URL was not redacted as a credential: %s", body)
	}
}
