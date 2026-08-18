package configschema

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCatalogsCoverOfficialIndexes(t *testing.T) {
	t.Parallel()
	wants := map[core.Engine]struct{ fields, topics int }{
		core.EngineMihomo:          {fields: 64, topics: 76},
		core.EngineXray:            {fields: 17, topics: 63},
		core.EngineSingBox:         {fields: 14, topics: 120},
		core.EngineShadowsocksRust: {fields: 13, topics: 4},
	}
	for engine, want := range wants {
		catalog, err := CatalogFor(engine)
		if err != nil {
			t.Fatalf("CatalogFor(%s): %v", engine, err)
		}
		if len(catalog.Fields) != want.fields || catalog.TopicCount != want.topics {
			t.Errorf("CatalogFor(%s) = %d fields / %d topics, want %d / %d", engine, len(catalog.Fields), catalog.TopicCount, want.fields, want.topics)
		}
		seen := map[string]bool{}
		for _, field := range catalog.Fields {
			if field.Key == "" || field.Docs == "" || seen[field.Key] {
				t.Errorf("CatalogFor(%s) has invalid or duplicate field %#v", engine, field)
			}
			seen[field.Key] = true
		}
	}
}

func TestMergeYAMLPreservesUnknownFieldsAndComments(t *testing.T) {
	t.Parallel()
	input := "# keep this comment\nfuture-option: yes\nmixed-port: 7890\n"
	output, err := MergeFragment(core.EngineMihomo, input, "mixed-port", "1080", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "# keep this comment") || !strings.Contains(output, "future-option: yes") || !strings.Contains(output, "mixed-port: 1080") {
		t.Fatalf("unexpected merged YAML:\n%s", output)
	}
	fragment, exists, err := Fragment(core.EngineMihomo, output, "mixed-port")
	if err != nil || !exists || fragment != "1080" {
		t.Fatalf("Fragment() = %q, %v, %v", fragment, exists, err)
	}
}

func TestMergeJSONPreservesUnknownFieldsAndRemovesSelectedField(t *testing.T) {
	t.Parallel()
	output, err := MergeFragment(core.EngineXray, `{"future":{"enabled":true},"inbounds":[]}`, "inbounds", `[{"protocol":"vless"}]`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"future"`) || !strings.Contains(output, `"vless"`) {
		t.Fatalf("unexpected merged JSON: %s", output)
	}
	output, err = MergeFragment(core.EngineXray, output, "inbounds", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, `"inbounds"`) || !strings.Contains(output, `"future"`) {
		t.Fatalf("unexpected JSON after remove: %s", output)
	}
}

func TestMergeListItemPreservesAdvancedFieldsAndAdditionalInbounds(t *testing.T) {
	t.Parallel()
	yamlCurrent := "# keep root comment\nfuture-option: yes\nlisteners:\n  - name: primary\n    type: vmess\n    port: 1000\n    sniffing:\n      enabled: true\n  - name: secondary\n    type: http\n    port: 2000\n"
	yamlGenerated := "log-level: info\nlisteners:\n  - name: replacement\n    type: trojan\n    port: 3000\nrules:\n  - MATCH,DIRECT\n"
	yamlOutput, err := MergeListItem(core.EngineMihomo, yamlCurrent, yamlGenerated, "listeners", "name", "primary")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"# keep root comment", "future-option: yes", "name: replacement", "name: secondary", "sniffing:", "enabled: true", "log-level: info", "MATCH,DIRECT"} {
		if !strings.Contains(yamlOutput, expected) {
			t.Errorf("merged YAML does not contain %q:\n%s", expected, yamlOutput)
		}
	}
	if strings.Contains(yamlOutput, "name: primary") || strings.Count(yamlOutput, "name: replacement") != 1 {
		t.Fatalf("primary YAML inbound was not replaced exactly once:\n%s", yamlOutput)
	}

	jsonCurrent := `{"future":{"enabled":true},"inbounds":[{"tag":"primary","port":1000,"streamSettings":{"sockopt":{"tcpFastOpen":true}}},{"tag":"secondary","port":2000}]}`
	jsonGenerated := `{"log":{"level":"info"},"inbounds":[{"tag":"replacement","port":3000}],"outbounds":[{"tag":"direct"}]}`
	jsonOutput, err := MergeListItem(core.EngineSingBox, jsonCurrent, jsonGenerated, "inbounds", "tag", "primary")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"future"`, `"replacement"`, `"secondary"`, `"sockopt"`, `"tcpFastOpen"`, `"outbounds"`, `"direct"`} {
		if !strings.Contains(jsonOutput, expected) {
			t.Errorf("merged JSON does not contain %q:\n%s", expected, jsonOutput)
		}
	}
	if strings.Contains(jsonOutput, `"primary"`) || strings.Count(jsonOutput, `"replacement"`) != 1 {
		t.Fatalf("primary JSON inbound was not replaced exactly once:\n%s", jsonOutput)
	}
}

func TestMutateListItemEnforcesExplicitAddModifyDelete(t *testing.T) {
	t.Parallel()
	yamlCurrent := "future-option: keep\nlisteners:\n  - name: primary\n    type: vmess\n    port: 1000\n    future-inbound: keep\n  - name: secondary\n    type: http\n    port: 2000\n"
	yamlGenerated := "log-level: info\nlisteners:\n  - name: replacement\n    type: trojan\n    port: 3000\n"
	yamlModified, err := MutateListItem(core.EngineMihomo, yamlCurrent, yamlGenerated, "listeners", "name", "primary", "modify", "name", "type", "port")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"name: replacement", "name: secondary", "future-option: keep", "future-inbound: keep"} {
		if !strings.Contains(yamlModified, expected) {
			t.Errorf("modified YAML does not contain %q:\n%s", expected, yamlModified)
		}
	}
	if strings.Contains(yamlModified, "name: primary") {
		t.Fatalf("modified YAML retained the old identity:\n%s", yamlModified)
	}
	yamlAdded, err := MutateListItem(core.EngineMihomo, yamlCurrent, yamlGenerated, "listeners", "name", "", "add")
	if err != nil || !strings.Contains(yamlAdded, "name: primary") || !strings.Contains(yamlAdded, "name: replacement") {
		t.Fatalf("added YAML = %q, %v", yamlAdded, err)
	}
	if _, err := MutateListItem(core.EngineMihomo, yamlAdded, yamlGenerated, "listeners", "name", "", "add"); err == nil {
		t.Fatal("duplicate YAML add was accepted")
	}
	yamlDeleted, err := MutateListItem(core.EngineMihomo, yamlCurrent, yamlGenerated, "listeners", "name", "primary", "delete")
	if err != nil || strings.Contains(yamlDeleted, "name: primary") || !strings.Contains(yamlDeleted, "name: secondary") || strings.Contains(yamlDeleted, "log-level") {
		t.Fatalf("deleted YAML = %q, %v", yamlDeleted, err)
	}
	if _, err := MutateListItem(core.EngineMihomo, yamlCurrent, yamlGenerated, "listeners", "name", "missing", "modify"); err == nil {
		t.Fatal("missing YAML modify target was appended")
	}

	jsonCurrent := `{"future":{"keep":true},"inbounds":[{"tag":"primary","type":"vmess","listen_port":1000,"futureInbound":"keep"},{"tag":"secondary","type":"socks","listen_port":2000}]}`
	jsonGenerated := `{"log":{"level":"info"},"inbounds":[{"tag":"replacement","type":"trojan","listen_port":3000}]}`
	jsonModified, err := MutateListItem(core.EngineSingBox, jsonCurrent, jsonGenerated, "inbounds", "tag", "primary", "modify", "tag", "type", "listen_port")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"replacement"`, `"secondary"`, `"futureInbound"`, `"future"`} {
		if !strings.Contains(jsonModified, expected) {
			t.Errorf("modified JSON does not contain %q:\n%s", expected, jsonModified)
		}
	}
	jsonAdded, err := MutateListItem(core.EngineSingBox, jsonCurrent, jsonGenerated, "inbounds", "tag", "", "add")
	if err != nil || !strings.Contains(jsonAdded, `"primary"`) || !strings.Contains(jsonAdded, `"replacement"`) {
		t.Fatalf("added JSON = %q, %v", jsonAdded, err)
	}
	if _, err := MutateListItem(core.EngineSingBox, jsonAdded, jsonGenerated, "inbounds", "tag", "", "add"); err == nil {
		t.Fatal("duplicate JSON add was accepted")
	}
	jsonDeleted, err := MutateListItem(core.EngineSingBox, jsonCurrent, jsonGenerated, "inbounds", "tag", "primary", "delete")
	if err != nil || strings.Contains(jsonDeleted, `"primary"`) || !strings.Contains(jsonDeleted, `"secondary"`) || strings.Contains(jsonDeleted, `"log"`) {
		t.Fatalf("deleted JSON = %q, %v", jsonDeleted, err)
	}
	if _, err := MutateListItem(core.EngineSingBox, jsonCurrent, jsonGenerated, "inbounds", "tag", "missing", "delete"); err == nil {
		t.Fatal("missing JSON delete target was accepted")
	}
}
