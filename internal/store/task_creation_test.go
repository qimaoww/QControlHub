package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCreateTaskCoalescesEquivalentActiveRequestsWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	config, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "coalesced task v1", Engine: core.EngineMihomo,
		Content: "listeners: [{name: first, type: http, port: 7890}]\nmode: rule\nrules:\n  - MATCH,DIRECT\n",
	}, 0)
	if err != nil {
		t.Fatalf("save initial configuration: %v", err)
	}
	baseline, err := dataStore.Overview(ctx)
	if err != nil {
		t.Fatalf("load baseline overview: %v", err)
	}

	request := core.TaskRequest{AgentID: agent.ID, Action: core.ActionValidate, Engine: core.EngineMihomo, ConfigID: config.ID}
	type outcome struct {
		task core.Task
		err  error
	}
	const writers = 8
	start := make(chan struct{})
	results := make(chan outcome, writers)
	var wait sync.WaitGroup
	for range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			task, createErr := dataStore.CreateTask(ctx, request)
			results <- outcome{task: task, err: createErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	firstID := ""
	created := 0
	reused := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("create equivalent task: %v", result.err)
		}
		if firstID == "" {
			firstID = result.task.ID
		} else if result.task.ID != firstID {
			t.Fatalf("equivalent task IDs differ: %s and %s", firstID, result.task.ID)
		}
		if result.task.Reused {
			reused++
		} else {
			created++
		}
	}
	if created != 1 || reused != writers-1 {
		t.Fatalf("equivalent task outcomes = %d created, %d reused; want 1 and %d", created, reused, writers-1)
	}
	assertTaskOverview(t, ctx, dataStore, baseline, 1, 1, 0)

	updated, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "coalesced task v2", Engine: core.EngineMihomo,
		Content: "listeners: [{name: second, type: http, port: 7891}]\nmode: rule\nrules:\n  - MATCH,DIRECT\n",
	}, config.Version)
	if err != nil {
		t.Fatalf("save updated configuration: %v", err)
	}
	versioned, err := dataStore.CreateTask(ctx, request)
	if err != nil || versioned.Reused || versioned.ID == firstID || versioned.ConfigVersion != updated.Version {
		t.Fatalf("new configuration version task = %+v, %v", versioned, err)
	}
	statusTask, err := dataStore.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionStatus, Engine: core.EngineMihomo})
	if err != nil || statusTask.Reused {
		t.Fatalf("create distinct status task = %+v, %v", statusTask, err)
	}
	statusDuplicate, err := dataStore.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionStatus, Engine: core.EngineMihomo})
	if err != nil || !statusDuplicate.Reused || statusDuplicate.ID != statusTask.ID {
		t.Fatalf("reuse status task = %+v, %v", statusDuplicate, err)
	}
	assertTaskOverview(t, ctx, dataStore, baseline, 3, 3, 0)
}

func TestCreateTaskValidatesCoreSourceWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()
	// legacyAgent advertises only self-upgrade, so it is an older Agent that
	// cannot negotiate core_source and must be refused the mirror task.
	legacyAgent, legacyEnrollment := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, legacyAgent.ID, legacyEnrollment)
	if _, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: legacyAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy Agent mirror task error = %v, want ErrConflict", err)
	}
	official, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: legacyAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceOfficial),
	})
	if err != nil || official.CoreSource != string(core.CoreSourceOfficial) {
		t.Fatalf("legacy official install task = %+v, %v", official, err)
	}
	defaulted, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: legacyAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment,
	})
	if err != nil || defaulted.CoreSource != string(core.CoreSourceOfficial) {
		t.Fatalf("legacy default install task = %+v, %v", defaulted, err)
	}
	if defaulted.ID != official.ID || !defaulted.Reused {
		t.Fatalf("omitted development install must reuse the explicit official task: %+v, want reuse of %s", defaulted, official.ID)
	}

	// sourceAgent advertises the negotiated feature and accepts mirror, which is
	// persisted and preserved on retry.
	sourceAgent, sourceEnrollment := enrollTaskTestAgentWithSource(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, sourceAgent.ID, sourceEnrollment)
	mirror, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: sourceAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	})
	if err != nil || mirror.CoreSource != string(core.CoreSourceMirror) {
		t.Fatalf("source Agent mirror install task = %+v, %v", mirror, err)
	}
	persisted, err := dataStore.GetTask(ctx, mirror.ID)
	if err != nil || persisted.CoreSource != string(core.CoreSourceMirror) {
		t.Fatalf("persisted mirror source = %+v, %v", persisted, err)
	}
	if err := dataStore.CancelTask(ctx, mirror.ID); err != nil {
		t.Fatalf("cancel mirror task: %v", err)
	}
	retried, err := dataStore.RetryTask(ctx, mirror.ID)
	if err != nil || retried.CoreSource != string(core.CoreSourceMirror) {
		t.Fatalf("retried mirror task = %+v, %v", retried, err)
	}

	invalidRequests := []core.TaskRequest{
		{AgentID: sourceAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionStable, CoreSource: string(core.CoreSourceMirror)},
		{AgentID: sourceAgent.ID, Action: core.ActionInstall, Engine: core.EngineXray, CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror)},
		{AgentID: sourceAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionDevelopment, CoreSource: "private"},
		{AgentID: legacyAgent.ID, Action: core.ActionStatus, Engine: core.EngineMihomo, CoreSource: string(core.CoreSourceMirror)},
	}
	for _, request := range invalidRequests {
		if _, err := dataStore.CreateTask(ctx, request); !errors.Is(err, ErrInvalid) {
			t.Fatalf("CreateTask(%+v) error = %v, want ErrInvalid", request, err)
		}
	}
}

func TestUninstallTaskRequiresNegotiatedAgentFeatureWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	request := core.TaskRequest{AgentID: agent.ID, Action: core.ActionUninstall, Engine: core.EngineMihomo}
	if _, err := dataStore.CreateTask(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("old Agent uninstall error = %v, want conflict", err)
	}
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{
		Features: []string{core.AgentFeatureCoreUninstall},
		Runtime:  map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}},
	}); err != nil {
		t.Fatal(err)
	}
	transition, err := dataStore.ChangeAgentEngineCapability(ctx, agent.ID, core.EngineMihomo, false)
	if err != nil || transition.TaskID == "" {
		t.Fatalf("disable installed capability = %+v, %v", transition, err)
	}
	stop, err := dataStore.ClaimTask(ctx, agent.ID)
	if err != nil || stop == nil || stop.ID != transition.TaskID || stop.Action != core.ActionStop {
		t.Fatalf("claim capability stop = %+v, %v", stop, err)
	}
	if err := dataStore.CompleteTask(ctx, agent.ID, stop.ID, core.TaskResultRequest{LeaseID: stop.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	disabled, err := dataStore.GetAgent(ctx, agent.ID)
	if err != nil || containsEngine(disabled.Capabilities, core.EngineMihomo) || !containsEngine(disabled.SupportedCapabilities, core.EngineMihomo) {
		t.Fatalf("disabled installed Agent = %+v, %v", disabled, err)
	}
	invalid := request
	invalid.CoreVersion = core.CoreVersionStable
	if _, err := dataStore.CreateTask(ctx, invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("versioned uninstall error = %v, want invalid", err)
	}
	created, err := dataStore.CreateTask(ctx, request)
	if err != nil || created.Action != core.ActionUninstall {
		t.Fatalf("create uninstall task = %+v, %v", created, err)
	}
	claimed, err := dataStore.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != created.ID || claimed.Action != core.ActionUninstall {
		t.Fatalf("claim uninstall task = %+v, %v", claimed, err)
	}
	if err := dataStore.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	completed, err := dataStore.GetTask(ctx, created.ID)
	if err != nil || completed.Status != core.TaskSucceeded {
		t.Fatalf("completed uninstall task = %+v, %v", completed, err)
	}
	user, err := dataStore.CreateUser(ctx, core.UserRequest{
		Username: fmt.Sprintf("uninstall-share-%d", time.Now().UnixNano()),
		Role:     core.RoleUser, Permissions: core.GrantablePermissions(),
	}, "test-only-hash")
	if err != nil {
		t.Fatal(err)
	}
	shareID, err := core.NewID("shr")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = dataStore.pool.Exec(cleanupContext, `DELETE FROM agent_shares WHERE id=$1`, shareID)
		_, _ = dataStore.pool.Exec(cleanupContext, `DELETE FROM panel_users WHERE id=$1`, user.ID)
	}()
	if _, err := dataStore.pool.Exec(ctx, `INSERT INTO agent_shares(id,user_id,agent_id,enabled,status,engines)
		VALUES ($1,$2,$3,true,'pending',ARRAY['mihomo']::text[])`, shareID, user.ID, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateTask(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("pending enabled share uninstall error = %v, want conflict", err)
	}
	if _, err := dataStore.pool.Exec(ctx, `DELETE FROM agent_shares WHERE id=$1`, shareID); err != nil {
		t.Fatal(err)
	}
	pending, err := dataStore.CreateTask(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{}); err != nil {
		t.Fatal(err)
	}
	if claimed, err := dataStore.ClaimTask(ctx, agent.ID); err != nil || claimed != nil {
		t.Fatalf("downgraded Agent claimed uninstall task = %+v, %v", claimed, err)
	}
	canceled, err := dataStore.GetTask(ctx, pending.ID)
	if err != nil || canceled.Status != core.TaskCanceled {
		t.Fatalf("downgraded Agent task = %+v, %v", canceled, err)
	}
}

func TestEffectiveSourceReuseWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	// An explicit official request must reuse an existing legacy row whose
	// core_source is empty (NULL), i.e. a legacy omitted-source install.
	legacy, legacyEnrollment := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, legacy.ID, legacyEnrollment)
	legacyID, err := core.NewID("tsk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.pool.Exec(ctx, `
		INSERT INTO tasks (id,agent_id,action,engine,core_version,status,created_at)
		VALUES ($1,$2,'install','mihomo','development','pending',now())`, legacyID, legacy.ID); err != nil {
		t.Fatalf("insert legacy empty-source task: %v", err)
	}
	official, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: legacy.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceOfficial),
	})
	if err != nil || official.ID != legacyID || !official.Reused {
		t.Fatalf("explicit official must reuse legacy empty-source task: %+v, %v; want reuse of %s", official, err, legacyID)
	}

	// An omitted source request must reuse an existing explicit-official row.
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	explicit, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceOfficial),
	})
	if err != nil {
		t.Fatalf("create explicit official task: %v", err)
	}
	omitted, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment,
	})
	if err != nil || omitted.ID != explicit.ID || !omitted.Reused || omitted.CoreSource != string(core.CoreSourceOfficial) {
		t.Fatalf("omitted source must reuse explicit official task: %+v, %v; want reuse of %s", omitted, err, explicit.ID)
	}
}
