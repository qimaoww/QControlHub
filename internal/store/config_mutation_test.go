package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestPresetMutationCommitsOrRollsBackTogether(t *testing.T) {
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
	db, err := OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("t", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	input := core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "atomic preset", Content: "listeners: [{name: a, port: 1080, type: socks}]\nrules: ['MATCH,DIRECT']\n"}
	metadata := &ConfigClientMetadataMutation{Tag: "a", Content: `{"client":"private"}`}
	options := ConfigMutationOptions{Action: core.ActionDeploy, ClientMetadata: metadata}
	// A task-side failure happens after INSERT/UPDATE of config + revision.
	// It must roll back both on the first save and on later revisions.
	if _, err := db.pool.Exec(ctx, `CREATE FUNCTION reject_preset_task() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test task insert failure'; END $$; CREATE TRIGGER reject_preset_task BEFORE INSERT ON tasks FOR EACH ROW EXECUTE FUNCTION reject_preset_task()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SaveAgentConfigAndTask(ctx, input, 0, options); err == nil {
		t.Fatal("task failure accepted")
	}
	if _, err := db.AgentConfig(ctx, agent.ID, input.Engine); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed initial save left a config: %v", err)
	}
	select {
	case <-db.TaskReady(agent.ID):
		t.Fatal("uncommitted task notified Agent")
	default:
	}
	if _, err := db.pool.Exec(ctx, `ALTER TABLE tasks DISABLE TRIGGER reject_preset_task`); err != nil {
		t.Fatal(err)
	}
	saved, task, err := db.SaveAgentConfigAndTask(ctx, input, 0, options)
	if err != nil || saved.Version != 1 || task.ConfigVersion != saved.Version || task.ConfigContent != saved.Content {
		t.Fatalf("atomic save: %+v %+v %v", saved, task, err)
	}
	// Frequent preset status polls must not read or return the execution log.
	log := strings.Repeat("task output\n", 10000)
	if _, err := db.pool.Exec(ctx, `UPDATE tasks SET output=$2 WHERE id=$1`, task.ID, log); err != nil {
		t.Fatal(err)
	}
	status, err := db.GetTaskState(ctx, task.ID)
	if err != nil || status.ID != task.ID || status.ConfigVersion != saved.Version || status.Status != task.Status || status.Output != "" {
		t.Fatalf("compact state: %+v %v", status, err)
	}
	full, err := db.GetTask(ctx, task.ID)
	if err != nil || full.Output != log {
		t.Fatal("compact polling changed the full execution log")
	}
	select {
	case <-db.TaskReady(agent.ID):
	default:
		t.Fatal("committed task did not notify Agent")
	}
	if _, err := db.pool.Exec(ctx, `ALTER TABLE tasks ENABLE TRIGGER reject_preset_task`); err != nil {
		t.Fatal(err)
	}
	input.Content = strings.ReplaceAll(input.Content, "1080", "2080")
	metadata.Content = `{"client":"changed"}`
	if _, _, err := db.SaveAgentConfigAndTask(ctx, input, 1, options); err == nil {
		t.Fatal("task failure accepted updated config")
	}
	current, err := db.AgentConfig(ctx, agent.ID, input.Engine)
	if err != nil || current.Version != 1 || current.Content != saved.Content {
		t.Fatalf("failed mutation changed config: %+v %v", current, err)
	}
	if _, err := db.ConfigRevision(ctx, saved.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed mutation left revision: %v", err)
	}
	got, err := db.ConfigClientMetadata(ctx, saved.ID, 1)
	if err != nil || got["a"] != `{"client":"private"}` {
		t.Fatalf("failed mutation changed metadata: %v %v", got, err)
	}
	select {
	case <-db.TaskReady(agent.ID):
		t.Fatal("failed update notified Agent")
	default:
	}
	if _, err := db.pool.Exec(ctx, `ALTER TABLE tasks DISABLE TRIGGER reject_preset_task`); err != nil {
		t.Fatal(err)
	}
	// Concurrent editors can only commit one revision and matching task.
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, _, err := db.SaveAgentConfigAndTask(ctx, input, 1, ConfigMutationOptions{Action: core.ActionDeploy})
			results <- err
		}()
	}
	ok, conflict := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			ok++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("concurrent save: %d success %d conflicts", ok, conflict)
	}
	t.Run("ss-rust policy and task snapshot", func(t *testing.T) {
		ssAgent, _ := enrollTaskTestAgent(t, ctx, db)
		if _, err := db.pool.Exec(ctx, `UPDATE agents SET capabilities='["ss-rust"]'::jsonb WHERE id=$1`, ssAgent.ID); err != nil {
			t.Fatal(err)
		}
		ssInput := core.Config{AgentID: ssAgent.ID, Engine: core.EngineShadowsocksRust, Name: "ss atomic", Content: `{"id":"one","server":"0.0.0.0","server_port":8388,"password":"test","method":"aes-256-gcm"}`}
		policy := core.MainlandAccessPolicy{AgentID: ssAgent.ID, Engine: ssInput.Engine, Tag: "one", Kind: "shadowsocks", Port: 8388, BlockMainlandSource: true}
		ssOptions := ConfigMutationOptions{Action: core.ActionDeploy, MainlandPolicies: []core.MainlandAccessPolicy{policy}}
		first, task, err := db.SaveAgentConfigAndTask(ctx, ssInput, 0, ssOptions)
		if err != nil || task.ConfigVersion != first.Version {
			t.Fatalf("first ss save: %v", err)
		}
		if _, err := db.pool.Exec(ctx, `ALTER TABLE tasks ENABLE TRIGGER reject_preset_task`); err != nil {
			t.Fatal(err)
		}
		ssInput.Content = strings.ReplaceAll(ssInput.Content, "8388", "8389")
		policy.Port = 8389
		ssOptions.MainlandPolicies = []core.MainlandAccessPolicy{policy}
		if _, _, err := db.SaveAgentConfigAndTask(ctx, ssInput, 1, ssOptions); err == nil {
			t.Fatal("task failure accepted ss policy update")
		}
		policies, err := db.ListMainlandAccessPolicies(ctx, ssAgent.ID)
		if err != nil || len(policies) != 1 || policies[0].Port != 8388 || policies[0].ConfigVersion != 1 {
			t.Fatalf("failed save changed policy: %+v %v", policies, err)
		}
		if _, err := db.pool.Exec(ctx, `ALTER TABLE tasks DISABLE TRIGGER reject_preset_task`); err != nil {
			t.Fatal(err)
		}
		second, task, err := db.SaveAgentConfigAndTask(ctx, ssInput, 1, ssOptions)
		if err != nil || second.Version != 2 || len(task.MainlandAccessPolicies) != 1 || task.MainlandAccessPolicies[0].Port != 8389 || task.MainlandAccessPolicies[0].ConfigVersion != 2 {
			t.Fatalf("task did not snapshot new policy: %+v %v", task.MainlandAccessPolicies, err)
		}
	})
}
