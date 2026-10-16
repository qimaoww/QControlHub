package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSingBoxFinalPresetDeletionRemovesManagedStatistics(t *testing.T) {
	const original = `{"inbounds":[{"type":"socks","tag":"failed","listen_port":21001}],"outbounds":[{"type":"direct","tag":"direct"}],"log":{"level":"warn"},"experimental":{"cache_file":{"enabled":true,"cache_id":"9007199254740993"}}}`
	plan, err := PlanPresetAccounting(core.EngineSingBox, original)
	if err != nil {
		t.Fatal(err)
	}
	source, err := PresetAccountingSource(core.EngineSingBox, plan.Content)
	if err == nil {
		source, err = DeletePresetInbound(core.EngineSingBox, source, "failed")
	}
	if err == nil {
		source, err = ReconcilePresetInboundReferences(core.EngineSingBox, source, "failed", "")
	}
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(source, "v2ray_api") || strings.Contains(source, accountingPrefix) {
		t.Fatalf("deleted preset left generated statistics in saved source: %s", source)
	}
	assertEmptySingBoxAccounting(t, source)
}

func TestSingBoxEmptyLegacyAccountingCleanup(t *testing.T) {
	// Already queued tasks and saved revisions from older panels still need
	// to work on official builds without with_v2ray_api.
	assertEmptySingBoxAccounting(t, `{"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}],"log":{"level":"warn"},"experimental":{"cache_file":{"enabled":true,"cache_id":"9007199254740993"},"v2ray_api":{"listen":"127.0.0.1:10086","stats":{"enabled":true,"inbounds":["failed"],"outbounds":[]}}}}`)
	for _, content := range []string{
		`{"inbounds":[],"experimental":{"v2ray_api":{"listen":"0.0.0.0:10086"}}}`,
		`{"inbounds":[],"experimental":{"clash_api":{"external_controller":"127.0.0.1:9090"}}}`,
		`{"inbounds":[],"endpoints":[{"type":"tun"}],"experimental":{"v2ray_api":{"listen":"127.0.0.1:10086"}}}`,
	} {
		if _, err := PrepareIndependentEgress(core.EngineSingBox, content); err == nil {
			t.Fatalf("cleanup accepted unsupported empty ingress or management API: %s", content)
		}
	}
}

func assertEmptySingBoxAccounting(t *testing.T, content string) {
	t.Helper()
	plan, err := PrepareIndependentEgress(core.EngineSingBox, content)
	if err != nil || plan.Source != "disabled" || len(plan.Ports) != 0 || strings.Contains(plan.Content, "v2ray_api") {
		t.Fatalf("empty configuration retained accounting listener: %+v %v", plan, err)
	}
	root, err := decodeAccountingRoot(core.EngineSingBox, plan.Content)
	if err != nil || mapValue(root["log"])["level"] != "warn" || mapValue(mapValue(root["experimental"])["cache_file"])["cache_id"] != "9007199254740993" {
		t.Fatalf("cleanup lost common configuration: %s %v", plan.Content, err)
	}
	again, err := PrepareIndependentEgress(core.EngineSingBox, plan.Content)
	if err != nil || again.Content != plan.Content {
		t.Fatalf("empty cleanup is not idempotent: %s %v", again.Content, err)
	}
}
