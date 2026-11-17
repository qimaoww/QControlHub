package frontend

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestRuntimeMetadataMatchesAuthority(t *testing.T) {
	source := string(mustReadFrontendFile(t, "modules/runtime-defaults.js"))
	defaults := core.DefaultPanelSettings()
	expected := map[string]any{
		"panelDefaults":       map[string]int{"task_page_size": defaults.TaskPageSize, "task_poll_interval_ms": defaults.TaskPollIntervalMS, "ui_font_scale": defaults.UIFontScale},
		"panelSettingOptions": core.PanelSettingOptions(), "allEngines": core.AllEngines(), "taskFilterActions": core.TaskFilterActions(),
	}
	for name, value := range expected {
		match := regexp.MustCompile(`export const ` + name + ` = Object.freeze\((.*)\);`).FindStringSubmatch(source)
		if len(match) != 2 {
			t.Fatalf("missing generated %s", name)
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var got, want any
		if err := json.Unmarshal([]byte(match[1]), &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s drift: run go run scripts/generate-runtime-defaults.go", name)
		}
	}
}

func TestInstallerEngineDefaultsMatchAuthority(t *testing.T) {
	data, err := os.ReadFile("../deploy/modules/shared/default-engines.sh")
	if err != nil {
		t.Fatal(err)
	}
	engines := []string{}
	for _, engine := range core.AllEngines() {
		engines = append(engines, string(engine))
	}
	if !strings.Contains(string(data), "qch_default_agent_engines='"+strings.Join(engines, ",")+"'") {
		t.Fatal("installer defaults drift: run go run scripts/generate-runtime-defaults.go and make generate-deploy-scripts")
	}
}
