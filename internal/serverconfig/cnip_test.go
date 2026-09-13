package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCNIPCustomResourcesRemainEditable(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
		content := `{"inbounds":[{"tag":"a","port":1080,"listen_port":1080}],"outbounds":[{"tag":"direct","type":"direct","protocol":"freedom"}]}`
		if engine == core.EngineMihomo {
			content = "listeners:\n  - name: a\n    type: socks\n    port: 1080\nrules:\n  - MATCH,DIRECT\n"
		}
		policy := MainlandAccessPolicy{Engine: engine, Tag: "a", Port: 1080, BlockMainlandDestination: true, BlockMainlandSource: true}
		configured, err := ApplyMainlandAccessPolicyWithPrefixes(engine, content, policy, nil)
		if err != nil {
			t.Fatal(err)
		}
		if engine != core.EngineMihomo {
			configured = strings.Replace(configured, "{", "{\"large\":9007199254740993,", 1)
		}
		custom, err := ApplyCNIPPrefixes(engine, configured, []string{"1.0.1.0/24"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(custom, "1.0.1.0/24") {
			t.Fatalf("%s did not use source", engine)
		}
		if engine != core.EngineMihomo && !strings.Contains(custom, "9007199254740993") {
			t.Fatal("integer precision lost")
		}
		found := DiscoverMainlandAccessPolicies(engine, custom)
		if len(found) != 1 || !found[0].BlockMainlandSource || !found[0].BlockMainlandDestination {
			t.Fatalf("%s lost managed identity: %+v", engine, found)
		}
		restored, err := ApplyCNIPPrefixes(engine, custom, nil)
		if err != nil || strings.Contains(restored, "1.0.1.0/24") {
			t.Fatalf("%s failed restoring defaults: %v", engine, err)
		}
		policy.BlockMainlandSource = false
		policy.BlockMainlandDestination = false
		if _, err := ApplyMainlandAccessPolicyWithPrefixes(engine, custom, policy, nil); err != nil {
			t.Fatalf("%s cannot edit custom rules: %v", engine, err)
		}
	}
}
