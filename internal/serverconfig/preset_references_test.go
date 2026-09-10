package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPresetInboundReferencesRenameAndDelete(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		route, selector := "routing", "inboundTag"
		if engine == core.EngineSingBox {
			route, selector = "route", "inbound"
		}
		content := `{"custom":9007199254740993,"` + route + `":{"rules":[{"` + selector + `":["old"],"marker":"one"},{"` + selector + `":["old","other"],"marker":"many"},{"` + selector + `":[],"marker":"empty"},{"marker":"global"}]}}`
		renamed, err := ReconcilePresetInboundReferences(engine, content, "old", "new")
		if err != nil || strings.Contains(renamed, `"old"`) || strings.Count(renamed, `"new"`) != 2 || !strings.Contains(renamed, "9007199254740993") {
			t.Fatalf("rename %s: %s %v", engine, renamed, err)
		}
		removed, err := ReconcilePresetInboundReferences(engine, renamed, "new", "")
		if err != nil || strings.Contains(removed, `"one"`) || strings.Contains(removed, `"new"`) || !strings.Contains(removed, `"other"`) || !strings.Contains(removed, `"empty"`) || !strings.Contains(removed, `"global"`) {
			t.Fatalf("delete %s widened or removed unrelated rule: %s %v", engine, removed, err)
		}
	}
	content := "# keep root comment\nlisteners: []\nrules: ['IN-NAME,old,exit', 'IN-NAME,other,DIRECT', 'MATCH,DIRECT']\n"
	renamed, err := ReconcilePresetInboundReferences(core.EngineMihomo, content, "old", "new")
	if err != nil || !strings.Contains(renamed, "IN-NAME,new,exit") || !strings.Contains(renamed, "# keep root comment") {
		t.Fatalf("mihomo rename: %s %v", renamed, err)
	}
	removed, err := ReconcilePresetInboundReferences(core.EngineMihomo, renamed, "new", "")
	if err != nil || strings.Contains(removed, "new") || !strings.Contains(removed, "IN-NAME,other,DIRECT") {
		t.Fatalf("mihomo delete: %s %v", removed, err)
	}
}

func TestPresetInboundDNSReferencesFailClosedWhenAmbiguous(t *testing.T) {
	for _, rule := range []string{
		`{"inbound":["old"],"invert":true,"server":"remote"}`,
		`{"type":"logical","mode":"or","rules":[{"inbound":["old"]},{"domain":["example.com"]}],"server":"remote"}`,
	} {
		content := `{"dns":{"rules":[` + rule + `]}}`
		if _, err := ReconcilePresetInboundReferences(core.EngineSingBox, content, "old", ""); err == nil {
			t.Fatal("ambiguous DNS restriction removed")
		}
	}
	content := `{"dns":{"rules":[{"inbound":"old","server":"remote"},{"domain":["example.com"],"server":"local"}]}}`
	updated, err := ReconcilePresetInboundReferences(core.EngineSingBox, content, "old", "new")
	if err != nil || !strings.Contains(updated, `"new"`) || !strings.Contains(updated, "example.com") {
		t.Fatalf("DNS rename: %s %v", updated, err)
	}
	for _, rule := range []string{"AND,((IN-NAME,old),(DOMAIN,example.com)),DIRECT", "AND,((IN-NAME, old ),(DOMAIN,example.com)),DIRECT"} {
		if _, err := ReconcilePresetInboundReferences(core.EngineMihomo, "rules: ['"+rule+"']\n", "old", ""); err == nil {
			t.Fatal("ambiguous logical Mihomo rule removed")
		}
	}
}

func TestPresetInboundMihomoSubruleReferencesFailClosed(t *testing.T) {
	for _, rule := range []string{"IN-NAME,old,DIRECT", "IN-NAME, old ,DIRECT", "AND,((IN-NAME,old),(DOMAIN,example.com)),DIRECT"} {
		// After the last inbound is deleted there is no accounting replan
		// to reject sub-rules; this reference check must still catch them.
		content := "listeners: []\nrules: ['MATCH,DIRECT']\nsub-rules:\n  custom:\n    - '" + rule + "'\n"
		for _, next := range []string{"new", ""} {
			if _, err := ReconcilePresetInboundReferences(core.EngineMihomo, content, "old", next); err == nil {
				t.Fatalf("sub-rule reference was not rejected: %q -> %q", rule, next)
			}
		}
	}
	content := "listeners: []\nsub-rules:\n  old:\n    - IN-NAME,old-other,DIRECT\n"
	if result, err := ReconcilePresetInboundReferences(core.EngineMihomo, content, "old", ""); err != nil || result != content {
		t.Fatalf("unrelated sub-rule changed: %s %v", result, err)
	}
}
