package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/komari"
)

func TestKomariNodeResourceIncludesCurrentPeriodUsage(t *testing.T) {
	resource := komariNodeResource(komari.Node{
		UUID:            "komari-node",
		TrafficLimit:    10 << 30,
		TrafficResetDay: 27,
		TrafficUsed:     4 << 30,
		TrafficUsedSet:  true,
	})
	if resource.UUID != "komari-node" || resource.TrafficResetDay != 27 || resource.TrafficUsed != 4<<30 || !resource.TrafficUsedAvailable {
		t.Fatalf("Komari API resource = %+v", resource)
	}
}

func TestDecodeJSONAcceptsMaximumConfigAfterEscapingExpansion(t *testing.T) {
	t.Parallel()
	input := core.Config{
		Name: "maximum", Engine: core.EngineMihomo,
		Content: strings.Repeat("<", core.MaxConfigBytes),
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= core.MaxConfigBytes+64<<10 {
		t.Fatalf("fixture did not exercise JSON escaping expansion: %d bytes", len(payload))
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/configs", bytes.NewReader(payload))
	response := httptest.NewRecorder()
	var decoded core.Config
	if err := decodeJSON(response, request, &decoded, core.MaxConfigEnvelopeBytes); err != nil {
		t.Fatalf("decode maximum configuration: %v", err)
	}
	if len(decoded.Content) != core.MaxConfigBytes {
		t.Fatalf("decoded content length = %d", len(decoded.Content))
	}
}

func TestEveryProxyAcceptsMaximumConfigEnvelope(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"../../frontend/nginx.conf", "../../deploy/nginx/qcontrolhub.conf"} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		match := regexp.MustCompile(`(?m)^\s*client_max_body_size\s+(\d+)([kKmMgG]?)\s*;`).FindSubmatch(contents)
		if len(match) != 3 {
			t.Fatalf("%s does not set client_max_body_size", path)
		}
		amount, err := strconv.ParseInt(string(match[1]), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		switch strings.ToLower(string(match[2])) {
		case "k":
			amount *= 1 << 10
		case "m":
			amount *= 1 << 20
		case "g":
			amount *= 1 << 30
		}
		if amount < core.MaxConfigEnvelopeBytes {
			t.Fatalf("%s body limit = %d bytes, smaller than maximum configuration envelope %d", path, amount, core.MaxConfigEnvelopeBytes)
		}
	}
}

func TestPanelSettingsResponseMasksKomariAPIKey(t *testing.T) {
	settings := core.DefaultPanelSettings()
	settings.KomariAPIKey = "secret-komari-key"
	response := panelSettingsResponse(settings)
	if response.KomariAPIKey == settings.KomariAPIKey || response.KomariAPIKey == "" {
		t.Fatalf("Komari API key was not masked: %q", response.KomariAPIKey)
	}
}

func TestPanelSettingsRequestDecodesKomariOverrides(t *testing.T) {
	var input struct {
		core.PanelSettings
		KomariURL         *string `json:"komari_url"`
		KomariAPIKey      *string `json:"komari_api_key"`
		ClearKomariAPIKey bool    `json:"clear_komari_api_key"`
	}
	if err := json.Unmarshal([]byte(`{"komari_url":"https://komari.example","komari_api_key":"key","clear_komari_api_key":true}`), &input); err != nil {
		t.Fatal(err)
	}
	if input.KomariURL == nil || *input.KomariURL != "https://komari.example" || input.KomariAPIKey == nil || *input.KomariAPIKey != "key" || !input.ClearKomariAPIKey {
		t.Fatalf("decoded Komari settings = %+v, url=%v, key=%v, clear=%v", input.PanelSettings, input.KomariURL, input.KomariAPIKey, input.ClearKomariAPIKey)
	}
}

func TestWebFrontendDropsRootQueryString(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("../../frontend/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	const expected = `location = / {
      absolute_redirect off;
      if ($args != "") { return 302 /; }
      try_files /index.html =404;
    }`
	if !strings.Contains(string(contents), expected) {
		t.Fatal("web frontend must redirect the root URL without its query string")
	}
}

func TestValidateEnrollmentRejectsDatabaseOverflowFields(t *testing.T) {
	t.Parallel()
	for _, input := range []core.EnrollRequest{
		{Name: strings.Repeat("节", 101), OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineMihomo}},
		{Name: "agent", Version: strings.Repeat("v", 101), OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineMihomo}},
		{Name: "agent", OS: strings.Repeat("o", 51), Arch: "amd64", Capabilities: []core.Engine{core.EngineMihomo}},
	} {
		if err := validateEnrollment(input); err == nil {
			t.Fatalf("validateEnrollment(%+v) succeeded", input)
		}
	}
}

func TestAgentDownloadsRequireEnrollmentToken(t *testing.T) {
	handler := New(nil, Config{
		AdminToken:     strings.Repeat("a", 48),
		AgentBinary:    []byte("binary"),
		AgentInstaller: []byte("#!/bin/sh\n"),
	}).Handler()
	for _, path := range []string{"/api/v1/agent-installer", "/api/v1/agent-binary"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s without enrollment token = %d, want %d", path, response.Code, http.StatusUnauthorized)
		}
	}
}

func TestConfigRevisionRoutesRejectInvalidRequestsBeforeStoreAccess(t *testing.T) {
	t.Parallel()
	adminToken := strings.Repeat("a", 48)
	handler := New(nil, Config{AdminToken: adminToken}).Handler()
	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "zero list limit", method: http.MethodGet, target: "/api/v1/configs/cfg_test/revisions?limit=0"},
		{name: "oversized list limit", method: http.MethodGet, target: "/api/v1/configs/cfg_test/revisions?limit=101"},
		{name: "invalid list limit", method: http.MethodGet, target: "/api/v1/configs/cfg_test/revisions?limit=many"},
		{name: "zero revision", method: http.MethodGet, target: "/api/v1/configs/cfg_test/revisions/0"},
		{name: "invalid revision", method: http.MethodGet, target: "/api/v1/configs/cfg_test/revisions/latest"},
		{name: "missing expected version", method: http.MethodPost, target: "/api/v1/configs/cfg_test/revisions/1/restore", body: `{}`},
		{name: "unknown restore field", method: http.MethodPost, target: "/api/v1/configs/cfg_test/revisions/1/restore", body: `{"expected_version":1,"force":true}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+adminToken)
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			var failure map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil || failure["error"] == "" {
				t.Fatalf("invalid error response %q: %v", response.Body.String(), err)
			}
		})
	}
}
