package store

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestEngineCapabilityDefaultsAndNodeOverrides(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	schema, err := testdb.IsolatePostgres(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(ctx)
	s, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.InitializeDefaultAgentEngines(ctx, []core.Engine{core.EngineXray}); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeDefaultAgentEngines(ctx, core.AllEngines()); err != nil {
		t.Fatal(err)
	}
	settings, err := s.PanelSettings(ctx)
	if err != nil || !reflect.DeepEqual(settings.DefaultAgentEngines, []core.Engine{core.EngineXray}) {
		t.Fatalf("seed overwritten: %+v, %v", settings, err)
	}
	credential, err := s.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "capabilities", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	input := core.EnrollRequest{Name: "capabilities", OS: "linux", Arch: "amd64", Capabilities: core.AllEngines(), PublicKey: base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t))}
	agent, err := s.EnrollAgent(ctx, input, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	assertCapabilities := func(want []core.Engine) {
		t.Helper()
		got, err := s.GetAgent(ctx, agent.ID)
		if err != nil || !reflect.DeepEqual(got.Capabilities, want) || len(got.SupportedCapabilities) != 4 {
			t.Fatalf("agent = %+v, %v; want %v", got, err, want)
		}
	}
	assertCapabilities([]core.Engine{core.EngineXray})
	// Global defaults are not an allowlist: the node can enable Mihomo.
	if err := s.SetAgentEngineCapability(ctx, agent.ID, core.EngineMihomo, true); err != nil {
		t.Fatal(err)
	}
	assertCapabilities([]core.Engine{core.EngineMihomo, core.EngineXray})
	config, err := s.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Name: "saved", Engine: core.EngineMihomo, Content: "mixed-port: 7890"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgentEngineCapability(ctx, agent.ID, core.EngineMihomo, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("pending task gate: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE tasks SET status='running' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgentEngineCapability(ctx, agent.ID, core.EngineMihomo, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("running task gate: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE tasks SET status='canceled' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray} {
		if err := s.SetAgentEngineCapability(ctx, agent.ID, engine, false); err != nil {
			t.Fatal(err)
		}
	}
	assertCapabilities([]core.Engine{})
	if got, err := s.AgentConfig(ctx, agent.ID, core.EngineMihomo); err != nil || got.ID != config.ID {
		t.Fatalf("configuration removed: %+v, %v", got, err)
	}
	if _, err := s.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disabled task accepted: %v", err)
	}
	if _, err := s.SaveAgentConfig(ctx, config, config.Version); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disabled configuration mutation accepted: %v", err)
	}
	settings.DefaultAgentEngines = []core.Engine{}
	if _, err := s.SavePanelSettingsRevision(ctx, settings, settings.Revision); err != nil {
		t.Fatal(err)
	}
	settings.DefaultAgentEngines = nil
	if _, err := s.SavePanelSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	settings, err = s.PanelSettings(ctx)
	if err != nil || settings.DefaultAgentEngines == nil || len(settings.DefaultAgentEngines) != 0 {
		t.Fatalf("legacy save lost empty defaults: %+v %v", settings, err)
	}
	if err := s.SetAgentEngineCapability(ctx, agent.ID, core.EngineSingBox, true); err != nil {
		t.Fatal(err)
	}
	// Re-enrollment retains the node override even though global defaults are empty.
	input.PublicKey = base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t))
	reinstalled, err := s.EnrollAgent(ctx, input, credential.Token)
	if err != nil || reinstalled.ID != agent.ID {
		t.Fatalf("reinstall: %+v %v", reinstalled, err)
	}
	assertCapabilities([]core.Engine{core.EngineSingBox})
	settings.DefaultAgentEngines = []core.Engine{core.EngineMihomo}
	if _, err := s.SavePanelSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	assertCapabilities([]core.Engine{core.EngineSingBox})
	settings.DefaultAgentEngines = []core.Engine{}
	if _, err := s.SavePanelSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	newCredential, err := s.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "monitor", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	input.Name = "monitor"
	input.PublicKey = base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t))
	input.Capabilities = []core.Engine{core.EngineXray}
	monitor, err := s.EnrollAgent(ctx, input, newCredential.Token)
	if err != nil || monitor.Capabilities == nil || len(monitor.Capabilities) != 0 {
		t.Fatalf("monitor enrollment: %+v %v", monitor, err)
	}
	if err := s.SetAgentEngineCapability(ctx, monitor.ID, core.EngineMihomo, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported engine accepted: %v", err)
	}
	if err := s.SetAgentEngineCapability(ctx, monitor.ID, core.EngineXray, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgentEngineCapability(ctx, "missing", core.EngineXray, true); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// Independent per-engine edits serialize on the node without losing updates.
	results := make(chan error, 4)
	for _, engine := range core.AllEngines() {
		go func() { results <- s.SetAgentEngineCapability(ctx, agent.ID, engine, true) }()
	}
	for range core.AllEngines() {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	assertCapabilities(core.AllEngines())
}

func TestEngineCapabilitiesMigrationPreservesExistingNodes(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	schema, err := testdb.IsolatePostgres(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(ctx)
	s, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	credential, err := s.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "legacy", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.EnrollAgent(ctx, core.EnrollRequest{Name: "legacy", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineXray}, PublicKey: base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t))}, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the exact v46 boundary: neither new column exists yet.
	if _, err := s.pool.Exec(ctx, `ALTER TABLE agents DROP COLUMN supported_capabilities;
		ALTER TABLE panel_settings DROP COLUMN default_agent_engines;
		ALTER TABLE tasks DROP COLUMN capability_transition;
		DELETE FROM qcontrolhub_schema_migrations;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(46)`); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	got, err := upgraded.GetAgent(ctx, agent.ID)
	want := []core.Engine{core.EngineXray}
	if err != nil || !reflect.DeepEqual(got.Capabilities, want) || !reflect.DeepEqual(got.SupportedCapabilities, want) {
		t.Fatalf("migration changed existing node: %+v %v", got, err)
	}
	settings, err := upgraded.PanelSettings(ctx)
	if err != nil || !reflect.DeepEqual(settings.DefaultAgentEngines, core.AllEngines()) {
		t.Fatalf("legacy global defaults: %+v %v", settings, err)
	}
	if err := upgraded.SetAgentEngineCapability(ctx, agent.ID, core.EngineXray, false); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.SetAgentEngineCapability(ctx, agent.ID, core.EngineXray, true); err != nil {
		t.Fatal(err)
	}
}

func TestEngineCapabilityRejectsUnsafeDiscovery(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	schema, err := testdb.IsolatePostgres(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(ctx)
	s, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	credential, err := s.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "unsafe-discovery", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.EnrollAgent(ctx, core.EnrollRequest{Name: "unsafe-discovery", OS: "linux", Arch: "amd64", Capabilities: core.AllEngines(), PublicKey: base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t))}, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		installed bool
		enabled   bool
	}{
		{"disable undiscovered active service", false, false},
		{"enable undiscovered active service", false, true},
		{"disable installed unsafe service", true, false},
		{"enable installed unsafe service", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := []core.Engine{core.EngineXray}
			if !test.enabled {
				selected = append(selected, core.EngineSingBox)
			}
			const reason = "wrapper 不在安全支持范围"
			runtime := map[core.Engine]core.RuntimeState{core.EngineSingBox: {
				Installed: test.installed, ServiceStatus: "active", ExistingConfigUnsupportedReason: reason,
			}}
			if _, err := s.pool.Exec(ctx, `UPDATE agents SET capabilities=$2,runtime=$3 WHERE id=$1`, agent.ID, selected, runtime); err != nil {
				t.Fatal(err)
			}
			change, err := s.ChangeAgentEngineCapability(ctx, agent.ID, core.EngineSingBox, test.enabled)
			if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), reason) {
				t.Errorf("unsafe discovery accepted: %+v, %v", change, err)
			}
			got, err := s.GetAgent(ctx, agent.ID)
			if err != nil || !reflect.DeepEqual(got.Capabilities, selected) || !reflect.DeepEqual(got.SupportedCapabilities, core.AllEngines()) || !reflect.DeepEqual(got.Runtime, runtime) {
				t.Fatalf("rejected switch changed node: %+v, %v", got, err)
			}
			var tasks int
			if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE agent_id=$1`, agent.ID).Scan(&tasks); err != nil || tasks != 0 {
				t.Fatalf("rejected switch created tasks: %d, %v", tasks, err)
			}
		})
	}
}

func TestEngineCapabilityServiceLifecycle(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	schema, err := testdb.IsolatePostgres(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(ctx)
	s, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	credential, err := s.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "lifecycle", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.EnrollAgent(ctx, core.EnrollRequest{Name: "lifecycle", OS: "linux", Arch: "amd64", Capabilities: core.AllEngines(), PublicKey: base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t))}, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	config, err := s.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Name: "saved", Engine: core.EngineMihomo, Content: "mixed-port: 7890"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Last known installed state survives offline periods; no live session is
	// needed to durably queue a transition.
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET runtime='{"mihomo":{"installed":true,"service_status":"active"}}' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	assertState := func(enabled bool, status core.TaskStatus) {
		t.Helper()
		got, err := s.GetAgent(ctx, agent.ID)
		if err != nil || containsEngine(got.Capabilities, core.EngineMihomo) != enabled || got.CapabilityTransitions[core.EngineMihomo].Status != status {
			t.Fatalf("state = %+v, %v; want enabled=%v status=%s", got, err, enabled, status)
		}
		items, err := s.ListAgents(ctx)
		if err != nil || len(items) != 1 || items[0].CapabilityTransitions[core.EngineMihomo].Status != status {
			t.Fatalf("list transition = %+v %v", items, err)
		}
	}
	stop, err := s.ChangeAgentEngineCapability(ctx, agent.ID, core.EngineMihomo, false)
	if err != nil || !stop.Enabled || stop.TaskID == "" {
		t.Fatalf("queue stop = %+v %v", stop, err)
	}
	select {
	case <-s.TaskReady(agent.ID):
	default:
		t.Fatal("Agent dispatch was not woken")
	}
	assertState(true, core.TaskPending)
	for _, enabled := range []bool{true, false} {
		if _, err := s.ChangeAgentEngineCapability(ctx, agent.ID, core.EngineMihomo, enabled); !errors.Is(err, ErrConflict) {
			t.Fatalf("overlapping switch accepted: %v", err)
		}
	}
	if _, err := s.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStart}); !errors.Is(err, ErrConflict) {
		t.Fatalf("competing start accepted: %v", err)
	}
	if _, err := s.SaveAgentConfig(ctx, config, config.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("competing config accepted: %v", err)
	}
	claimed, err := s.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != stop.TaskID || claimed.Action != core.ActionStop {
		t.Fatalf("claim stop = %+v %v", claimed, err)
	}
	assertState(true, core.TaskRunning)
	resumed, err := s.RunningTask(ctx, agent.ID)
	if err != nil || resumed == nil || resumed.LeaseID != claimed.LeaseID {
		t.Fatalf("reconnect = %+v %v", resumed, err)
	}
	if err := s.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: strings.Repeat("z", 48), Success: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale acknowledgement accepted: %v", err)
	}
	if err := s.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: false, Error: "stop failed"}); err != nil {
		t.Fatal(err)
	}
	assertState(true, core.TaskFailed)
	retry, err := s.RetryTask(ctx, stop.TaskID)
	if err != nil || retry.ID == stop.TaskID || retry.Action != core.ActionStop {
		t.Fatalf("retry stop = %+v %v", retry, err)
	}
	claimed, err = s.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim retry: %+v %v", claimed, err)
	}
	if err := s.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	assertState(false, core.TaskSucceeded)
	if _, err := s.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStart}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disabled core ordinary start accepted: %v", err)
	}
	start, err := s.ChangeAgentEngineCapability(ctx, agent.ID, core.EngineMihomo, true)
	if err != nil || start.Enabled || start.TaskID == "" {
		t.Fatalf("queue start = %+v %v", start, err)
	}
	assertState(false, core.TaskPending)
	if _, err := s.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStart}); !errors.Is(err, ErrConflict) {
		t.Fatalf("competing task during enable: %v", err)
	}
	if _, err := s.SaveAgentConfig(ctx, config, config.Version); !errors.Is(err, ErrConflict) {
		t.Fatalf("competing config during enable: %v", err)
	}
	if err := s.CancelTask(ctx, start.TaskID); err != nil {
		t.Fatal(err)
	}
	assertState(false, core.TaskCanceled)
	retry, err = s.RetryTask(ctx, start.TaskID)
	if err != nil || retry.Action != core.ActionStart {
		t.Fatalf("retry canceled start = %+v %v", retry, err)
	}
	claimed, err = s.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.Action != core.ActionStart {
		t.Fatalf("claim start = %+v %v", claimed, err)
	}
	if err := s.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: false, Error: "start failed"}); err != nil {
		t.Fatal(err)
	}
	assertState(false, core.TaskFailed)
	retry, err = s.RetryTask(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim retry start = %+v %v", claimed, err)
	}
	if err := s.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	assertState(true, core.TaskSucceeded)
	if got, err := s.AgentConfig(ctx, agent.ID, core.EngineMihomo); err != nil || got.ID != config.ID || got.Content != config.Content {
		t.Fatalf("saved config changed = %+v %v", got, err)
	}
	// Ordinary stop tasks must not change management eligibility.
	ordinary, err := s.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStop})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != ordinary.ID {
		t.Fatalf("claim ordinary stop = %+v %v", claimed, err)
	}
	if err := s.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	assertState(true, core.TaskSucceeded)
	// A queued start cannot reintroduce support removed by a re-enrollment.
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET capabilities='[]' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	start, err = s.ChangeAgentEngineCapability(ctx, agent.ID, core.EngineMihomo, true)
	if err != nil || start.TaskID == "" {
		t.Fatalf("queue narrowed-support start: %+v %v", start, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET supported_capabilities='["xray"]' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim narrowed-support start: %+v %v", claimed, err)
	}
	if err := s.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	assertState(false, core.TaskFailed)
}
