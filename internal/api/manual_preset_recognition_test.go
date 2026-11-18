package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

const mixedNativeXraySource = `{
  "inbounds": [
    {"tag":"quoted-port","port":"21001","listen":"127.0.0.1","protocol":"vmess",
     "settings":{"clients":[{"id":"123e4567-e89b-42d3-a456-426614174001","email":"first"},{"id":"123e4567-e89b-42d3-a456-426614174002","email":"second"}]},
     "sniffing":{"enabled":false,"future":9007199254740993}},
    {"tag":"native-range","port":"21101-21102","listen":"127.0.0.1","protocol":"http"},
    {"tag":"forward","port":21003,"listen":"127.0.0.1","protocol":"tunnel",
     "settings":{"allowedNetwork":"tcp","rewriteAddress":"example.test","rewritePort":80,"followRedirect":false}}
  ],
  "outbounds":[{"tag":"direct","protocol":"freedom"}],
  "routing":{"rules":[{"type":"field","inboundTag":["native-range"],"outboundTag":"direct"}]}
}`

func TestWorkspaceInboundRowsDistinguishSSRustRootFromFirstArrayEntry(t *testing.T) {
	const engine = core.EngineShadowsocksRust
	const source = `{"id":"root","server":"127.0.0.1","server_port":21001,"method":"aes-128-gcm","password":"fixture-only","servers":[{"id":"first","server":"::1","server_port":21001,"method":"aes-128-gcm","password":"fixture-only-v6"}]}`
	for _, tc := range []struct {
		name, source string
		projectedRow int
	}{
		{"root projection", source, 0},
		{"array projection", strings.Replace(source, `"method":"aes-128-gcm","password":"fixture-only"`, `"method":"none"`, 1), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory, err := serverconfig.DiscoverNativeInbounds(engine, tc.source)
			if err != nil {
				t.Fatal(err)
			}
			rows := workspaceInboundRows(inventory, serverconfig.ParseAll(engine, tc.source))
			if len(rows) != 2 || rows[tc.projectedRow].ProjectionIndex == nil || rows[1-tc.projectedRow].ProjectionIndex != nil {
				t.Fatal("root and array identities shared a projection")
			}
		})
	}
}

func TestNativeInboundInventoryAcrossKernels(t *testing.T) {
	for _, tc := range []struct {
		engine core.Engine
		source string
		want   []string
	}{
		{core.EngineSingBox,
			`{"inbounds":[{"tag":"ws","type":"vmess","listen":"::1","listen_port":21001,"users":[{"uuid":"123e4567-e89b-42d3-a456-426614174001"}],"transport":{"type":"ws","path":"/native"},"tls":{"enabled":false,"min_version":"1.3"}},{"type":"http","listen":"127.0.0.1","listen_port":21002}],
			"endpoints":[{"type":"tailscale","tag":"tail"},{"type":"wireguard","tag":"wg","listen_port":21003,"address":["10.92.0.1/24"],"private_key":"dwdtCnMYpX08FsFyUbJmRd9ML4frwJkqsXf7pR25LCo=","peers":[{"public_key":"3p7bfXt9wbTTW2HC7OQ1Nz+DQ8hbeGdNrfx+FG+IK08=","allowed_ips":["10.92.0.2/32"],"persistent_keepalive_interval":0}]}]}`,
			[]string{"vmess-ws", "", "", "wireguard"}},
		{core.EngineMihomo,
			"listeners:\n  - {name: first, type: snell, port: 21001, psk: fixture-snell-v6, version: 6, mode: unshaped, udp: false}\n  - {name: native, type: socks, listen: 127.0.0.1, port: 21002}\n  - {name: second, type: snell, listen: '::1', port: 21003, psk: fixture-snell-v6, version: 6, mode: unshaped}\nrules: ['MATCH,DIRECT']\n",
			[]string{"snell-v6-unshaped", "", "snell-v6-unshaped"}},
		{core.EngineShadowsocksRust,
			`{"server":"::","server_port":21001,"method":"aes-128-gcm","password":"test-password-012345"}`,
			[]string{"shadowsocks"}},
		{core.EngineShadowsocksRust,
			`{"shadowsocks":[{"id":"plain","server":"::","server_port":21001,"method":"aes-128-gcm","password":"test-password-012345"},{"id":"cipherless","server":"127.0.0.1","server_port":21002,"method":"none","password":""},{"server":"127.0.0.1","server_port":21003,"method":"aes-128-gcm","password":"test-password-012345"},{"name":"compatibility","address":"127.0.0.1","port":21004,"method":"aes-128-gcm","password":"test-password-012345"}]}`,
			[]string{"shadowsocks", "", "", "shadowsocks"}},
	} {
		t.Run(string(tc.engine), func(t *testing.T) {
			db, ctx, _, owner, _ := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "native inventory", tc.engine)
			if tc.engine == core.EngineShadowsocksRust {
				if err := db.SetAgentClientAddress(ctx, agent.ID, "ss-rust.example.test"); err != nil {
					t.Fatal(err)
				}
			}
			base := "/agents/" + agent.ID + "/configs/" + string(tc.engine)
			owner.call("PUT", base, core.Config{Name: "native source", Content: tc.source}, 200, nil)
			var workspace agentConfigWorkspaceResource
			owner.call("GET", base+"/workspace", nil, 200, &workspace)
			var got []string
			for _, row := range workspace.InboundRows {
				protocol := ""
				if row.PresetEditable && row.ProjectionIndex != nil {
					protocol = workspace.Inbounds[*row.ProjectionIndex].Protocol
				}
				got = append(got, protocol)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("protocols in source order = %q, want %q", got, tc.want)
			}
			if tc.engine == core.EngineSingBox {
				wg := workspace.InboundRows[3]
				if wg.Section != "endpoints" || wg.ClientExport || workspace.InboundRows[2].Port != 0 {
					t.Fatal("endpoints lost their native identity or acquired missing client keys")
				}
			}
			if len(tc.want) == 4 && tc.engine == core.EngineShadowsocksRust {
				if row := workspace.InboundRows[2]; row.Tag != "" || row.TargetTag != "" || row.ProjectionIndex == nil || row.PresetEditable {
					t.Fatal("unnamed multi-server entry acquired a positional write target")
				}
				if row := workspace.InboundRows[3]; !row.ClientExport || row.TargetTag != "compatibility" {
					t.Fatal("official SS Rust server aliases lost safe preset or export support")
				}
				var exported struct {
					Content string `json:"content"`
				}
				input := workspace.Inbounds[*workspace.InboundRows[2].ProjectionIndex]
				path := base + "/client-access?" + url.Values{
					"source": {workspace.ClientSource}, "tag": {input.Tag}, "port": {strconv.Itoa(input.Port)}, "protocol": {input.Protocol},
				}.Encode()
				owner.call("GET", path, nil, 200, &exported)
				if exported.Content == "" {
					t.Fatal("untagged native server did not retain its safe client export")
				}
			}
		})
	}
}

func TestNativeInboundInventoryUsesSavedSourceAndStrictIdentity(t *testing.T) {
	db, ctx, admin, owner, other := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "native-rows", core.EngineXray)
	base := "/agents/" + agent.ID + "/configs/xray"
	var config core.Config
	owner.call("PUT", base, core.Config{Name: "mixed", Content: mixedNativeXraySource}, 200, &config)
	var workspace agentConfigWorkspaceResource
	owner.call("GET", base+"/workspace", nil, 200, &workspace)
	rows := workspace.InboundRows
	if len(rows) != 3 || len(workspace.Inbounds) != 2 || len(workspace.InboundTargets) != 2 ||
		rows[0].Tag != "quoted-port" || !rows[0].PresetEditable || rows[1].Tag != "native-range" || rows[1].ProjectionIndex != nil ||
		rows[1].Port != 0 || rows[1].PortSpec != "21101-21102" || rows[2].Tag != "forward" || !rows[2].PresetEditable || rows[2].ClientExport {
		t.Fatal("native list, editable projections or legacy policy targets disagree")
	}
	input := workspace.Inbounds[*rows[2].ProjectionIndex]
	input.TargetPort = 8080
	var result configMutationResult
	owner.call("POST", base+"/server-inbounds", map[string]any{
		"operation": "modify", "original_tag": "forward", "expected_version": config.Version, "intent": "deploy", "input": input,
	}, 200, &result)
	expected := strings.Replace(config.Content, `"rewritePort": 80,`, `"rewritePort": 8080,`, 1)
	decode := func(content string) map[string]any {
		t.Helper()
		var root map[string]any
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&root); err != nil {
			t.Fatal(err)
		}
		return root
	}
	if !reflect.DeepEqual(decode(expected), decode(result.Config.Content)) {
		t.Fatal("editing the third row changed the first two, a route, or the requested field")
	}
	completeConfigScopeAPITask(t, ctx, db, result.Task)
	owner.call("GET", base+"/workspace", nil, 200, &workspace)
	if workspace.Deployment.State != "applied" || len(workspace.InboundRows) != 3 || workspace.InboundRows[1].PortSpec != rows[1].PortSpec {
		t.Fatal("runtime accounting normalization replaced the native inventory")
	}
	// A native object without a port or projection cannot lend its tag to
	// the valid VMess projection, even if a caller sends its original port.
	duplicate := strings.Replace(mixedNativeXraySource, `"tag":"native-range"`, `"tag":"quoted-port"`, 1)
	owner.call("PUT", base, core.Config{Name: "duplicate", Content: duplicate, Version: result.Config.Version}, 200, &config)
	workspace = agentConfigWorkspaceResource{}
	owner.call("GET", base+"/workspace", nil, 200, &workspace)
	if len(workspace.InboundRows) != 3 || workspace.InboundRows[0].TargetTag != "" || workspace.InboundRows[1].TargetTag != "" || !workspace.InboundRows[2].PresetEditable {
		t.Fatal("ambiguous identity affected the wrong rows")
	}
	for _, operation := range []string{"modify", "delete"} {
		owner.call("POST", base+"/server-inbounds", map[string]any{
			"operation": operation, "original_tag": "quoted-port", "expected_version": config.Version, "intent": "validate", "input": input,
		}, 400, nil)
	}
	other.call("GET", base+"/workspace", nil, 404, nil)
	admin.call("PUT", "/users/"+owner.userID, core.UserUpdate{Permissions: &[]core.Permission{core.PermissionAgentConfigRead}}, 200, nil)
	reader, _ := managementLogin(t, admin.handler, "alice", configScopeFixturePassword, 200)
	reader.call("GET", base+"/workspace", nil, 200, &workspace)
	if len(workspace.InboundRows) != 3 || workspace.InboundRows[2].ClientExport || workspace.Config.Version != config.Version {
		t.Fatal("read-only workspace gained capabilities or saved a rejected edit")
	}
	reader.call("POST", base+"/source", map[string]any{"name": "forbidden", "content": duplicate, "version": config.Version, "intent": "deploy"}, 403, nil)
}

func TestWorkspaceRecognitionDoesNotReuseMetadataAcrossSources(t *testing.T) {
	db, ctx, _, owner, other := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "same-node-name", core.EngineSingBox)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	second, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "other-instance", core.EngineSingBox)
	foreign, _ := ownedConfigScopeAPIAgent(t, ctx, db, other, "same-node-name", core.EngineSingBox)
	const native = `{"inbounds":[{"tag":"edge","type":"vless","listen":"127.0.0.1","listen_port":21001,"users":[{"name":"fixture","uuid":"123e4567-e89b-42d3-a456-426614174001"}],"transport":{"type":"ws","path":"/native"}}],"outbounds":[{"tag":"direct","type":"direct"}]}`
	input, ok := serverconfig.Parse(core.EngineSingBox, native)
	if !ok || !input.SourceOnly {
		t.Fatal("plaintext backend exposed an inferred TLS preset")
	}
	input.Protocol = "vless-ws-tls"
	input.TLSEnabled, input.TLSMode, input.TLSServerName, input.TLSPublicPort, input.TLSACMEEmail = true, "caddy", "tls.example.test", 443, "test@example.test"
	meta, err := serverconfig.MarshalClientMetadataWithPrevious(input, "")
	if err != nil {
		t.Fatal(err)
	}
	scoped := store.WithConfigScope(ctx, owner.userID, false)
	saved, err := db.SaveAgentConfigWithClientMetadata(scoped, core.Config{AgentID: agent.ID, Engine: core.EngineSingBox, Name: "bound", Content: native},
		0, store.ConfigClientMetadataMutation{Tag: "edge", Content: meta})
	if err != nil {
		t.Fatal(err)
	}
	base := "/agents/" + agent.ID + "/configs/sing-box"
	var workspace agentConfigWorkspaceResource
	owner.call("GET", base+"/workspace", nil, 200, &workspace)
	if !workspace.InboundRows[0].PresetEditable || workspace.Inbounds[0].Protocol != "vless-ws-tls" {
		t.Fatal("bound metadata did not complete the saved preset")
	}
	for _, entry := range []struct {
		client configScopeAPIClient
		agent  core.Agent
	}{{owner, second}, {other, foreign}} {
		workspace = agentConfigWorkspaceResource{}
		path := "/agents/" + entry.agent.ID + "/configs/sing-box"
		entry.client.call("PUT", path, core.Config{Name: "other", Content: native}, 200, nil)
		entry.client.call("GET", path+"/workspace", nil, 200, &workspace)
		if workspace.InboundRows[0].PresetEditable || workspace.Inbounds[0].TLSEnabled {
			t.Fatal("a different workspace hydrated detached metadata")
		}
	}
	var read core.Task
	owner.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineSingBox, Action: core.ActionReadManagedConfig}, 201, &read)
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != read.ID {
		t.Fatal("managed read task was not claimable")
	}
	if err := db.CompleteTask(ctx, agent.ID, read.ID, core.TaskResultRequest{Success: true, LeaseID: claimed.LeaseID, Output: saved.Content}); err != nil {
		t.Fatal(err)
	}
	workspace = agentConfigWorkspaceResource{}
	owner.call("GET", base+"/workspace/snapshots/"+read.ID, nil, 200, &workspace)
	if workspace.InboundRows[0].PresetEditable || workspace.Inbounds[0].TLSEnabled {
		t.Fatal("a snapshot reused saved TLS metadata")
	}
	other.call("GET", base+"/workspace/snapshots/"+read.ID, nil, 404, nil)
	owner.call("POST", base+"/server-inbounds", map[string]any{
		"operation": "modify", "original_tag": "edge", "expected_version": saved.Version, "snapshot_task_id": read.ID, "intent": "validate", "input": input,
	}, 400, nil)
}

func TestSnapshotEditsDoNotCopySavedClientMetadata(t *testing.T) {
	for _, operation := range []string{"preset", "native fragment", "outbound binding"} {
		t.Run(operation, func(t *testing.T) {
			testSnapshotEditMetadata(t, operation)
		})
	}
}

func testSnapshotEditMetadata(t *testing.T, operation string) {
	t.Helper()
	db, ctx, _, owner, _ := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "snapshot-with-detached-client-host", core.EngineXray)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	base := "/agents/" + agent.ID + "/configs/xray"
	content := `{"inbounds":[{"tag":"ws","listen":"127.0.0.1","port":21001,"protocol":"vmess","settings":{"clients":[{"id":"123e4567-e89b-42d3-a456-426614174001"}]},"streamSettings":{"network":"ws","wsSettings":{"path":"/native"}}}],"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"backup","protocol":"freedom"}]}`
	parsed, ok := serverconfig.Parse(core.EngineXray, content)
	if !ok || !serverconfig.CanEditNativePreset(core.EngineXray, parsed) {
		t.Fatal("plaintext WebSocket preset did not parse")
	}
	parsed.TransportHost = "saved.example.test"
	metadata, err := serverconfig.MarshalClientMetadata(parsed)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := db.SaveAgentConfigWithClientMetadata(store.WithConfigScope(ctx, owner.userID, false),
		core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "saved", Content: content}, 0,
		store.ConfigClientMetadataMutation{Tag: parsed.Tag, Content: metadata})
	if err != nil {
		t.Fatal(err)
	}
	var read core.Task
	owner.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineXray, Action: core.ActionReadManagedConfig}, 201, &read)
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || lease.ID != read.ID {
		t.Fatal("snapshot read was not claimable")
	}
	if err := db.CompleteTask(ctx, agent.ID, read.ID, core.TaskResultRequest{Success: true, LeaseID: lease.LeaseID, Output: content}); err != nil {
		t.Fatal(err)
	}
	var snapshot agentConfigWorkspaceResource
	owner.call("GET", base+"/workspace/snapshots/"+read.ID, nil, 200, &snapshot)
	if snapshot.Inbounds[0].TransportHost != "" || !snapshot.InboundRows[0].PresetEditable {
		t.Fatal("snapshot read reused detached host metadata")
	}
	port, metadataCount := 21001, 0
	var result configMutationResult
	switch operation {
	case "preset":
		port, metadataCount = 21002, 1
		input := snapshot.Inbounds[0]
		input.Port = port
		owner.call("POST", base+"/server-inbounds", map[string]any{
			"operation": "modify", "original_tag": "ws", "expected_version": saved.Version,
			"snapshot_task_id": read.ID, "intent": "validate", "input": input,
		}, 200, &result)
	case "native fragment":
		fragment, _, err := serverconfig.InboundSource(core.EngineXray, content, parsed.Tag)
		if err != nil {
			t.Fatal(err)
		}
		owner.call("POST", base+"/server-inbounds", map[string]any{
			"operation": "modify", "original_tag": "ws", "expected_version": saved.Version,
			"snapshot_task_id": read.ID, "intent": "validate", "fragment": fragment,
		}, 200, &result)
	case "outbound binding":
		owner.call("POST", base+"/outbounds", map[string]any{
			"operation": "bind", "section": "outbounds", "tag": "backup", "inbound_tag": "ws", "inbound_port": port,
			"snapshot_task_id": read.ID, "intent": "validate", "version": saved.Version,
		}, 200, &result)
	}
	values, err := db.ConfigClientMetadata(store.WithConfigScope(ctx, owner.userID, false), result.Config.ID, result.Config.Version)
	if err != nil || len(values) != metadataCount || strings.Contains(values["ws"], "saved.example.test") {
		t.Fatalf("saved snapshot edit inherited unrelated client metadata: %v", err)
	}
	var reopened agentConfigWorkspaceResource
	owner.call("GET", base+"/workspace", nil, 200, &reopened)
	if reopened.Inbounds[0].TransportHost != "" || reopened.Inbounds[0].Port != port {
		t.Fatal("snapshot save restored the previous detached client host")
	}
}

func TestManualPresetRecognitionScopedSaveEditAndRead(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
		for _, shared := range []bool{false, true} {
			t.Run(string(engine)+map[bool]string{false: "/owner", true: "/shared"}[shared], func(t *testing.T) {
				db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
				agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "manual-semantic", engine)
				grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001, 21002, 23101)
				client, foreign := owner, recipient
				if shared {
					client, foreign = recipient, owner
				}
				base := "/agents/" + agent.ID + "/configs/" + string(engine)
				source := `{"inbounds":[{"tag":"manual","listen":"0.0.0.0","port":21001,"protocol":"vless","settings":{"decryption":"none","clients":[{"id":"123e4567-e89b-42d3-a456-426614174000"}]}},{"tag":"editable","listen":"0.0.0.0","port":21002,"protocol":"vmess","settings":{"clients":[{"id":"123e4567-e89b-42d3-a456-426614174001","email":"first","level":9007199254740993},{"id":"123e4567-e89b-42d3-a456-426614174002","email":"second"}]},"future":{"nested":"keep"}}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`
				if engine == core.EngineSingBox {
					source = `{"inbounds":[{"tag":"manual","listen":"0.0.0.0","listen_port":21001,"type":"vless","users":[{"uuid":"123e4567-e89b-42d3-a456-426614174000"}]},{"tag":"editable","listen":"0.0.0.0","listen_port":21002,"type":"vmess","users":[{"uuid":"123e4567-e89b-42d3-a456-426614174001","name":"first"}],"future":{"nested":9007199254740993}}],"outbounds":[{"tag":"direct","type":"direct"}]}`
				}
				if engine == core.EngineMihomo {
					source = `listeners: [{name: manual, type: vless, listen: 0.0.0.0, port: 21001, users: [{uuid: 123e4567-e89b-42d3-a456-426614174000}]}, {name: editable, type: vmess, listen: 0.0.0.0, port: 21002, users: [{uuid: 123e4567-e89b-42d3-a456-426614174001, username: first, future: 9007199254740993}, {uuid: 123e4567-e89b-42d3-a456-426614174002, username: second}], future: {nested: keep}}]
rules: ['MATCH,DIRECT']`
				}
				var config core.Config
				client.call("PUT", base, map[string]any{"name": "manual", "engine": engine, "content": source, "version": 0}, http.StatusOK, &config)
				var workspace agentConfigWorkspaceResource
				client.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
				if len(workspace.Inbounds) != 2 || !workspace.Inbounds[0].SourceOnly || workspace.Inbounds[1].SourceOnly {
					t.Fatal("manual source misclassified")
				}
				if workspace.Config.Content != config.Content {
					t.Fatal("recognition changed source")
				}
				foreign.call("GET", base+"/server-inbounds/manual", nil, http.StatusNotFound, nil)
				admin.call("GET", base+"/server-inbounds/manual", nil, http.StatusNotFound, nil)
				candidate := workspace.Inbounds[1]
				candidate.Tag = "manual"
				body := map[string]any{"operation": "modify", "original_tag": "manual", "expected_version": config.Version, "intent": "validate", "input": candidate}
				// Do not trust a caller that drops the read-only projection marker.
				for i := 0; i < 2; i++ {
					client.call("POST", base+"/server-inbounds", body, http.StatusBadRequest, nil)
				}
				var unchanged core.Config
				client.call("GET", base, nil, http.StatusOK, &unchanged)
				if unchanged.Version != config.Version || unchanged.Content != config.Content {
					t.Fatal("failed preset replacement changed saved source")
				}
				candidate = workspace.Inbounds[1]
				candidate.Port = 23101
				body["original_tag"], body["input"], body["intent"] = "editable", candidate, "deploy"
				var result configMutationResult
				client.call("POST", base+"/server-inbounds", body, http.StatusOK, &result)
				if result.Task.ConfigVersion != result.Config.Version || result.Task.SharedInstance != shared || !strings.Contains(result.Config.Content, "9007199254740993") {
					t.Fatal("edit lost precision or task scope")
				}
				if engine != core.EngineSingBox && !strings.Contains(result.Config.Content, "second") {
					t.Fatal("edit lost extra account")
				}
				client.call("POST", base+"/server-inbounds", body, http.StatusConflict, nil)
				completeConfigScopeAPITask(t, ctx, db, result.Task)
				client.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
				if workspace.Deployment.State != "applied" || len(workspace.Inbounds) != 2 || !workspace.Inbounds[0].SourceOnly || workspace.Inbounds[1].SourceOnly || workspace.Inbounds[1].Port != 23101 {
					t.Fatal("save/deploy/read semantic drift")
				}
				var revisions []core.Config
				client.call("GET", "/configs/"+config.ID+"/revisions?limit=10", nil, http.StatusOK, &revisions)
				if len(revisions) != 2 || revisions[0].Version == revisions[1].Version || revisions[0].UpdatedAt.IsZero() {
					t.Fatal("successful saves did not retain distinct dated archives")
				}
			})
		}
	}
}
