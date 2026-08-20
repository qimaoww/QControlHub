package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestWSSAgentLifecycleWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := store.Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	adminToken := strings.Repeat("a", 48)
	httpServer := httptest.NewServer(New(dataStore, Config{
		AdminToken:   adminToken,
		AgentBinary:  []byte("test-agent-binary"),
		AgentVersion: "test-version",
	}).Handler())
	defer httpServer.Close()

	enrollment, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "integration", TTLMinutes: 5, MaxUses: 1})
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	var enrolledAgentID string
	t.Cleanup(func() {
		var agentIDs []string
		if enrolledAgentID != "" {
			agentIDs = append(agentIDs, enrolledAgentID)
		}
		cleanupTaskAPIFixture(t, databaseURL, enrollment.ID, agentIDs)
	})
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	enrollmentBody, _ := json.Marshal(core.EnrollRequest{
		Name: "integration-agent", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: authn.EncodePublicKey(publicKey),
	})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/agent/v1/enroll", bytes.NewReader(enrollmentBody))
	request.Header.Set("Authorization", "Bearer "+enrollment.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("enroll request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("enroll status = %s", response.Status)
	}
	var enrolled core.EnrollResponse
	if err := json.NewDecoder(response.Body).Decode(&enrolled); err != nil {
		t.Fatalf("decode enrollment: %v", err)
	}
	enrolledAgentID = enrolled.AgentID

	// An enrolled Agent can fetch the control-plane's exact binary only with
	// its own fresh Ed25519 request signature. The response is checksummed so
	// the Agent can verify it before replacing its executable.
	binaryRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/agent/v1/binary", nil)
	if err != nil {
		t.Fatalf("create signed binary request: %v", err)
	}
	if err := authn.SignRequest(binaryRequest, nil, enrolled.AgentID, privateKey, time.Now().UTC()); err != nil {
		t.Fatalf("sign binary request: %v", err)
	}
	binaryResponse, err := http.DefaultClient.Do(binaryRequest)
	if err != nil {
		t.Fatalf("download signed Agent binary: %v", err)
	}
	defer binaryResponse.Body.Close()
	if binaryResponse.StatusCode != http.StatusOK {
		t.Fatalf("signed Agent binary status = %s", binaryResponse.Status)
	}
	binaryContents, err := io.ReadAll(binaryResponse.Body)
	if err != nil {
		t.Fatalf("read signed Agent binary: %v", err)
	}
	if string(binaryContents) != "test-agent-binary" || binaryResponse.Header.Get("X-QControlHub-Agent-Version") != "test-version" || binaryResponse.Header.Get("X-QControlHub-Agent-SHA256") == "" {
		t.Fatalf("signed Agent binary response = body %q headers version=%q checksum=%q", string(binaryContents), binaryResponse.Header.Get("X-QControlHub-Agent-Version"), binaryResponse.Header.Get("X-QControlHub-Agent-SHA256"))
	}

	websocketURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/agent/v1/connect"
	handshake, _ := http.NewRequestWithContext(ctx, http.MethodGet, websocketURL, nil)
	if err := authn.SignRequest(handshake, nil, enrolled.AgentID, privateKey, time.Now().UTC()); err != nil {
		t.Fatalf("sign WSS handshake: %v", err)
	}
	connection, dialResponse, err := websocket.Dial(ctx, websocketURL, &websocket.DialOptions{
		HTTPHeader: handshake.Header, Subprotocols: []string{"qcontrolhub.agent.v1"},
	})
	if err != nil {
		if dialResponse != nil {
			t.Fatalf("dial WSS: %v (%s)", err, dialResponse.Status)
		}
		t.Fatalf("dial WSS: %v", err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test complete")
	var hello core.WireMessage
	if err := wsjson.Read(ctx, connection, &hello); err != nil || hello.Type != core.WireHello {
		t.Fatalf("read hello: message=%+v error=%v", hello, err)
	}
	heartbeat := core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
		Version: "test", Metrics: &core.HostMetrics{
			CPUAvailable: true, CPUPercent: 12.5,
			MemoryAvailable: true, MemoryUsedBytes: 2 << 30, MemoryTotalBytes: 4 << 30,
			DiskAvailable: true, DiskUsedBytes: 8 << 30, DiskTotalBytes: 16 << 30,
			NetworkAvailable: true, NetworkRXBytes: 1000, NetworkTXBytes: 500, NetworkRXBPS: 100, NetworkTXBPS: 50,
			NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.168.31.205"}}},
		},
	}}
	if err := wsjson.Write(ctx, connection, heartbeat); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	config, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: enrolled.AgentID, Name: "integration node configuration", Engine: core.EngineMihomo,
		Content: "mixed-port: 7890\nproxies: []\nproxy-groups: []\nrules:\n  - MATCH,DIRECT\n",
	}, 0)
	if err != nil {
		t.Fatalf("create agent config: %v", err)
	}
	queued, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: enrolled.AgentID, Action: core.ActionValidate, Engine: core.EngineMihomo, ConfigID: config.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	var taskMessage core.WireMessage
	if err := wsjson.Read(ctx, connection, &taskMessage); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if taskMessage.Type != core.WireTask || taskMessage.Task == nil || taskMessage.Task.ID != queued.ID || taskMessage.Task.LeaseID == "" {
		t.Fatalf("invalid task message: %+v", taskMessage)
	}
	result := core.WireMessage{Type: core.WireResult, Result: &core.TaskResultEnvelope{
		TaskID: queued.ID,
		Result: core.TaskResultRequest{LeaseID: taskMessage.Task.LeaseID, Success: true, Output: "validated"},
	}}
	if err := wsjson.Write(ctx, connection, result); err != nil {
		t.Fatalf("write result: %v", err)
	}
	var acknowledgment core.WireMessage
	if err := wsjson.Read(ctx, connection, &acknowledgment); err != nil {
		t.Fatalf("read result acknowledgment: %v", err)
	}
	if acknowledgment.Type != core.WireResultAck || acknowledgment.TaskID != queued.ID {
		t.Fatalf("invalid result acknowledgment: %+v", acknowledgment)
	}

	resumable, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: enrolled.AgentID, Action: core.ActionStatus, Engine: core.EngineMihomo,
	})
	if err != nil {
		t.Fatalf("create resumable task: %v", err)
	}
	var originalLease core.WireMessage
	if err := wsjson.Read(ctx, connection, &originalLease); err != nil || originalLease.Task == nil || originalLease.Task.ID != resumable.ID {
		t.Fatalf("read resumable task: message=%+v error=%v", originalLease, err)
	}
	if err := connection.Close(websocket.StatusGoingAway, "simulate transient disconnect"); err != nil {
		t.Fatalf("close first WSS session: %v", err)
	}

	resumedHandshake, _ := http.NewRequestWithContext(ctx, http.MethodGet, websocketURL, nil)
	if err := authn.SignRequest(resumedHandshake, nil, enrolled.AgentID, privateKey, time.Now().UTC()); err != nil {
		t.Fatalf("sign resumed WSS handshake: %v", err)
	}
	resumedConnection, resumedResponse, err := websocket.Dial(ctx, websocketURL, &websocket.DialOptions{
		HTTPHeader: resumedHandshake.Header, Subprotocols: []string{"qcontrolhub.agent.v1"},
	})
	if err != nil {
		if resumedResponse != nil {
			t.Fatalf("dial resumed WSS: %v (%s)", err, resumedResponse.Status)
		}
		t.Fatalf("dial resumed WSS: %v", err)
	}
	defer resumedConnection.Close(websocket.StatusNormalClosure, "test complete")
	var resumedHello core.WireMessage
	if err := wsjson.Read(ctx, resumedConnection, &resumedHello); err != nil || resumedHello.Type != core.WireHello {
		t.Fatalf("read resumed hello: message=%+v error=%v", resumedHello, err)
	}
	var resumedTask core.WireMessage
	if err := wsjson.Read(ctx, resumedConnection, &resumedTask); err != nil {
		t.Fatalf("read resumed task without stale-lease delay: %v", err)
	}
	if resumedTask.Task == nil || resumedTask.Task.ID != resumable.ID || resumedTask.Task.LeaseID != originalLease.Task.LeaseID || resumedTask.Task.Attempt != originalLease.Task.Attempt {
		t.Fatalf("resumed task changed lease or attempt: original=%+v resumed=%+v", originalLease.Task, resumedTask.Task)
	}
	resumedResult := core.WireMessage{Type: core.WireResult, Result: &core.TaskResultEnvelope{
		TaskID: resumable.ID,
		Result: core.TaskResultRequest{LeaseID: resumedTask.Task.LeaseID, Success: true, Output: "resumed status"},
	}}
	if err := wsjson.Write(ctx, resumedConnection, resumedResult); err != nil {
		t.Fatalf("write resumed result: %v", err)
	}
	var resumedAcknowledgment core.WireMessage
	if err := wsjson.Read(ctx, resumedConnection, &resumedAcknowledgment); err != nil || resumedAcknowledgment.TaskID != resumable.ID {
		t.Fatalf("read resumed acknowledgment: message=%+v error=%v", resumedAcknowledgment, err)
	}
	completedResumed, err := dataStore.GetTask(ctx, resumable.ID)
	if err != nil || completedResumed.Status != core.TaskSucceeded || completedResumed.Attempt != 1 {
		t.Fatalf("resumed task completion = %+v, %v", completedResumed, err)
	}
	connection = resumedConnection
	installTask, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: enrolled.AgentID, Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionDevelopment,
	})
	if err != nil {
		t.Fatalf("create core install task: %v", err)
	}
	taskMessage = core.WireMessage{}
	if err := wsjson.Read(ctx, connection, &taskMessage); err != nil {
		t.Fatalf("read core install task: %v", err)
	}
	if taskMessage.Task == nil || taskMessage.Task.ID != installTask.ID || taskMessage.Task.CoreVersion != core.CoreVersionDevelopment || taskMessage.Task.ConfigContent != "" {
		t.Fatalf("invalid core install task message: %+v", taskMessage)
	}
	result = core.WireMessage{Type: core.WireResult, Result: &core.TaskResultEnvelope{
		TaskID: installTask.ID,
		Result: core.TaskResultRequest{LeaseID: taskMessage.Task.LeaseID, Success: true, Output: "installed"},
	}}
	if err := wsjson.Write(ctx, connection, result); err != nil {
		t.Fatalf("write core install result: %v", err)
	}
	acknowledgment = core.WireMessage{}
	if err := wsjson.Read(ctx, connection, &acknowledgment); err != nil || acknowledgment.TaskID != installTask.ID {
		t.Fatalf("read core install acknowledgment: message=%+v error=%v", acknowledgment, err)
	}
	tasks, err := dataStore.ListTasks(ctx, enrolled.AgentID, 10)
	if err != nil || len(tasks) < 2 || tasks[0].Status != core.TaskSucceeded || tasks[0].CoreVersion != core.CoreVersionDevelopment {
		t.Fatalf("completed task not persisted: tasks=%+v error=%v", tasks, err)
	}
	for _, action := range []core.Action{core.ActionDeploy, core.ActionReadConfig, core.ActionStart, core.ActionStop, core.ActionRestart} {
		request := core.TaskRequest{AgentID: enrolled.AgentID, Action: action, Engine: core.EngineMihomo}
		if action == core.ActionDeploy {
			request.ConfigID = config.ID
		}
		created, createErr := dataStore.CreateTask(ctx, request)
		if createErr != nil {
			t.Fatalf("create %s task: %v", action, createErr)
		}
		var dispatched core.WireMessage
		if readErr := wsjson.Read(ctx, connection, &dispatched); readErr != nil || dispatched.Task == nil || dispatched.Task.ID != created.ID || dispatched.Task.Action != action {
			t.Fatalf("read %s task: message=%+v error=%v", action, dispatched, readErr)
		}
		output := "completed " + string(action)
		if action == core.ActionReadConfig {
			output = config.Content
		}
		completed := core.WireMessage{Type: core.WireResult, Result: &core.TaskResultEnvelope{
			TaskID: created.ID,
			Result: core.TaskResultRequest{LeaseID: dispatched.Task.LeaseID, Success: true, Output: output},
		}}
		if writeErr := wsjson.Write(ctx, connection, completed); writeErr != nil {
			t.Fatalf("write %s result: %v", action, writeErr)
		}
		var ack core.WireMessage
		if readErr := wsjson.Read(ctx, connection, &ack); readErr != nil || ack.TaskID != created.ID {
			t.Fatalf("read %s acknowledgment: message=%+v error=%v", action, ack, readErr)
		}
		stored, getErr := dataStore.GetTask(ctx, created.ID)
		if getErr != nil || stored.Status != core.TaskSucceeded {
			t.Fatalf("stored %s task = %+v, %v", action, stored, getErr)
		}
		if action == core.ActionReadConfig {
			snapshot, snapshotErr := dataStore.ReadTaskConfigSnapshot(ctx, created.ID, enrolled.AgentID, core.EngineMihomo)
			if snapshotErr != nil || snapshot != config.Content {
				t.Fatalf("read-config snapshot = %q, %v", snapshot, snapshotErr)
			}
		}
	}
	deployments, err := dataStore.LatestDeployments(ctx)
	if err != nil {
		t.Fatalf("list deployments after task matrix: %v", err)
	}
	foundDeployment := false
	for _, deployment := range deployments {
		if deployment.AgentID == enrolled.AgentID {
			foundDeployment = true
			break
		}
	}
	if !foundDeployment {
		t.Fatal("successful deployment task did not update the latest deployment")
	}
	agent, err := dataStore.GetAgent(ctx, enrolled.AgentID)
	if err != nil || agent.Metrics.CollectedAt.IsZero() || agent.Metrics.CPUPercent != 12.5 || agent.Metrics.NetworkRXBPS != 100 || len(agent.Metrics.NetworkInterfaces) != 1 {
		t.Fatalf("heartbeat metrics not persisted: agent=%+v error=%v", agent, err)
	}
	revokedTask, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: enrolled.AgentID, Action: core.ActionStatus, Engine: core.EngineMihomo,
	})
	if err != nil {
		t.Fatalf("create task before agent revocation: %v", err)
	}

	deleteRequest, _ := http.NewRequestWithContext(ctx, http.MethodDelete, httpServer.URL+"/api/v1/agents/"+enrolled.AgentID, nil)
	deleteRequest.Header.Set("Authorization", "Bearer "+adminToken)
	deleteResponse, err := http.DefaultClient.Do(deleteRequest)
	if err != nil {
		t.Fatalf("delete connected agent: %v", err)
	}
	deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete connected agent status = %s", deleteResponse.Status)
	}
	disconnectContext, cancelDisconnect := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelDisconnect()
	for {
		var afterRevocation core.WireMessage
		if err := wsjson.Read(disconnectContext, connection, &afterRevocation); err != nil {
			break
		}
	}
	if errors.Is(disconnectContext.Err(), context.DeadlineExceeded) {
		t.Fatal("revoked Agent WSS was not closed promptly")
	}
	if _, err := dataStore.GetAgent(ctx, enrolled.AgentID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked agent remains queryable: %v", err)
	}
	terminatedTask, err := dataStore.GetTask(ctx, revokedTask.ID)
	if err != nil || terminatedTask.Status != core.TaskFailed || terminatedTask.Error != "agent identity was revoked" || terminatedTask.FinishedAt == nil {
		t.Fatalf("task after agent revocation = %+v, %v", terminatedTask, err)
	}
	if _, err := dataStore.AgentConfig(ctx, enrolled.AgentID, core.EngineMihomo); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked agent configuration remains active: %v", err)
	}
	if _, err := dataStore.ConfigRevision(ctx, config.ID, config.Version); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked agent configuration history remains available: %v", err)
	}

	rejectedHandshake, _ := http.NewRequestWithContext(ctx, http.MethodGet, websocketURL, nil)
	if err := authn.SignRequest(rejectedHandshake, nil, enrolled.AgentID, privateKey, time.Now().UTC()); err != nil {
		t.Fatalf("sign revoked WSS handshake: %v", err)
	}
	rejectedConnection, rejectedResponse, err := websocket.Dial(ctx, websocketURL, &websocket.DialOptions{
		HTTPHeader: rejectedHandshake.Header, Subprotocols: []string{"qcontrolhub.agent.v1"},
	})
	if rejectedConnection != nil {
		rejectedConnection.CloseNow()
	}
	if err == nil || rejectedResponse == nil || rejectedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked Agent reconnect = connection=%v response=%v error=%v", rejectedConnection, rejectedResponse, err)
	}
	rejectedResponse.Body.Close()
}
