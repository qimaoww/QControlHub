package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestArchiveExampleDefaultsRoundTrip(t *testing.T) {
	_, _, _, alice, bob := newConfigScopeAPIFixture(t)
	for _, example := range []struct {
		engine core.Engine
		file   string
	}{
		{core.EngineMihomo, "mihomo-minimal.yaml"},
		{core.EngineXray, "xray-minimal.json"},
		{core.EngineSingBox, "sing-box-minimal.json"},
		{core.EngineShadowsocksRust, "shadowsocks-rust-minimal.json"},
	} {
		t.Run(string(example.engine), func(t *testing.T) {
			alice, bob := alice, bob
			alice.t, bob.t = t, t
			content, err := os.ReadFile(filepath.Join("..", "..", "examples", "configs", example.file))
			if err != nil {
				t.Fatal(err)
			}
			var saved core.Config
			alice.call("POST", "/configs", core.Config{Name: string(example.engine), Description: "default example",
				Engine: example.engine, Content: string(content)}, http.StatusCreated, &saved)
			if saved.Engine != example.engine || saved.Content != string(content) || saved.Version != 1 {
				t.Fatalf("created config differs from the selected engine/default: %+v", saved)
			}
			var items []core.Config
			alice.call("GET", "/configs", nil, http.StatusOK, &items)
			found := false
			for _, item := range items {
				if item.ID == saved.ID {
					found = true
					if item.Engine != saved.Engine || item.Content != saved.Content || item.Version != 1 || item.Description != saved.Description {
						t.Fatalf("reopened archive differs: %+v", item)
					}
				}
			}
			if !found {
				t.Fatal("created archive missing from list")
			}
			var revision core.Config
			alice.call("GET", "/configs/"+saved.ID+"/revisions/1", nil, http.StatusOK, &revision)
			if revision.Engine != saved.Engine || revision.Content != saved.Content || revision.Version != 1 {
				t.Fatalf("revision differs: %+v", revision)
			}
			bob.call("GET", "/configs/"+saved.ID+"/revisions/1", nil, http.StatusNotFound, nil)
			var template core.ConfigTemplate
			alice.call("POST", "/templates", map[string]any{"name": string(example.engine), "engine": example.engine,
				"content": string(content)}, http.StatusCreated, &template)
			if template.Engine != example.engine || template.Content != strings.TrimSpace(string(content)) {
				t.Fatalf("template differs from the selected engine/default: %+v", template)
			}
		})
	}
}
