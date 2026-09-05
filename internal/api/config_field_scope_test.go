package api

import (
	"net/http/httptest"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestConfigFieldInboundNeverFallsBackToGlobal(t *testing.T) {
	for _, query := range []string{"?inbound=", "?inbound=+", "?inbound=one&inbound=two"} {
		_, scoped, err := configFieldInbound(httptest.NewRequest("GET", "/"+query, nil), core.EngineShadowsocksRust)
		if err == nil || !scoped {
			t.Errorf("accepted ambiguous scope %s", query)
		}
	}
	if _, _, err := configFieldInbound(httptest.NewRequest("GET", "/?inbound=one", nil), core.EngineXray); err == nil {
		t.Fatal("accepted unsupported engine scope")
	}
	inbound, scoped, err := configFieldInbound(httptest.NewRequest("GET", "/?inbound=HK%20%26%20JP", nil), core.EngineShadowsocksRust)
	if err != nil || !scoped || inbound != "HK & JP" {
		t.Fatalf("encoded scope: %q %v %v", inbound, scoped, err)
	}
	_, scoped, err = configFieldInbound(httptest.NewRequest("GET", "/", nil), core.EngineShadowsocksRust)
	if err != nil || scoped {
		t.Fatal("global editor must stay root scoped")
	}
}
