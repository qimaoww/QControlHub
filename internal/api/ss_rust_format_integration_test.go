package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// Exercise the actual compiler -> editable source -> persistence -> workspace
// round trip. Echoing a submitted body cannot detect accounting compaction.
func TestSSRustReadableSourceRoundTrip(t *testing.T) {
	testJSONReadableSourceRoundTrip(t, core.EngineShadowsocksRust, `{"server":"::","server_port":21001,"method":"aes-256-gcm","password":"fixture-only-password","fast_open":true,"ipv6_first":false,"mode":"tcp_and_udp","timeout":300,"unknown":{"number":9007199254740993,"list":[true,false,"a\\b"]}}`)
}

func TestJSONReadableSourceRoundTrip(t *testing.T) {
	for engine, source := range map[core.Engine]string{
		core.EngineXray:    `{"inbounds":[{"tag":"socks","listen":"::1","port":21001,"protocol":"socks","settings":{"auth":"noauth","udp":true}}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"unknown":{"number":9007199254740993,"decimal":1.234567890123456789,"list":[false,true,"a\\b"]}}`,
		core.EngineSingBox: `{"inbounds":[{"tag":"socks","listen":"::1","listen_port":21001,"type":"socks"}],"outbounds":[{"tag":"direct","type":"direct"}],"route":{"final":"direct"},"unknown":{"number":9007199254740993,"decimal":1.234567890123456789,"list":[false,true,"a\\b"]}}`,
	} {
		t.Run(string(engine), func(t *testing.T) { testJSONReadableSourceRoundTrip(t, engine, source) })
	}
}

func testJSONReadableSourceRoundTrip(t *testing.T, engine core.Engine, source string) {
	db, ctx, _, owner, _ := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "ss-format", engine)

	plan, err := serverconfig.PlanPresetAccounting(engine, source)
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.SaveAgentConfig(store.WithConfigScope(ctx, owner.userID, false), core.Config{AgentID: agent.ID, Engine: engine, Name: "format fixture", Content: plan.Content}, 0)
	if err != nil {
		t.Fatal(err)
	}
	base := "/agents/" + agent.ID + "/configs/" + string(engine)
	check := func(stage, content string) {
		t.Helper()
		if !strings.Contains(content, "\n  \"") {
			t.Errorf("%s: configuration body is compact", stage)
		}
		if !strings.Contains(content, "9007199254740993") {
			t.Errorf("%s: integer precision lost", stage)
		}
	}
	var workspace struct {
		Config core.Config `json:"config"`
		Source string      `json:"source_content"`
	}
	owner.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
	initial := workspace.Source
	if initial == "" {
		initial = workspace.Config.Content
	}
	check("initial workspace", initial)
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, []byte(source), "", "  "); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var result configMutationResult
		owner.call("POST", base+"/source", map[string]any{"name": first.Name, "content": formatted.String() + "\n", "version": first.Version, "intent": "validate"}, http.StatusOK, &result)
		check("save response", result.Config.Content)
		completeConfigScopeAPITask(t, ctx, db, result.Task)
		var fetched core.Config
		owner.call("GET", base, nil, http.StatusOK, &fetched)
		check("independent query", fetched.Content)
		owner.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
		if workspace.Source != "" {
			check("workspace reload", workspace.Source)
		}
		if fetched.Content != result.Config.Content {
			t.Error("save and read disagree")
		}
		first = fetched
	}
	var archive, queried core.Config
	owner.call("POST", "/configs", core.Config{Name: "formatted archive", Engine: engine, Content: source}, http.StatusCreated, &archive)
	check("independent archive save", archive.Content)
	owner.call("GET", "/configs/"+archive.ID+"/revisions/1", nil, http.StatusOK, &queried)
	if queried.Content != archive.Content {
		t.Fatal("archive save/read mismatch")
	}
	archive.Content = source
	owner.call("PUT", "/configs/"+archive.ID, archive, http.StatusOK, &archive)
	check("independent archive update", archive.Content)
	owner.call("POST", "/configs/"+archive.ID+"/revisions/1/restore", map[string]any{"expected_version": archive.Version}, http.StatusOK, &queried)
	check("independent archive restore", queried.Content)

}

func TestSSRustFormatHistoryAndIsolation(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineShadowsocksRust, core.EngineXray, core.EngineSingBox} {
		for _, role := range []string{"admin", "owner", "shared"} {
			t.Run(string(engine)+"/"+role, func(t *testing.T) {
				db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
				client, foreign := owner, recipient
				if role == "admin" {
					client = admin
				}
				agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, client, "json-format-isolation", engine)
				if role == "shared" {
					grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001, 21002)
					client, foreign = recipient, owner
				}
				base := "/agents/" + agent.ID + "/configs/" + string(engine)
				source := `{"fast_open":true,"ipv6_first":false,"mode":"tcp_and_udp","timeout":300,"acl":"/var/lib/fixture/long/path/allow.acl","unknown":{"large":9007199254740993,"decimal":1.234567890123456789,"nested":[{"escaped":"fixture\\\"password"},false]},"servers":[{"id":"one","server":"::","server_port":21001,"password":"fixture-one","method":"aes-256-gcm"},{"id":"two","server":"::1","server_port":21002,"password":"fixture-two","method":"aes-256-gcm"}]}`
				if engine == core.EngineXray {
					source = `{"inbounds":[{"tag":"one","listen":"::1","port":21001,"protocol":"socks","settings":{"auth":"noauth","udp":true}}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`
				}
				if engine == core.EngineSingBox {
					source = `{"inbounds":[{"tag":"one","listen":"::1","listen_port":21001,"type":"socks"}],"outbounds":[{"tag":"direct","type":"direct"}],"route":{"final":"direct"}}`
				}

				if role == "shared" {
					if engine == core.EngineXray {
						source = `{"inbounds":[{"tag":"one","listen":"::","port":21001,"protocol":"shadowsocks","settings":{"method":"aes-256-gcm","password":"fixture-only-password","network":"tcp,udp"}}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`
					}
					var fields map[string]json.RawMessage
					_ = json.Unmarshal([]byte(source), &fields)
					delete(fields, "unknown")
					encoded, _ := json.Marshal(fields)
					source = string(encoded)
				}
				var first core.Config
				client.call("PUT", base, core.Config{Name: "readable source", Content: source}, http.StatusOK, &first)
				var compact bytes.Buffer
				if err := json.Compact(&compact, []byte(first.Content)); err != nil || compact.String() != source {
					t.Fatal("save-only formatting changed tokens", err)
				}
				if !strings.Contains(first.Content, "\n  \"") {
					t.Fatal("save-only persisted compact content")
				}
				version := first.Version
				var previous string
				for round := 0; round < 2; round++ {
					var saved configMutationResult
					client.call("POST", base+"/source", map[string]any{"name": first.Name, "version": version, "content": first.Content, "intent": "validate"}, http.StatusOK, &saved)
					if !strings.Contains(saved.Config.Content, "\n  \"") || strings.Contains(saved.Config.Content, "outbound_fwmark") {
						t.Fatal("editable source lost format or contains generated marks")
					}
					if round > 0 && previous != saved.Config.Content {
						t.Fatal("repeated save changed formatting")
					}
					previous, version = saved.Config.Content, saved.Config.Version
					var fetched core.Config
					client.call("GET", base, nil, http.StatusOK, &fetched)
					if fetched.Content != previous {
						t.Fatal("save/read mismatch")
					}
					lease, err := db.ClaimTask(ctx, agent.ID)
					if err != nil || lease == nil || lease.ID != saved.Task.ID || lease.SharedInstance != (role == "shared") || (engine == core.EngineShadowsocksRust && !strings.Contains(lease.ConfigContent, "outbound_fwmark")) || serverconfig.ValidateIndependentEgress(engine, lease.ConfigContent) != nil {
						t.Fatal("runtime accounting or instance target changed", err)
					}
					if err := db.CompleteTask(ctx, agent.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, TrafficSettled: true}); err != nil {
						t.Fatal(err)
					}
				}
				client.call("POST", base+"/source", map[string]any{"name": first.Name, "version": first.Version, "content": source, "intent": "validate"}, http.StatusConflict, nil)
				client.call("POST", base+"/source", map[string]any{"name": first.Name, "version": version, "content": `{"servers":`, "intent": "validate"}, http.StatusBadRequest, nil)
				admin.call("POST", "/users", core.UserRequest{Username: "no-config-permissions", Role: core.RoleUser, Password: configScopeFixturePassword}, http.StatusCreated, nil)
				limited := loginCoreInstallUser(t, configScopeAPIClient{t: t, handler: admin.handler}, "no-config-permissions")
				limited.call("GET", base, nil, http.StatusForbidden, nil)
				limited.call("POST", base+"/source", map[string]any{"name": first.Name, "version": version, "content": source, "intent": "validate"}, http.StatusForbidden, nil)
				foreign.call("GET", "/configs/"+first.ID+"/revisions/1", nil, http.StatusNotFound, nil)
				foreign.call("POST", "/configs/"+first.ID+"/revisions/1/restore", map[string]any{"expected_version": version}, http.StatusNotFound, nil)
				var restored, archive core.Config
				client.call("POST", "/configs/"+first.ID+"/revisions/1/restore", map[string]any{"expected_version": version}, http.StatusOK, &restored)
				client.call("GET", "/configs/"+first.ID+"/revisions/1", nil, http.StatusOK, &archive)
				if restored.Content != first.Content || restored.Version != version+1 || !restored.UpdatedAt.After(first.UpdatedAt) || archive.Content != first.Content {
					t.Fatal("restore changed original archive or failed to retain readable source")
				}
				var revisions []core.Config
				client.call("GET", "/configs/"+first.ID+"/revisions", nil, http.StatusOK, &revisions)
				if len(revisions) != 4 {
					t.Fatalf("same-day saves overwrote history: %d", len(revisions))
				}
			})
		}
	}
}
