package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/jackc/pgx/v5"
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
	auditorToken := strings.Repeat("u", 48)
	tasksReadToken := strings.Repeat("r", 48)
	configSnapshotReadToken := strings.Repeat("c", 48)
	trustedProxies, err := authn.ParseTrustedProxies([]string{"127.0.0.1/32"})
	if err != nil {
		t.Fatalf("parse trusted proxy fixture: %v", err)
	}
	apiServer := New(dataStore, Config{
		AdminToken:     adminToken,
		AuditorTokens:  []string{auditorToken},
		TrustedProxies: trustedProxies,
		AgentBinary:    []byte("test-agent-binary"),
		AgentVersion:   "test-version",
	})
	apiServer.roleTokens[sha256.Sum256([]byte(tasksReadToken))] = tokenPrincipal{
		Role: core.RoleUser, Permissions: []core.Permission{core.PermissionTasksRead},
	}
	apiServer.roleTokens[sha256.Sum256([]byte(configSnapshotReadToken))] = tokenPrincipal{
		Role: core.RoleUser,
		Permissions: []core.Permission{
			core.PermissionTasksRead,
			core.PermissionAgentConfigRead,
		},
	}
	httpServer := httptest.NewServer(apiServer.Handler())
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
		Capabilities: []core.Engine{core.EngineMihomo},
		Features:     []string{core.AgentFeatureSelfUpgrade, core.AgentFeaturePortTraffic, core.AgentFeatureMihomoDevelopmentSource},
		PublicKey:    authn.EncodePublicKey(publicKey),
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
	trafficPolicy, err := dataStore.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{
		AgentID: enrolled.AgentID, Name: "integration port", Engine: core.EngineMihomo,
		Port: 24443, Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly,
		CycleAnchor: core.UTCDate(time.Now().UTC()), LimitBytes: 100 << 30,
	})
	if err != nil {
		t.Fatalf("create traffic policy: %v", err)
	}

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
	handshake.Header.Set("X-Forwarded-For", "2001:4860:4860:0:0:0:0:8888")
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
	if len(hello.TrafficPolicies) != 1 || hello.TrafficPolicies[0].ID != trafficPolicy.ID {
		t.Fatalf("hello traffic policies = %+v", hello.TrafficPolicies)
	}
	connectedAgent, err := dataStore.GetAgent(ctx, enrolled.AgentID)
	if err != nil || connectedAgent.Metrics.ObservedPublicIP != "2001:4860:4860::8888" {
		t.Fatalf("trusted WSS public source was not normalized and stored: agent=%+v error=%v", connectedAgent, err)
	}
	periodStart, periodEnd, err := core.TrafficPeriodAt(trafficPolicy.CycleAnchor, trafficPolicy.Cycle, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
		Version: "test", Features: []string{core.AgentFeatureSelfUpgrade, core.AgentFeaturePortTraffic, core.AgentFeatureCoreLogs, core.AgentFeatureCoreLogStatus, core.AgentFeatureMihomoDevelopmentSource},
		Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true, ServiceStatus: "active", CoreLogStatus: "waiting", CoreLogError: "source-missing"}},
		TrafficUsage: []core.PortTrafficUsage{{
			PolicyID: trafficPolicy.ID, ResetGeneration: trafficPolicy.ResetGeneration,
			ReceivedBytes: 2048, SentBytes: 1024, UsedBytes: 3072,
			ReceiveBPS: 128, SendBPS: 64, PeriodStart: periodStart, PeriodEnd: periodEnd,
			EnforcementAvailable: true,
		}}, Metrics: &core.HostMetrics{
			CPUAvailable: true, CPUPercent: 12.5,
			MemoryAvailable: true, MemoryUsedBytes: 2 << 30, MemoryTotalBytes: 4 << 30,
			DiskAvailable: true, DiskUsedBytes: 8 << 30, DiskTotalBytes: 16 << 30,
			NetworkAvailable: true, NetworkRXBytes: 1000, NetworkTXBytes: 500, NetworkRXBPS: 100, NetworkTXBPS: 50,
			NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"198.35.26.96"}}},
			ObservedPublicIP:  "203.0.113.99",
		},
	}}
	if err := wsjson.Write(ctx, connection, heartbeat); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	for attempt := 0; attempt < 50; attempt++ {
		policies, listErr := dataStore.AgentPortTrafficPolicies(ctx, enrolled.AgentID)
		if listErr == nil && len(policies) == 1 && policies[0].UsedBytes == 3072 && policies[0].EnforcementAvailable {
			break
		}
		if attempt == 49 {
			t.Fatalf("traffic heartbeat was not stored: policies=%+v error=%v", policies, listErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A high-frequency metrics-only push must refresh the live snapshot
	// without clobbering version, runtime, or features from the heartbeat.
	metricsPush := core.WireMessage{Type: core.WireMetrics, Metrics: &core.HostMetrics{
		CPUAvailable: true, CPUPercent: 42.5,
		MemoryAvailable: true, MemoryUsedBytes: 1 << 30, MemoryTotalBytes: 4 << 30,
		DiskAvailable: true, DiskUsedBytes: 2 << 30, DiskTotalBytes: 16 << 30,
		NetworkAvailable: true, NetworkRXBytes: 2000, NetworkTXBytes: 900, NetworkRXBPS: 300, NetworkTXBPS: 120,
		ObservedPublicIP: "10.0.0.8",
	}}
	if err := wsjson.Write(ctx, connection, metricsPush); err != nil {
		t.Fatalf("write metrics push: %v", err)
	}
	for attempt := 0; attempt < 50; attempt++ {
		pushed, pushErr := dataStore.GetAgent(ctx, enrolled.AgentID)
		if pushErr == nil && pushed.Metrics.CPUPercent == 42.5 && pushed.Metrics.NetworkRXBPS == 300 {
			if pushed.Version != "test" || len(pushed.Features) == 0 || pushed.Runtime[core.EngineSingBox].CoreLogStatus != "waiting" {
				t.Fatalf("metrics push clobbered heartbeat state: agent=%+v", pushed)
			}
			if pushed.Metrics.ObservedPublicIP != "2001:4860:4860::8888" || len(pushed.Metrics.NetworkInterfaces) != 1 || pushed.Metrics.NetworkInterfaces[0].Addresses[0] != "198.35.26.96" {
				t.Fatalf("metrics push did not preserve server-observed and last usable address state: %+v", pushed.Metrics)
			}
			break
		}
		if attempt == 49 {
			t.Fatalf("metrics push was not stored: agent=%+v error=%v", pushed, pushErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	logBatch := core.CoreLogBatch{ID: "log_0123456789abcdef", Entries: []core.CoreLogEntry{{
		Engine: core.EngineMihomo, Level: "info", Message: "integration core log", LoggedAt: time.Now().UTC(),
	}}}
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireCoreLogs, CoreLogs: &logBatch}); err != nil {
		t.Fatalf("write core log batch: %v", err)
	}
	var logAcknowledgment core.WireMessage
	if err := wsjson.Read(ctx, connection, &logAcknowledgment); err != nil || logAcknowledgment.Type != core.WireCoreLogsAck || logAcknowledgment.BatchID != logBatch.ID {
		t.Fatalf("core log acknowledgment = %+v, %v", logAcknowledgment, err)
	}
	storedLogs, err := dataStore.ListCoreLogs(ctx, store.CoreLogQuery{AgentID: enrolled.AgentID, Limit: 10})
	if err != nil || len(storedLogs) != 1 || storedLogs[0].Message != logBatch.Entries[0].Message {
		t.Fatalf("stored core logs = %+v, %v", storedLogs, err)
	}
	largeBatch := core.CoreLogBatch{ID: "log_fedcba9876543210", Entries: make([]core.CoreLogEntry, core.MaxCoreLogBatchEntries)}
	for index := range largeBatch.Entries {
		largeBatch.Entries[index] = core.CoreLogEntry{
			Engine: core.EngineXray, Level: "debug", Message: strings.Repeat("\x01", core.MaxCoreLogMessageBytes), LoggedAt: time.Now().UTC(),
		}
	}
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireCoreLogs, CoreLogs: &largeBatch}); err != nil {
		t.Fatalf("write maximum encoded core log batch: %v", err)
	}
	if err := wsjson.Read(ctx, connection, &logAcknowledgment); err != nil || logAcknowledgment.Type != core.WireCoreLogsAck || logAcknowledgment.BatchID != largeBatch.ID {
		t.Fatalf("maximum core log acknowledgment = %+v, %v", logAcknowledgment, err)
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
	resumedHandshake.Header.Set("X-Forwarded-For", "93.184.216.34")
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
	reconnectedAgent, err := dataStore.GetAgent(ctx, enrolled.AgentID)
	if err != nil || reconnectedAgent.Metrics.ObservedPublicIP != "93.184.216.34" {
		t.Fatalf("reconnected WSS public source did not replace the previous observation: agent=%+v error=%v", reconnectedAgent, err)
	}
	// Dispatch is gated until this connection's first heartbeat is persisted so
	// the resumed features are authoritative; send it before resuming.
	if err := wsjson.Write(ctx, resumedConnection, core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
		Version:  "test",
		Features: []string{core.AgentFeatureSelfUpgrade, core.AgentFeaturePortTraffic, core.AgentFeatureCoreLogs, core.AgentFeatureMihomoDevelopmentSource},
	}}); err != nil {
		t.Fatalf("write resumed heartbeat: %v", err)
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
		AgentID: enrolled.AgentID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	})
	if err != nil {
		t.Fatalf("create core install task: %v", err)
	}
	taskMessage = core.WireMessage{}
	if err := wsjson.Read(ctx, connection, &taskMessage); err != nil {
		t.Fatalf("read core install task: %v", err)
	}
	if taskMessage.Task == nil || taskMessage.Task.ID != installTask.ID || taskMessage.Task.CoreVersion != core.CoreVersionDevelopment || taskMessage.Task.CoreSource != string(core.CoreSourceMirror) || taskMessage.Task.ConfigContent != "" {
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
	for _, action := range []core.Action{core.ActionDeploy, core.ActionImportExisting, core.ActionReadConfig, core.ActionStart, core.ActionStop, core.ActionRestart} {
		request := core.TaskRequest{AgentID: enrolled.AgentID, Action: action, Engine: core.EngineMihomo}
		if action == core.ActionDeploy || action == core.ActionImportExisting {
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
			for _, testCase := range []struct {
				name, token string
				wantStatus  int
				wantContent bool
			}{
				{name: "admin", token: adminToken, wantStatus: http.StatusOK, wantContent: true},
				{name: "explicit snapshot reader", token: configSnapshotReadToken, wantStatus: http.StatusOK, wantContent: true},
				{name: "default auditor", token: auditorToken, wantStatus: http.StatusForbidden},
				{name: "tasks read only", token: tasksReadToken, wantStatus: http.StatusForbidden},
			} {
				t.Run("read-config snapshot authorization/"+testCase.name, func(t *testing.T) {
					snapshotRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/api/v1/tasks/"+created.ID+"/config-snapshot", nil)
					if requestErr != nil {
						t.Fatalf("create read-config snapshot request: %v", requestErr)
					}
					snapshotRequest.Header.Set("Authorization", "Bearer "+testCase.token)
					snapshotResponse, requestErr := http.DefaultClient.Do(snapshotRequest)
					if requestErr != nil {
						t.Fatalf("read-config snapshot request: %v", requestErr)
					}
					body, readErr := io.ReadAll(snapshotResponse.Body)
					_ = snapshotResponse.Body.Close()
					if readErr != nil {
						t.Fatalf("read read-config snapshot response: %v", readErr)
					}
					if snapshotResponse.StatusCode != testCase.wantStatus {
						t.Fatalf("read-config snapshot API status=%d body=%q, want %d", snapshotResponse.StatusCode, body, testCase.wantStatus)
					}
					if !testCase.wantContent {
						var deniedPayload map[string]any
						if decodeErr := json.Unmarshal(body, &deniedPayload); decodeErr != nil {
							t.Fatalf("decode forbidden read-config snapshot response: %v", decodeErr)
						}
						_, exposedContent := deniedPayload["content"]
						if exposedContent || bytes.Contains(body, []byte("mixed-port")) {
							t.Fatalf("forbidden read-config snapshot exposed plaintext: %q", body)
						}
						return
					}
					var snapshotPayload struct {
						Content string `json:"content"`
					}
					if decodeErr := json.Unmarshal(body, &snapshotPayload); decodeErr != nil || snapshotPayload.Content != config.Content {
						t.Fatalf("read-config snapshot API content=%q decode=%v", snapshotPayload.Content, decodeErr)
					}
				})
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
	if err != nil || agent.Metrics.CollectedAt.IsZero() || agent.Metrics.CPUPercent != 42.5 || agent.Metrics.NetworkRXBPS != 300 || agent.Metrics.ObservedPublicIP != "93.184.216.34" || agent.Version != "test" || len(agent.Features) == 0 {
		t.Fatalf("live metrics snapshot not persisted: agent=%+v error=%v", agent, err)
	}
	if err := dataStore.SetAgentClientAddress(ctx, enrolled.AgentID, "managed.example.test"); err != nil {
		t.Fatalf("set managed client address: %v", err)
	}
	managedAgent, err := dataStore.GetAgent(ctx, enrolled.AgentID)
	if candidates := clientAddressCandidates(managedAgent); err != nil || len(candidates) == 0 || candidates[0].address != "managed.example.test" || candidates[0].source != "手动设置" {
		t.Fatalf("managed client address did not take priority: candidates=%+v error=%v", candidates, err)
	}
	if err := dataStore.SetAgentClientAddress(ctx, enrolled.AgentID, ""); err != nil {
		t.Fatalf("restore automatic client address: %v", err)
	}
	automaticAgent, err := dataStore.GetAgent(ctx, enrolled.AgentID)
	if candidates := clientAddressCandidates(automaticAgent); err != nil || len(candidates) != 2 || candidates[0].address != "198.35.26.96" || candidates[0].source != "Agent 默认路由接口 eth0" || candidates[1].address != "93.184.216.34" || candidates[1].source != "已验证连接来源 · IPv4" {
		t.Fatalf("automatic client address did not prefer route interface then verified WSS: candidates=%+v error=%v", candidates, err)
	}
	if err := dataStore.UpdateAgentObservedPublicIP(ctx, enrolled.AgentID, ""); err != nil {
		t.Fatalf("clear unavailable WSS public observation: %v", err)
	}
	fallbackAgent, err := dataStore.GetAgent(ctx, enrolled.AgentID)
	if candidates := clientAddressCandidates(fallbackAgent); err != nil || len(candidates) == 0 || candidates[0].address != "198.35.26.96" || candidates[0].source != "Agent 默认路由接口 eth0" {
		t.Fatalf("unavailable public source did not retain the default-route fallback: candidates=%+v error=%v", candidates, err)
	}
	// A direct Cloudflare edge observation must never be stored as the Agent
	// address. Reconnecting through that relay clears the stale observation
	// instead of surfacing the control-plane hop.
	if err := dataStore.UpdateAgentObservedPublicIP(ctx, enrolled.AgentID, "93.184.216.34"); err != nil {
		t.Fatalf("seed stale WSS public observation: %v", err)
	}
	ambiguousHandshake, _ := http.NewRequestWithContext(ctx, http.MethodGet, websocketURL, nil)
	if err := authn.SignRequest(ambiguousHandshake, nil, enrolled.AgentID, privateKey, time.Now().UTC()); err != nil {
		t.Fatalf("sign ambiguous WSS handshake: %v", err)
	}
	ambiguousHandshake.Header.Set("X-Forwarded-For", "172.69.135.152")
	ambiguousConnection, ambiguousResponse, err := websocket.Dial(ctx, websocketURL, &websocket.DialOptions{
		HTTPHeader: ambiguousHandshake.Header, Subprotocols: []string{"qcontrolhub.agent.v1"},
	})
	if err != nil {
		if ambiguousResponse != nil {
			t.Fatalf("dial ambiguous WSS: %v (%s)", err, ambiguousResponse.Status)
		}
		t.Fatalf("dial ambiguous WSS: %v", err)
	}
	defer ambiguousConnection.Close(websocket.StatusNormalClosure, "test complete")
	var ambiguousHello core.WireMessage
	if err := wsjson.Read(ctx, ambiguousConnection, &ambiguousHello); err != nil || ambiguousHello.Type != core.WireHello {
		t.Fatalf("read ambiguous hello: message=%+v error=%v", ambiguousHello, err)
	}
	ambiguousAgent, err := dataStore.GetAgent(ctx, enrolled.AgentID)
	if err != nil || ambiguousAgent.Metrics.ObservedPublicIP != "" {
		t.Fatalf("ambiguous WSS source was stored as the Agent address: agent=%+v error=%v", ambiguousAgent, err)
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

// TestWSSMirrorFeatureDowngradeWithPostgreSQL models the real ordering bug: the
// stored Agent features still advertise mihomo-development-source-v1 when the
// websocket reconnects, so dispatch must wait until this connection's first
// heartbeat is persisted. Before that heartbeat no task is delivered; after a
// heartbeat that drops the feature, the running mirror task is failed
// atomically and the pending mirror task stays pending, while an official
// omitted-source install becomes dispatchable.
func TestWSSMirrorFeatureDowngradeWithPostgreSQL(t *testing.T) {
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

	adminToken := strings.Repeat("d", 48)
	trustedProxies, err := authn.ParseTrustedProxies([]string{"127.0.0.1/32"})
	if err != nil {
		t.Fatalf("parse trusted proxy fixture: %v", err)
	}
	httpServer := httptest.NewServer(New(dataStore, Config{
		AdminToken:     adminToken,
		TrustedProxies: trustedProxies,
		AgentBinary:    []byte("test-agent-binary"),
		AgentVersion:   "test-version",
	}).Handler())
	defer httpServer.Close()

	// Agent A holds a running mirror install and a pending omitted-source
	// install. After the reconnecting Agent drops the source feature, the
	// running mirror must be failed, the pending mirror skipped, and the
	// official task dispatched.
	a, aKey := enrollWSSTestSourceAgent(t, ctx, httpServer.URL, databaseURL, dataStore, "wss-downgrade-a")
	running, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: a, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	})
	if err != nil {
		t.Fatalf("create running mirror task: %v", err)
	}
	claimed, err := dataStore.ClaimTask(ctx, a)
	if err != nil || claimed == nil || claimed.ID != running.ID {
		t.Fatalf("claim running mirror before reconnect = %+v, %v; want %s", claimed, err, running.ID)
	}
	official, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: a, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment,
	})
	if err != nil || official.CoreSource != string(core.CoreSourceOfficial) {
		t.Fatalf("create official omitted-source task = %+v, %v; want official source", official, err)
	}
	connA, dispatchedA := downgradeReconnect(t, ctx, httpServer.URL, a, aKey, func() {
		if got, err := dataStore.GetTask(ctx, running.ID); err != nil || got.Status != core.TaskRunning {
			t.Fatalf("running mirror was dispatched before the first heartbeat: %+v, %v", got, err)
		}
	})
	if dispatchedA == nil {
		t.Fatal("expected an official task dispatch after the downgraded heartbeat")
	}
	if dispatchedA.Task.ID != official.ID || dispatchedA.Task.CoreSource != string(core.CoreSourceOfficial) {
		t.Fatalf("expected official task %s after heartbeat, got %s (source %q)", official.ID, dispatchedA.Task.ID, dispatchedA.Task.CoreSource)
	}
	assertRawTaskTerminalCleanAPI(t, ctx, databaseURL, running.ID)
	assertEmptyAgentFeaturesAndMirrorBlocked(t, ctx, dataStore, a)
	connA.Close(websocket.StatusNormalClosure, "scenario complete")

	// Agent B holds only a pending mirror install that must stay pending and
	// never be dispatched after the source feature is dropped.
	b, bKey := enrollWSSTestSourceAgent(t, ctx, httpServer.URL, databaseURL, dataStore, "wss-downgrade-b")
	pending, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: b, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	})
	if err != nil {
		t.Fatalf("create pending mirror task: %v", err)
	}
	connB, dispatchedB := downgradeReconnect(t, ctx, httpServer.URL, b, bKey, nil)
	if dispatchedB != nil {
		t.Fatalf("pending mirror task was dispatched after downgrade: %+v", dispatchedB.Task)
	}
	if got, err := dataStore.GetTask(ctx, pending.ID); err != nil || got.Status != core.TaskPending {
		t.Fatalf("pending mirror after downgraded heartbeat = %+v, %v; want pending", got, err)
	}
	assertEmptyAgentFeaturesAndMirrorBlocked(t, ctx, dataStore, b)
	connB.Close(websocket.StatusNormalClosure, "scenario complete")
}

// assertEmptyAgentFeaturesAndMirrorBlocked verifies that an empty-feature
// heartbeat cleared the stale source capability and that the API mirror gate
// now rejects a mirror install.
func assertEmptyAgentFeaturesAndMirrorBlocked(t *testing.T, ctx context.Context, dataStore *store.Store, agentID string) {
	t.Helper()
	current, err := dataStore.GetAgent(ctx, agentID)
	if err != nil || len(current.Features) != 0 {
		t.Fatalf("agent features after empty-feature heartbeat = %+v, %v; want empty", current.Features, err)
	}
	if _, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agentID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("mirror create after empty-feature heartbeat = %v, want ErrConflict", err)
	}
}

// enrollWSSTestSourceAgent enrolls a Mihomo-capable Agent advertising the
// negotiated mihomo-development-source-v1 feature and registers cleanup.
func enrollWSSTestSourceAgent(t *testing.T, ctx context.Context, base, databaseURL string, dataStore *store.Store, name string) (string, ed25519.PrivateKey) {
	t.Helper()
	enrollment, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: name, TTLMinutes: 5, MaxUses: 1})
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	body, _ := json.Marshal(core.EnrollRequest{
		Name: name, OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo},
		Features:     []string{core.AgentFeatureSelfUpgrade, core.AgentFeatureMihomoDevelopmentSource},
		PublicKey:    authn.EncodePublicKey(publicKey),
	})
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/agent/v1/enroll", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+enrollment.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("enroll %s: %v", name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("enroll %s status = %s", name, response.Status)
	}
	var enrolled core.EnrollResponse
	if err := json.NewDecoder(response.Body).Decode(&enrolled); err != nil {
		t.Fatalf("decode enrollment %s: %v", name, err)
	}
	t.Cleanup(func() { cleanupTaskAPIFixture(t, databaseURL, enrollment.ID, []string{enrolled.AgentID}) })
	return enrolled.AgentID, privateKey
}

// downgradeReconnect dials the websocket with the given Agent key, reads Hello,
// runs an optional preHeartbeat assertion, sends a heartbeat that drops
// mihomo-development-source-v1, and returns the connection plus the first task
// message that arrives afterwards (nil if none arrives).
func downgradeReconnect(t *testing.T, ctx context.Context, base, agentID string, privateKey ed25519.PrivateKey, preHeartbeat func()) (*websocket.Conn, *core.WireMessage) {
	t.Helper()
	websocketURL := "ws" + strings.TrimPrefix(base, "http") + "/agent/v1/connect"
	handshake, _ := http.NewRequestWithContext(ctx, http.MethodGet, websocketURL, nil)
	if err := authn.SignRequest(handshake, nil, agentID, privateKey, time.Now().UTC()); err != nil {
		t.Fatalf("sign WSS handshake: %v", err)
	}
	handshake.Header.Set("X-Forwarded-For", "192.0.2.44")
	connection, dialResponse, err := websocket.Dial(ctx, websocketURL, &websocket.DialOptions{
		HTTPHeader: handshake.Header, Subprotocols: []string{"qcontrolhub.agent.v1"},
	})
	if err != nil {
		if dialResponse != nil {
			t.Fatalf("dial WSS: %v (%s)", err, dialResponse.Status)
		}
		t.Fatalf("dial WSS: %v", err)
	}
	var hello core.WireMessage
	if err := wsjson.Read(ctx, connection, &hello); err != nil || hello.Type != core.WireHello {
		t.Fatalf("read hello: message=%+v error=%v", hello, err)
	}
	if preHeartbeat != nil {
		preHeartbeat()
	}
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{
		Version: "downgraded",
	}}); err != nil {
		t.Fatalf("write downgraded heartbeat: %v", err)
	}
	postCtx, cancelPost := context.WithTimeout(ctx, 2*time.Second)
	defer cancelPost()
	var dispatched core.WireMessage
	if err := wsjson.Read(postCtx, connection, &dispatched); err != nil {
		return connection, nil
	}
	return connection, &dispatched
}

// assertRawTaskTerminalCleanAPI reads the underlying tasks row directly and
// asserts the terminal-state cleanup and unknown-outcome wording. GetTask does
// not expose lease_id or config_content, so the raw columns must be checked
// here to avoid a false positive during the downgrade-fail regression.
func assertRawTaskTerminalCleanAPI(t *testing.T, ctx context.Context, databaseURL, taskID string) {
	t.Helper()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect for raw terminal task: %v", err)
	}
	defer connection.Close(ctx)
	var status string
	var finishedAt *time.Time
	var leaseID *string
	var configContent *string
	var errMsg string
	if err := connection.QueryRow(ctx, `
		SELECT status, finished_at, lease_id, config_content, COALESCE(error,'')
		FROM tasks WHERE id=$1`, taskID).Scan(&status, &finishedAt, &leaseID, &configContent, &errMsg); err != nil {
		t.Fatalf("read raw terminal task: %v", err)
	}
	if status != string(core.TaskFailed) {
		t.Fatalf("raw terminal task status = %q, want %q", status, core.TaskFailed)
	}
	if finishedAt == nil {
		t.Fatalf("raw terminal task finished_at is NULL")
	}
	if leaseID != nil {
		t.Fatalf("raw terminal task lease must be NULL, got %q", *leaseID)
	}
	if configContent != nil {
		t.Fatalf("raw terminal task config_content must be NULL, got %q", *configContent)
	}
	if !strings.Contains(errMsg, core.AgentFeatureMihomoDevelopmentSource) || !strings.Contains(errMsg, "cannot be safely resumed") || !strings.Contains(errMsg, "unknown whether the previous Agent executed it") {
		t.Fatalf("raw terminal task error = %q, want feature + safe-resume + unknown-outcome wording", errMsg)
	}
	if strings.Contains(errMsg, "was not executed") {
		t.Fatalf("raw terminal task error must not claim the task was never executed: %q", errMsg)
	}
}
