package serverconfig

import (
	"github.com/qimaoww/qcontrolhub/internal/core"
	"strings"
	"testing"
)

const legacyRoutedXrayAPI = `{"api":{"tag":"api","services":["HandlerService","LoggerService","StatsService"]},"inbounds":[{"tag":"api","listen":"127.0.0.1","port":40152,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1"}},{"tag":"VLESS-REALITY-8443.json","port":8443,"protocol":"vless","settings":{"clients":[{"id":"b3625453-d453-4ce5-8abc-9ce94fc44087"}]} }],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["api"],"outboundTag":"api"}]}}`

func TestXrayLegacyAPIDeletion(t *testing.T) {
	plan, err := PrepareAccounting(core.EngineXray, legacyRoutedXrayAPI)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateIndependentEgress(core.EngineXray, plan.Content); err == nil {
		t.Fatal("legacy mutable service must reproduce rejection")
	}
	migrated, err := DisableXrayMutableAPI(core.EngineXray, plan.Content)
	if err != nil {
		t.Fatal(err)
	}
	source, err := PresetAccountingSource(core.EngineXray, migrated)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := DeletePresetInbound(core.EngineXray, source, "VLESS-REALITY-8443.json")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err = ReconcilePresetInboundReferences(core.EngineXray, deleted, "VLESS-REALITY-8443.json", "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := PrepareIndependentEgress(core.EngineXray, deleted)
	if err != nil || result.Source != "disabled" {
		t.Fatalf("final deletion: %+v %v", result, err)
	}
	for _, preserved := range []string{"LoggerService", "StatsService", "40152", `"outboundTag": "api"`, "qch-stat-block"} {
		if !strings.Contains(deleted, preserved) {
			t.Fatalf("lost %s", preserved)
		}
	}
	if strings.Contains(deleted, "HandlerService") || strings.Contains(deleted, "8443") {
		t.Fatal("retained deleted resources")
	}
	// An API-like name, public address or conditional API route cannot establish
	// an empty proxy configuration, even after explicit migration consent.
	for _, unsafe := range []string{
		strings.Replace(deleted, `"127.0.0.1"`, `"0.0.0.0"`, -1),
		strings.Replace(deleted, `"dokodemo-door"`, `"socks"`, 1),
		strings.Replace(deleted, `"outboundTag": "api"`, `"outboundTag": "direct"`, 1),
		strings.Replace(deleted, `"outboundTag": "api"`, `"domain": ["example.test"], "outboundTag": "api"`, 1),
	} {
		if p, e := PrepareIndependentEgress(core.EngineXray, unsafe); e == nil && p.Source == "disabled" {
			t.Fatal("unverified API became empty")
		}
	}
}

func TestXrayNonRoutingServicesAndExplicitMigration(t *testing.T) {
	for _, services := range []string{`"statsservice"`, `"StatsService","LoggerService","ReflectionService","ObservatoryService"`} {
		content := strings.Replace(legacyRoutedXrayAPI, `"HandlerService","LoggerService","StatsService"`, services, 1)
		plan, err := PrepareIndependentEgress(core.EngineXray, content)
		if err != nil || len(plan.Ports) != 1 {
			t.Fatalf("non-routing services rejected: %v", err)
		}
	}
	for _, service := range []string{"HandlerService", "RoutingService", "UnknownService"} {
		content := strings.Replace(legacyRoutedXrayAPI, "HandlerService", service, 1)
		if err := ValidateIndependentEgress(core.EngineXray, content); err == nil {
			t.Fatal("mutable or unknown service accepted")
		}
		_, err := DisableXrayMutableAPI(core.EngineXray, content)
		if (service == "UnknownService") != (err != nil) {
			t.Fatalf("migration %s: %v", service, err)
		}
	}
}
