package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDiscoverSingBoxEntriesIncludesInboundAndEndpoint(t *testing.T) {
	content := `{"inbounds":[{"tag":"web","type":"socks","listen_port":1080}],"endpoints":[{"tag":"wg","type":"wireguard","listen_port":51820},{"tag":"ts","type":"tailscale"}]}`
	entries, err := DiscoverSingBoxEntries(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[1].Section != "endpoints" || entries[1].Port != 51820 || entries[2].Port != 0 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestMutateGeneratedSingBoxEndpointChecksCrossListIdentity(t *testing.T) {
	current := `{"inbounds":[{"tag":"web","type":"socks","listen_port":1080}],"endpoints":[]}`
	input, generated := wireGuardEndpointFixture(t)
	input.Port = 1080
	conflicting, err := Generate(core.EngineSingBox, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MutateGenerated(core.EngineSingBox, current, conflicting, "", "add"); err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("expected cross-list port collision, got %v", err)
	}
	added, err := MutateGenerated(core.EngineSingBox, current, generated, "", "add")
	if err != nil || !strings.Contains(added, `"endpoints"`) {
		t.Fatalf("add endpoint error=%v\n%s", err, added)
	}
	input.Tag, input.Port = "wg2", 51821
	generated, err = Generate(core.EngineSingBox, input)
	if err != nil {
		t.Fatal(err)
	}
	modified, err := MutateGenerated(core.EngineSingBox, added, generated, "wg", "modify")
	if err != nil || strings.Contains(modified, `"tag": "wg"`) || !strings.Contains(modified, `"tag": "wg2"`) {
		t.Fatalf("rename endpoint error=%v\n%s", err, modified)
	}
	deleted, err := MutateGenerated(core.EngineSingBox, modified, `{"endpoints":[{"tag":"wg2","type":"wireguard","listen_port":51821}]}`, "wg2", "delete")
	if err != nil || strings.Contains(deleted, `"tag": "wg2"`) {
		t.Fatalf("delete endpoint error=%v\n%s", err, deleted)
	}
}

func TestMutateGeneratedSingBoxEndpointPreservesUnknownCustomFields(t *testing.T) {
	_, generated := wireGuardEndpointFixture(t)
	current := strings.Replace(generated, `"type": "wireguard"`, `"type": "wireguard", "custom_vendor_field":{"keep":true}`, 1)
	if _, err := MutateGenerated(core.EngineSingBox, current, generated, "wg", "modify"); err == nil {
		t.Fatal("custom endpoint was accepted for lossy preset editing")
	}
	updated, err := MutateGenerated(core.EngineSingBox, current, `{"inbounds":[{"tag":"web","type":"socks","listen_port":1080}]}`, "", "add")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated, `"custom_vendor_field"`) || !strings.Contains(updated, `"keep": true`) {
		t.Fatalf("unknown endpoint field was lost: %s", updated)
	}
}
