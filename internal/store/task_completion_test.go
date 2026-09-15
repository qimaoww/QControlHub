package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDeployAdvancesLatestDeploymentWithPostgreSQL(t *testing.T) {
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
		AgentID: agent.ID, Name: "deployment target", Engine: core.EngineMihomo,
		Content: "listeners: [{name: first, type: http, port: 7890}]\nmode: rule\nrules:\n  - MATCH,DIRECT\n",
	}, 0)
	if err != nil {
		t.Fatalf("save deployment configuration: %v", err)
	}
	completeDeploy := func() core.Task {
		t.Helper()
		created, createErr := dataStore.CreateTask(ctx, core.TaskRequest{
			AgentID: agent.ID, Action: core.ActionDeploy, Engine: core.EngineMihomo, ConfigID: config.ID,
		})
		if createErr != nil {
			t.Fatalf("create deployment task: %v", createErr)
		}
		claimed, claimErr := dataStore.ClaimTask(ctx, agent.ID)
		if claimErr != nil || claimed == nil || claimed.ID != created.ID {
			t.Fatalf("claim deployment task = %+v, %v", claimed, claimErr)
		}
		if completeErr := dataStore.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{
			LeaseID: claimed.LeaseID, Success: true, Output: "deployment result",
		}); completeErr != nil {
			t.Fatalf("complete deployment task: %v", completeErr)
		}
		stored, getErr := dataStore.GetTask(ctx, claimed.ID)
		if getErr != nil || stored.Status != core.TaskSucceeded {
			t.Fatalf("stored deployment task = %+v, %v", stored, getErr)
		}
		return stored
	}

	completeDeploy()
	deployments, err := dataStore.LatestDeployments(ctx)
	if err != nil {
		t.Fatalf("list deployments after deployment: %v", err)
	}
	real := completeDeploy()
	deployments, err = dataStore.LatestDeployments(ctx)
	if err != nil {
		t.Fatalf("list deployments after real completion: %v", err)
	}
	var actual core.Deployment
	for _, deployment := range deployments {
		if deployment.AgentID == agent.ID {
			actual = deployment
			break
		}
	}
	if actual.ConfigID != config.ID || actual.ConfigVersion != config.Version {
		t.Fatalf("deployment task %s deployment = %+v", real.ID, deployments)
	}
}
