package serverconfig

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPairedConfigFilesPreserveOrder(t *testing.T) {
	data, err := os.ReadFile("testdata/paired_config_files.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name           string          `json:"name"`
		Config         json.RawMessage `json:"config"`
		OutboundCounts []int           `json:"outbound_counts"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		for _, test := range cases {
			t.Run(string(engine)+"/"+test.Name, func(t *testing.T) {
				files, err := SplitConfigFiles(engine, string(test.Config))
				if err != nil || len(files) != len(test.OutboundCounts) {
					t.Fatalf("files: %+v %v", files, err)
				}
				for i, file := range files {
					if strings.HasPrefix(file.Path, "outbounds/") {
						t.Fatal("created standalone exit")
					}
					var fragment struct {
						Outbounds []json.RawMessage `json:"outbounds"`
					}
					if err := json.Unmarshal([]byte(file.Content), &fragment); err != nil || len(fragment.Outbounds) != test.OutboundCounts[i] {
						t.Fatalf("wrong grouping: %s %v", file.Content, err)
					}
				}
				merged, err := MergeConfigFiles(engine, files)
				if err != nil {
					t.Fatal(err)
				}
				var before, after any
				_ = json.Unmarshal(test.Config, &before)
				_ = json.Unmarshal([]byte(merged), &after)
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("round trip changed routing/default: %s", merged)
				}
			})
		}
	}
	for _, common := range []string{"common.json", "00-common.json"} {
		legacy := []ConfigFile{{Path: common, Content: `{}`}, {Path: "inbounds/0000.json", Content: `{"inbounds":[{"tag":"a"}]}`}, {Path: "outbounds/0000.json", Content: `{"outbounds":[{"tag":"direct"}]}`}}
		merged, err := MergeConfigFiles(core.EngineXray, legacy)
		if err != nil {
			t.Fatal(err)
		}
		files, err := SplitConfigFiles(core.EngineXray, merged)
		if err != nil || len(files) != 2 {
			t.Fatalf("legacy migration: %+v %v", files, err)
		}
	}
}

func TestConfigFilesRoundTrip(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		content := `{"inbounds":[{"tag":"one","port":1080},{"tag":"two","port":1081}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"large":9007199254740993,"routing":{"rules":[]}}`
		files, err := SplitConfigFiles(engine, content)
		if err != nil || len(files) != 3 {
			t.Fatalf("split: %v %v", files, err)
		}
		merged, err := MergeConfigFiles(engine, files)
		if err != nil {
			t.Fatal(err)
		}
		var before, after map[string]json.RawMessage
		_ = json.Unmarshal([]byte(content), &before)
		_ = json.Unmarshal([]byte(merged), &after)
		for key, value := range before {
			var a, b any
			_ = json.Unmarshal(value, &a)
			_ = json.Unmarshal(after[key], &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("changed %s", key)
			}
		}
		if string(after["large"]) != "9007199254740993" {
			t.Fatal("lost integer precision")
		}
	}
}

func TestConfigFilesPresetNamesAndLegacyManifest(t *testing.T) {
	content := `{"inbounds":[{"tag":"VLESS-REALITY-8443.json"},{"tag":"香港入口"},{"tag":"香港入口"},{"tag":"../a/b"},{"tag":"..."},{"tag":"` + strings.Repeat("a", 60) + `"}],"outbounds":[{"tag":"direct"}]}`
	files, err := SplitConfigFiles(core.EngineXray, content)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"common.json", "inbounds/VLESS-REALITY-8443.json", "inbounds/香港入口.json", "inbounds/香港入口-2.json", "inbounds/_a_b.json", "inbounds/inbound-5.json", "inbounds/" + strings.Repeat("a", 48) + ".json"}
	for i := range files {
		if files[i].Path != want[i] {
			t.Fatalf("file %d: %s != %s", i, files[i].Path, want[i])
		}
	}
	if _, err := MergeConfigFiles(core.EngineXray, files); err != nil {
		t.Fatal(err)
	}
	legacy := []ConfigFile{{Path: "00-common.json", Content: `{}`}, {Path: "inbounds/0000.json", Content: `{"inbounds":[{"tag":"old","port":1080}]}`}}
	merged, err := MergeConfigFiles(core.EngineXray, legacy)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := SplitConfigFiles(core.EngineXray, merged)
	if err != nil || upgraded[1].Path != "inbounds/old.json" {
		t.Fatalf("legacy upgrade: %+v %v", upgraded, err)
	}
	// Changing the tag within a draft does not invalidate its current manifest.
	upgraded[1].Content = `{"inbounds":[{"tag":"renamed","port":1080}]}`
	merged, err = MergeConfigFiles(core.EngineXray, upgraded)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, _ = SplitConfigFiles(core.EngineXray, merged)
	if upgraded[1].Path != "inbounds/renamed.json" {
		t.Fatal("next revision did not follow the preset name")
	}
}

func TestConfigFilesRejectUnsafeBundles(t *testing.T) {
	for _, files := range [][]ConfigFile{
		{{Path: "common.json", Content: `{}`}, {Path: "inbounds/a.json", Content: `{"inbounds":[{}],"outbounds":null}`}},
		{{Path: "common.json", Content: `{}`}, {Path: "inbounds/a.json", Content: `{"inbounds":[{}],"outbounds":[null]}`}},
		{{Path: "common.json", Content: `{}`}, {Path: "inbounds/a.json", Content: `{"inbounds":[{}],"outbounds":[{}]}`}, {Path: "outbounds/b.json", Content: `{"outbounds":[{}]}`}},
		{{Path: "common.json", Content: `{}`}, {Path: "inbounds/../evil.json", Content: `{"inbounds":[{}]}`}},
		{{Path: "common.json", Content: `{}`}, {Path: "inbounds/.hidden.json", Content: `{"inbounds":[{}]}`}},
		{{Path: "common.json", Content: `{}`}, {Path: "inbounds/a\\b.json", Content: `{"inbounds":[{}]}`}},
		{{Path: "common.json", Content: `{}`}, {Path: "inbounds/a.json", Content: `{"inbounds":[{}]}`}, {Path: "inbounds/a.json", Content: `{"inbounds":[{}]}`}},
		{{Path: "../common.json", Content: `{}`}},
		{{Path: "00-common.json", Content: `{}`}, {Path: "inbounds/../evil.json", Content: `{"inbounds":[{}]}`}},
		{{Path: "00-common.json", Content: `{}`}, {Path: "inbounds/0001.json", Content: `{"inbounds":[{}]}`}},
		{{Path: "00-common.json", Content: `{"inbounds":[]}`}, {Path: "inbounds/0000.json", Content: `{"inbounds":[{}]}`}},
		{{Path: "00-common.json", Content: `{}`}, {Path: "inbounds/0000.json", Content: `{"inbounds":[null]}`}},
		{{Path: "00-common.json", Content: `{}`}, {Path: "inbounds/0000.json", Content: `{"inbounds":[{},{}]}`}},
		{{Path: "00-common.json", Content: `{}`}, {Path: "inbounds/0000.json", Content: `{"inbounds":[{}],"routing":{}}`}},
	} {
		if _, err := MergeConfigFiles(core.EngineXray, files); err == nil {
			t.Fatalf("accepted %#v", files)
		}
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineShadowsocksRust} {
		files, err := SplitConfigFiles(engine, "unchanged")
		if err != nil || len(files) != 1 || files[0].Content != "unchanged" {
			t.Fatalf("changed single file: %v %v", files, err)
		}
		if _, err := MergeConfigFiles(engine, files); err == nil {
			t.Fatal("enabled unsupported multi-file engine")
		}
	}
}
