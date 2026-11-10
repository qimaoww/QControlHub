package api

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"go.yaml.in/yaml/v4"
)

func workspaceExportPath(base string, workspace agentConfigWorkspaceResource) string {
	input := workspace.Inbounds[0]
	return base + "/client-access?" + url.Values{"source": {workspace.ClientSource}, "tag": {input.Tag}, "port": {strconv.Itoa(input.Port)}, "protocol": {input.Protocol}}.Encode()
}

func TestWorkspaceClientExportUsesCurrentWorkspaceAndOwner(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21001)
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21002)
	base := "/agents/" + agent.ID + "/configs/mihomo"
	var initial core.Config
	alice.call("PUT", base, configScopeAPIConfig(t, 21001), 200, &initial)
	var task core.Task
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: initial.Engine, ConfigID: initial.ID, Action: core.ActionDeploy}, 201, &task)
	completeConfigScopeAPITask(t, ctx, db, task)
	var before agentConfigWorkspaceResource
	alice.call("GET", base+"/workspace", nil, 200, &before)
	next := configScopeAPIConfig(t, 21001)
	next.Version = initial.Version
	var saved core.Config
	alice.call("PUT", base, next, 200, &saved)
	alice.call("GET", workspaceExportPath(base, before), nil, 409, nil)
	var current agentConfigWorkspaceResource
	alice.call("GET", base+"/workspace", nil, 200, &current)
	var exported struct{ Content, Format, Source string }
	tasks, _ := db.ListTasks(ctx, "", 100)
	alice.call("GET", workspaceExportPath(base, current), nil, 200, &exported)
	if exported.Source != "saved" || !strings.HasPrefix(exported.Content, "ss://") {
		t.Fatal("missing workspace client URL")
	}
	uri, err := url.Parse(exported.Content)
	if err != nil || uri.Hostname() != "edge.example.test" || uri.Port() != "21001" {
		t.Fatal("incorrect client destination")
	}
	expected, err := serverconfig.BuildClientProfile(current.Inbounds[0], "edge.example.test", "")
	if err != nil || exported.Content != expected.URI {
		t.Fatal("export used deployed credentials")
	}
	// A node administrator and another shared account cannot select Alice's workspace.
	bob.call("GET", workspaceExportPath(base, current), nil, 409, nil)
	admin.call("GET", workspaceExportPath(base, current), nil, 409, nil)
	bob.call("PUT", base, configScopeAPIConfig(t, 21002), 200, nil)
	bob.call("GET", workspaceExportPath(base, current), nil, 409, nil)
	latest, _ := db.AgentConfig(store.WithConfigScope(ctx, alice.userID, false), agent.ID, core.EngineMihomo)
	afterTasks, _ := db.ListTasks(ctx, "", 100)
	if latest.Version != saved.Version || latest.Content != saved.Content || len(executionTasks(t, tasks)) != len(executionTasks(t, afterTasks)) {
		t.Fatal("read export mutated configuration or created tasks")
	}
	// Drop all write and execution permissions while retaining this exact owner.
	permissions := []core.Permission{core.PermissionAgentsRead, core.PermissionAgentConfigRead, core.PermissionClientAccessRead}
	admin.call("PUT", "/users/"+alice.userID, map[string]any{"permissions": permissions}, 200, nil)
	reader, _ := managementLogin(t, admin.handler, "alice", "config scope password fixture", 200)
	reader.call("GET", workspaceExportPath(base, current), nil, 200, &exported)
	permissions = []core.Permission{core.PermissionAgentsRead, core.PermissionAgentConfigRead}
	admin.call("PUT", "/users/"+alice.userID, map[string]any{"permissions": permissions}, 200, nil)
	denied, _ := managementLogin(t, admin.handler, "alice", "config scope password fixture", 200)
	denied.call("GET", workspaceExportPath(base, current), nil, 403, nil)
}

func TestWorkspaceClientExportNativeFormatsAndEngineIdentity(t *testing.T) {
	db, ctx, admin, _, _ := newConfigScopeAPIFixture(t)
	enrollment, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "client-exports"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "exports", OS: "linux", Arch: "amd64", Capabilities: core.AllEngines(), Features: []string{core.AgentFeatureIndependentEgress}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, enrollment.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentClientAddress(ctx, agent.ID, "native.example.test"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		engine   core.Engine
		protocol string
		format   string
	}{
		{core.EngineMihomo, serverconfig.ProtocolSS2022, "Shadowsocks SIP002 URI"},
		{core.EngineMihomo, serverconfig.ProtocolSnell, "Surge Snell"},
		{core.EngineMihomo, serverconfig.ProtocolMieru, "Mihomo Mieru YAML"},
		{core.EngineXray, serverconfig.ProtocolVLESS, "VLESS URI"},
		{core.EngineXray, serverconfig.ProtocolVLESSXHTTP, "Xray client JSON"},
		{core.EngineSingBox, serverconfig.ProtocolWireGuard, "WireGuard native config"},
		{core.EngineShadowsocksRust, serverconfig.ProtocolSS2022, "Shadowsocks SIP002 URI"},
	}
	var previousPath string
	for _, test := range cases {
		t.Run(string(test.engine)+"/"+test.protocol, func(t *testing.T) {
			protocol, ok := serverconfig.FindProtocol(test.engine, test.protocol)
			if !ok {
				t.Fatal("protocol missing")
			}
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Tag, input.Port = "same-tag", 21001
			if input.RealityEnabled {
				input.RealityServerName = "reality.example.test"
			}
			if test.protocol == serverconfig.ProtocolVLESSXHTTP {
				input.XHTTPExtra = `{"xmux":{"maxConcurrency":8}}`
			}
			content, err := serverconfig.Generate(test.engine, input)
			if err != nil {
				t.Fatal(err)
			}
			base := "/agents/" + agent.ID + "/configs/" + string(test.engine)
			current, _ := db.AgentConfig(ctx, agent.ID, test.engine)
			metadata, err := serverconfig.MarshalClientMetadata(input)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.SaveAgentConfigWithClientMetadata(ctx, core.Config{AgentID: agent.ID, Engine: test.engine, Name: "native", Content: content}, current.Version, store.ConfigClientMetadataMutation{Tag: input.Tag, Content: metadata})
			if err != nil {
				t.Fatal(err)
			}
			var workspace agentConfigWorkspaceResource
			admin.call("GET", base+"/workspace", nil, 200, &workspace)
			path := workspaceExportPath(base, workspace)
			var result struct{ Content, Format string }
			admin.call("GET", path, nil, 200, &result)
			if result.Format != test.format || result.Content == "" {
				t.Fatalf("unexpected format %q", result.Format)
			}
			if test.protocol == serverconfig.ProtocolWireGuard && (!strings.Contains(result.Content, "[Interface]") || !strings.Contains(result.Content, input.WireGuardClientPrivateKey)) {
				t.Fatal("incomplete native WireGuard export")
			}
			if test.protocol == serverconfig.ProtocolSnell && !strings.Contains(result.Content, "= snell, native.example.test, 21001") {
				t.Fatal("invalid Surge export")
			}
			if previousPath != "" {
				other := strings.Split(previousPath, "?")[1]
				admin.call("GET", base+"/client-access?"+other, nil, 409, nil)
			}
			previousPath = path
		})
	}
}

func TestWorkspaceClientExportPreservesNativeAndVariants(t *testing.T) {
	for _, key := range []string{serverconfig.ProtocolVLESSXHTTP, serverconfig.ProtocolXHTTPFive, serverconfig.ProtocolMieru} {
		engine := core.EngineXray
		if key == serverconfig.ProtocolMieru {
			engine = core.EngineMihomo
		}
		protocol, ok := serverconfig.FindProtocol(engine, key)
		if !ok {
			t.Fatal("protocol missing")
		}
		input, err := serverconfig.NewPlan(protocol)
		if err != nil {
			t.Fatal(err)
		}
		if key == serverconfig.ProtocolVLESSXHTTP {
			input.XHTTPExtra = `{"xmux":{"maxConcurrency":8}}`
		}
		if key == serverconfig.ProtocolXHTTPFive {
			input.TLSServerName = "tls.example.test"
			input.XHTTPTLSAddress = "tls.example.test"
		}
		profile, err := serverconfig.BuildClientProfile(input, "native.example.test", "")
		if err != nil {
			t.Fatal(err)
		}
		content, format, err := workspaceClientExport(profile)
		if err != nil {
			t.Fatal(err)
		}
		if key == serverconfig.ProtocolMieru {
			var value map[string]any
			if yaml.Unmarshal([]byte(content), &value) != nil || len(value) == 0 {
				t.Fatal("invalid YAML")
			}
			continue
		}
		if format != "Xray client JSON" {
			t.Fatalf("expected native JSON, got %q", format)
		}
		var document map[string]any
		if json.Unmarshal([]byte(content), &document) != nil {
			t.Fatal("invalid native JSON")
		}
		if key == serverconfig.ProtocolXHTTPFive && len(document["outbounds"].([]any)) != 5 {
			t.Fatal("missing or repeated variant")
		}
	}
}

func TestWorkspaceClientExportSnapshotKeepsItsOwnCredentials(t *testing.T) {
	db, ctx, admin, _, _ := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	base := "/agents/" + agent.ID + "/configs/mihomo"
	var saved core.Config
	admin.call("PUT", base, configScopeAPIConfig(t, 21001), 200, &saved)
	var workspace agentConfigWorkspaceResource
	admin.call("GET", base+"/workspace", nil, 200, &workspace)
	snapshot := configScopeAPIConfig(t, 21001)
	var read core.Task
	admin.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionReadManagedConfig}, 201, &read)
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatal("read task not claimable")
	}
	if err := db.CompleteTask(ctx, agent.ID, read.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, Output: snapshot.Content}); err != nil {
		t.Fatal(err)
	}
	var preview agentConfigWorkspaceResource
	admin.call("GET", base+"/workspace/snapshots/"+read.ID, nil, 200, &preview)
	path := workspaceExportPath(base, preview) + "&snapshot=" + read.ID
	var result struct{ Content, Source string }
	admin.call("GET", path, nil, 200, &result)
	expected, _ := serverconfig.BuildClientProfile(preview.Inbounds[0], "edge.example.test", "")
	if result.Content != expected.URI || result.Source != "snapshot" || preview.ClientSource == workspace.ClientSource {
		t.Fatal("snapshot export selected saved source")
	}
	admin.call("GET", workspaceExportPath(base, workspace)+"&snapshot="+read.ID, nil, 409, nil)
	// Administrative fleet access must not grant another account's snapshot.
	var otherUser core.User
	admin.call("POST", "/users", core.UserRequest{Username: "other-admin", Password: "other administrator snapshot fixture", Role: core.RoleAdmin}, 201, &otherUser)
	other, _ := managementLogin(t, admin.handler, "other-admin", "other administrator snapshot fixture", 200)
	var otherRead core.Task
	other.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionReadManagedConfig}, 201, &otherRead)
	claimed, err = db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != otherRead.ID {
		t.Fatal("foreign read task not claimable")
	}
	if err := db.CompleteTask(ctx, agent.ID, otherRead.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, Output: snapshot.Content}); err != nil {
		t.Fatal(err)
	}
	admin.call("GET", workspaceExportPath(base, preview)+"&snapshot="+otherRead.ID, nil, 404, nil)
	var current core.Config
	admin.call("GET", base, nil, 200, &current)
	if current.Version != saved.Version || current.Content != saved.Content {
		t.Fatal("copy saved snapshot")
	}
}
