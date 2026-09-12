package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTaskSubmissionRetryAndDispatchRequireIndependentEgress(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	config, err := db.CreateConfig(ctx, core.Config{Name: "unsafe-route", Engine: core.EngineMihomo,
		Content: "mode: direct\nlisteners: [{name: a, type: http, port: 21001}]\n"})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		_, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: action})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "独立出口校验失败") {
			t.Fatalf("%s accepted a shared process-wide exit: %v", action, err)
		}
	}
	// Simulate durable work created by an older control plane. Retry and
	// reconnect must not bypass the new compiler using an old task snapshot.
	for _, status := range []core.TaskStatus{core.TaskFailed, core.TaskPending, core.TaskRunning} {
		id, err := core.NewID("tsk")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.pool.Exec(ctx, `INSERT INTO tasks
			(id,agent_id,engine,action,status,config_id,config_version,config_content,created_at,started_at,lease_id)
			VALUES($1,$2,$3,'deploy',$4,$5,1,$6,now(),now(),'test-existing-lease')`,
			id, agent.ID, config.Engine, status, config.ID, config.Content); err != nil {
			t.Fatal(err)
		}
		switch status {
		case core.TaskFailed:
			if _, err := db.RetryTask(ctx, id); !errors.Is(err, ErrInvalid) {
				t.Fatalf("retry bypassed independent exits: %v", err)
			}
		case core.TaskPending:
			if task, err := db.ClaimTask(ctx, agent.ID); err != nil || task != nil {
				t.Fatalf("legacy queued config dispatched: %+v %v", task, err)
			}
		case core.TaskRunning:
			if task, err := db.RunningTask(ctx, agent.ID); err != nil || task != nil {
				t.Fatalf("legacy running config resumed: %+v %v", task, err)
			}
		}
	}
}

func TestIndependentEgressNegotiatesAgentSupport(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	config, err := db.CreateConfig(ctx, core.Config{Name: "independent", Engine: core.EngineMihomo,
		Content: "listeners: [{name: a, type: http, port: 21001}]\n"})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		if _, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: action}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: []string{core.AgentFeatureSharedTraffic}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionValidate}); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy Agent accepted config task: %v", err)
	}
	if task, err := db.ClaimTask(ctx, agent.ID); err != nil || task != nil {
		t.Fatalf("downgraded Agent received pending config: %+v %v", task, err)
	}
}
