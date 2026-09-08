package serverconfig

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestConfigFilesRoundTrip(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		content := `{"inbounds":[{"tag":"one","port":1080},{"tag":"two","port":1081}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"large":9007199254740993,"routing":{"rules":[]}}`
		files, err := SplitConfigFiles(engine, content)
		if err != nil || len(files) != 4 {
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

func TestConfigFilesRejectUnsafeBundles(t *testing.T) {
	for _, files := range [][]ConfigFile{
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
