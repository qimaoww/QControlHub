package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSiteBrandingPublicMinimalAndAdministratorOnly(t *testing.T) {
	_, _, admin, alice, bob := newConfigScopeAPIFixture(t)
	public := func() map[string]any {
		response := httptest.NewRecorder()
		admin.handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/branding", nil))
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("public branding unavailable")
		}
		var result map[string]any
		json.Unmarshal(response.Body.Bytes(), &result)
		if len(result) != 1 {
			t.Fatal("public branding exposed settings")
		}
		return result
	}
	if public()["logo"] != "" {
		t.Fatal("default logo missing")
	}
	var data bytes.Buffer
	png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 20, 5)))
	logo := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes())
	var saved core.SiteBranding
	admin.call("PUT", "/settings/branding", map[string]any{"logo": logo, "revision": 1}, 200, &saved)
	for _, client := range []configScopeAPIClient{alice, bob} {
		client.call("GET", "/settings/branding", nil, 403, nil)
		client.call("PUT", "/settings/branding", map[string]any{"logo": "", "revision": saved.Revision}, 403, nil)
		settings := core.DefaultPanelSettings()
		settings.PanelName = "personal"
		client.call("PUT", "/settings", settings, 200, nil)
	}
	if public()["logo"] != logo {
		t.Fatal("personal settings overwrote global brand")
	}
	admin.call("PUT", "/settings/branding", map[string]any{"logo": "", "revision": 1}, 409, nil)
	admin.call("PUT", "/settings/branding", map[string]any{"revision": saved.Revision}, 200, &saved)
	if saved.Logo != logo {
		t.Fatal("omitted logo reset stored value")
	}
	admin.call("PUT", "/settings/branding", map[string]any{"logo": "data:image/png;base64,YnJva2Vu", "revision": saved.Revision}, 400, nil)
	if public()["logo"] != logo {
		t.Fatal("failed save changed logo")
	}
	admin.call("PUT", "/settings/branding", map[string]any{"logo": "", "revision": saved.Revision}, 200, &saved)
	if public()["logo"] != "" {
		t.Fatal("restore default failed")
	}
	response := httptest.NewRecorder()
	admin.handler.ServeHTTP(response, httptest.NewRequest("PUT", "/api/v1/settings/branding", nil))
	if response.Code != 401 {
		t.Fatal("anonymous branding write accepted")
	}
}
