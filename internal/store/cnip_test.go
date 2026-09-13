package store

import (
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCNIPAccountSettingsAndTaskSnapshot(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, alice := sharedTestUser(t, db, ctx, "cnip-alice")
	bobUser, bob := sharedTestUser(t, db, ctx, "cnip-bob")
	first, second := sharedTestAgent(t, db, alice), sharedTestAgent(t, db, bob)
	a := core.DefaultPanelSettings()
	a.CNIPSource = &core.CNIPSource{URL: "https://example.com/alice.mmdb", Format: "auto", IPv6URL: "https://example.com/alice6.txt"}
	b := core.DefaultPanelSettings()
	b.CNIPSource = &core.CNIPSource{URL: "https://example.com/bob.srs", Format: "auto", IPv6URL: "https://example.com/bob6.srs"}
	if _, err := db.SavePanelSettings(alice, a); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SavePanelSettings(bob, b); err != nil {
		t.Fatal(err)
	}
	read, err := db.PanelSettings(alice)
	if err != nil || read.CNIPSource.URL != a.CNIPSource.URL {
		t.Fatalf("alice settings %+v %v", read, err)
	}
	read, err = db.PanelSettings(bob)
	if err != nil || read.CNIPSource.URL != b.CNIPSource.URL {
		t.Fatalf("bob settings %+v %v", read, err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET capabilities='["xray","mihomo"]'::jsonb, features=features||'["cnip-source-v1"]'::jsonb WHERE id=ANY($1)`, []string{first.ID, second.ID}); err != nil {
		t.Fatal(err)
	}
	config, err := db.SaveAgentConfig(alice, core.Config{AgentID: first.ID, Engine: core.EngineXray, Name: "cnip", Content: `{"inbounds":[{"tag":"a","port":21001,"protocol":"socks"}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`}, 0)
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.CreateTask(alice, core.TaskRequest{AgentID: first.ID, Engine: core.EngineXray, Action: core.ActionValidate, ConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	if task.CNIPSource == nil || task.CNIPSource.URL != a.CNIPSource.URL {
		t.Fatalf("missing owner source: %+v", task.CNIPSource)
	}
	a.CNIPSource = &core.CNIPSource{}
	if _, err := db.SavePanelSettings(alice, a); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, first.ID)
	if err != nil || claimed == nil || claimed.CNIPSource == nil || (claimed.CNIPSource.URL != "https://example.com/alice.mmdb" || claimed.CNIPSource.IPv6URL != "https://example.com/alice6.txt") {
		t.Fatalf("snapshot changed: %+v %v", claimed, err)
	}
	resumed, err := db.RunningTask(ctx, first.ID)
	if err != nil || resumed == nil || resumed.CNIPSource.URL != claimed.CNIPSource.URL {
		t.Fatalf("reconnect lost source: %+v %v", resumed, err)
	}
	sharedTestAllocation(t, db, ctx, bobUser.ID, first.ID, 1000000, 22001)
	borrowed := sharedTestConfig(t, db, bob, first.ID, 22001)
	borrowedTask, err := db.CreateTask(bob, core.TaskRequest{AgentID: first.ID, Engine: core.EngineMihomo, Action: core.ActionValidate, ConfigID: borrowed.ID})
	if err != nil || borrowedTask.CNIPSource == nil || borrowedTask.CNIPSource.URL != b.CNIPSource.URL {
		t.Fatalf("borrower inherited host source: %+v %v", borrowedTask.CNIPSource, err)
	}
	// Existing Agents must never silently ignore the custom-source payload.
	if _, err := db.pool.Exec(ctx, "UPDATE agents SET features=features-'cnip-source-v1' WHERE id=$1", second.ID); err != nil {
		t.Fatal(err)
	}
	own, err := db.SaveAgentConfig(bob, core.Config{AgentID: second.ID, Engine: core.EngineXray, Name: "old-agent", Content: "{\"inbounds\":[]}"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTask(bob, core.TaskRequest{AgentID: second.ID, Engine: core.EngineXray, Action: core.ActionValidate, ConfigID: own.ID}); err == nil {
		t.Fatal("old Agent accepted custom source")
	}

}
