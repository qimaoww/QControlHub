package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionRetentionSettingsAPI(t *testing.T) {
	_, _, _, alice, bob := newConfigScopeAPIFixture(t)
	var settings core.PanelSettings
	alice.call("GET", "/settings", nil, http.StatusOK, &settings)
	if settings.ClientConnectionRetentionDays != 30 {
		t.Fatalf("default: %+v", settings)
	}
	settings.ClientConnectionRetentionDays = 45
	alice.call("PUT", "/settings", settings, http.StatusOK, &settings)
	if settings.ClientConnectionRetentionDays != 45 {
		t.Fatalf("save: %+v", settings)
	}
	// Older clients cannot reset retention to permanent just by omitting the field.
	payload, _ := json.Marshal(settings)
	var legacy map[string]any
	if err := json.Unmarshal(payload, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "client_connection_retention_days")
	alice.call("PUT", "/settings", legacy, http.StatusOK, &settings)
	if settings.ClientConnectionRetentionDays != 45 {
		t.Fatalf("legacy reset: %+v", settings)
	}
	for _, days := range []int{-1, 3651} {
		invalid := settings
		invalid.ClientConnectionRetentionDays = days
		alice.call("PUT", "/settings", invalid, http.StatusBadRequest, nil)
	}
	settings.ClientConnectionRetentionDays = 0
	alice.call("PUT", "/settings", settings, http.StatusOK, &settings)
	alice.call("GET", "/settings", nil, http.StatusOK, &settings)
	if settings.ClientConnectionRetentionDays != 0 {
		t.Fatalf("permanent: %+v", settings)
	}
	bob.call("GET", "/settings", nil, http.StatusOK, &settings)
	if settings.ClientConnectionRetentionDays != 30 {
		t.Fatalf("account policy leaked: %+v", settings)
	}
}
