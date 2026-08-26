//go:build linux

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestRunWebSocketAppliesCapabilityGatedPublicIPProbeMessage(t *testing.T) {
	requireAgentRoot(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	heartbeatSeen := make(chan core.HeartbeatRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(w, request, &websocket.AcceptOptions{Subprotocols: []string{"qcontrolhub.agent.v1"}})
		if err != nil {
			return
		}
		defer connection.Close(websocket.StatusNormalClosure, "test complete")
		if err := wsjson.Write(request.Context(), connection, core.WireMessage{Type: core.WireHello}); err != nil {
			return
		}
		var heartbeat core.WireMessage
		if err := wsjson.Read(request.Context(), connection, &heartbeat); err != nil || heartbeat.Type != core.WireHeartbeat || heartbeat.Heartbeat == nil {
			return
		}
		heartbeatSeen <- *heartbeat.Heartbeat
		probe := core.PublicIPProbeConfig{IPv4Endpoint: "https://probe.example.test/v4", IntervalSeconds: 300}
		if err := wsjson.Write(request.Context(), connection, core.WireMessage{Type: core.WirePublicIPProbe, PublicIPProbe: &probe}); err != nil {
			return
		}
		var final core.WireMessage
		_ = wsjson.Read(request.Context(), connection, &final)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		ServerURL: server.URL, StatePath: filepath.Join(t.TempDir(), "agent-state.json"),
		HeartbeatEvery: 30 * time.Second, MetricsEvery: 30 * time.Second,
	}, testClientExecutor(t))
	if err != nil {
		t.Fatalf("new Client: %v", err)
	}
	client.creds = credentials{AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(privateKey)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- client.runWebSocket(ctx) }()

	select {
	case heartbeat := <-heartbeatSeen:
		found := false
		for _, feature := range heartbeat.Features {
			found = found || feature == core.AgentFeatureManagedPublicIPProbe
		}
		if !found {
			t.Fatalf("initial heartbeat features = %v; managed capability missing", heartbeat.Features)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for initial heartbeat")
	}
	for {
		client.publicIP.mu.Lock()
		endpoint := ""
		if len(client.publicIP.config.endpoints[0]) > 0 {
			endpoint = client.publicIP.config.endpoints[0][0]
		}
		source := client.publicIP.config.source
		client.publicIP.mu.Unlock()
		if endpoint == "https://probe.example.test/v4" && source == core.PublicIPProbeSourceControlPlane {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("WSS ended before managed config applied: %v", err)
		case <-ctx.Done():
			t.Fatal("managed public IP probe message was not applied")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-finished; err != nil {
		t.Fatalf("runWebSocket after cancellation: %v", err)
	}
}

func TestNewClientTrustsConfiguredPrivateCA(t *testing.T) {
	requireAgentRoot(t)
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer tlsServer.Close()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw})
	caPath := filepath.Join(t.TempDir(), "control-plane-ca.pem")
	if err := os.WriteFile(caPath, certificate, 0o600); err != nil {
		t.Fatalf("write private CA: %v", err)
	}
	executor := testClientExecutor(t)
	client, err := NewClient(ClientConfig{
		ServerURL: "wss" + strings.TrimPrefix(tlsServer.URL, "https"), TLSCAFile: caPath,
	}, executor)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, client.config.ServerURL, nil)
	response, err := client.http.Do(request)
	if err != nil {
		t.Fatalf("request through configured private CA: %v", err)
	}
	response.Body.Close()
}

func TestRunStopsRetryingWhenPersistedIdentityIsRejected(t *testing.T) {
	requireAgentRoot(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/agent/v1/connect" {
			http.NotFound(w, request)
			return
		}
		requests.Add(1)
		http.Error(w, "agent identity is invalid or revoked", http.StatusUnauthorized)
	}))
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	if err := saveCredentials(statePath, credentials{
		AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(privateKey),
	}); err != nil {
		t.Fatal(err)
	}
	executor := testClientExecutor(t)
	client, err := NewClient(ClientConfig{ServerURL: server.URL, StatePath: statePath}, executor)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Run(ctx); !errors.Is(err, ErrIdentityRejected) {
		t.Fatalf("Run() error = %v, want ErrIdentityRejected", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("rejected identity made %d handshakes, want 1", got)
	}
}

func TestCompletedTaskResultsPersistAndReuseCurrentLease(t *testing.T) {
	t.Parallel()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	want := credentials{
		AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(privateKey),
		CompletedTasks: map[string]completedTask{
			"tsk_0123456789abcdef": {Success: true, Output: "configuration applied", CompletedAt: time.Now().UTC()},
		},
	}
	if err := saveCredentials(statePath, want); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadCredentials(statePath)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{creds: loaded}
	outgoing := make(chan core.WireMessage, 1)
	client.executeTask(context.Background(), core.Task{
		ID: "tsk_0123456789abcdef", LeaseID: "new-lease-identifier-that-is-long-enough",
	}, outgoing)
	message := <-outgoing
	if message.Result == nil || message.Result.Result.LeaseID != "new-lease-identifier-that-is-long-enough" || !message.Result.Result.Success || message.Result.Result.Output != "configuration applied" {
		t.Fatalf("cached result = %+v", message)
	}
}

func TestClientEnrollAdvertisesCoreLogsPerServiceManager(t *testing.T) {
	run := func(t *testing.T, kind string, wantsCoreLogs bool) {
		t.Helper()
		var captured core.EnrollRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/agent/v1/enroll" {
				http.NotFound(w, request)
				return
			}
			if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
				t.Fatalf("decode enroll request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(core.EnrollResponse{AgentID: "agt_0123456789abcdef"})
		}))
		defer server.Close()

		manager, err := NewServiceManager(kind)
		if err != nil {
			t.Fatal(err)
		}
		executor := &Executor{Services: manager}
		client := &Client{
			config: ClientConfig{
				ServerURL: server.URL, EnrollmentToken: "token", Name: "node", Version: "1.0.0",
				Capabilities: []core.Engine{core.EngineXray}, Labels: map[string]string{},
			},
			http:     server.Client(),
			executor: executor,
		}
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := client.enroll(ctx, publicKey, privateKey); err != nil {
			t.Fatalf("enroll() error = %v", err)
		}
		if got := stringInSlice(core.AgentFeatureCoreLogs, captured.Features); got != wantsCoreLogs {
			t.Fatalf("core-logs advertised = %v; want %v (features: %v)", got, wantsCoreLogs, captured.Features)
		}
		if got := stringInSlice(core.AgentFeatureCoreLogStatus, captured.Features); got != wantsCoreLogs {
			t.Fatalf("core-log status advertised = %v; want %v (features: %v)", got, wantsCoreLogs, captured.Features)
		}
		if !stringInSlice(core.AgentFeatureSelfUpgrade, captured.Features) || !stringInSlice(core.AgentFeaturePortTraffic, captured.Features) {
			t.Fatalf("self-upgrade or port-traffic feature missing: %v", captured.Features)
		}
	}

	t.Run("systemd", func(t *testing.T) { run(t, ServiceManagerSystemd, true) })
	t.Run("openrc", func(t *testing.T) { run(t, ServiceManagerOpenRC, true) })
}

func TestClientHeartbeatAdvertisesCoreLogsPerServiceManager(t *testing.T) {
	run := func(t *testing.T, kind string, wantsCoreLogs bool) {
		t.Helper()
		manager, err := NewServiceManager(kind)
		if err != nil {
			t.Fatal(err)
		}
		statePath := filepath.Join(t.TempDir(), "agent-state.json")
		client := &Client{
			config:   ClientConfig{Version: "1.0.0", StatePath: statePath},
			executor: &Executor{Services: manager},
			metrics:  NewMetricsCollector(),
			traffic:  NewTrafficManager(statePath),
		}
		outgoing := make(chan core.WireMessage, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.queueHeartbeat(ctx, outgoing); err != nil {
			t.Fatalf("queueHeartbeat() error = %v", err)
		}
		message := <-outgoing
		if message.Heartbeat == nil {
			t.Fatal("heartbeat message has no payload")
		}
		if got := stringInSlice(core.AgentFeatureCoreLogs, message.Heartbeat.Features); got != wantsCoreLogs {
			t.Fatalf("heartbeat core-logs advertised = %v; want %v (features: %v)", got, wantsCoreLogs, message.Heartbeat.Features)
		}
		if got := stringInSlice(core.AgentFeatureCoreLogStatus, message.Heartbeat.Features); got != wantsCoreLogs {
			t.Fatalf("heartbeat core-log status advertised = %v; want %v (features: %v)", got, wantsCoreLogs, message.Heartbeat.Features)
		}
		if !stringInSlice(core.AgentFeatureSelfUpgrade, message.Heartbeat.Features) || !stringInSlice(core.AgentFeaturePortTraffic, message.Heartbeat.Features) {
			t.Fatalf("heartbeat self-upgrade or port-traffic feature missing: %v", message.Heartbeat.Features)
		}
	}

	t.Run("systemd", func(t *testing.T) { run(t, ServiceManagerSystemd, true) })
	t.Run("openrc", func(t *testing.T) { run(t, ServiceManagerOpenRC, true) })
}

func TestImportTaskCapturesSingBoxStartupFileBeforeResultDelivery(t *testing.T) {
	logRoot := t.TempDir()
	previous := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previous })
	content := `{"log":{"level":"info","timestamp":true,"output":"runtime.log"}}`
	executor := newImportedSingBoxLogExecutor(t, content)
	collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
	client := &Client{
		config:   ClientConfig{StatePath: filepath.Join(t.TempDir(), "agent-state.json")},
		executor: executor,
		logs:     collector,
		executeFunc: func(context.Context, core.Task) (string, error) {
			return "imported", os.WriteFile(filepath.Join(logRoot, "runtime.log"), []byte("managed startup ready\n"), 0o600)
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	result := client.resultForTask(context.Background(), core.Task{
		ID: "tsk_0123456789abcdef", LeaseID: "lease_0123456789abcdef",
		Action: core.ActionImportExisting, Engine: core.EngineSingBox, ConfigContent: content,
	})
	if !result.Success {
		t.Fatalf("import result = %+v", result)
	}
	if _, ok := waitForLine(t, collector, "managed startup ready"); !ok {
		t.Fatal("startup line written during import did not reach the WSS batch queue")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("client import collector leaked")
	}
}

func TestDeployTaskOwnsSingBoxLogTransitionExactlyOnce(t *testing.T) {
	oldContent := `{"log":{"level":"info","output":"runtime.log"}}`
	for index, test := range []struct {
		name        string
		target      string
		token       string
		wantSuccess bool
		wantFile    bool
		execute     func(t *testing.T, executor *Executor, logRoot, oldContent, target, token string) error
	}{
		{
			name: "same path file success", target: `{"log":{"level":"debug","output":"runtime.log"}}`,
			token: "same-path-transition-once", wantSuccess: true, wantFile: true,
			execute: func(t *testing.T, executor *Executor, logRoot, _, target, token string) error {
				writeSingBoxTransitionConfig(t, executor, target)
				appendSingBoxTransitionLine(t, filepath.Join(logRoot, "runtime.log"), token)
				time.Sleep(1200 * time.Millisecond)
				return nil
			},
		},
		{
			name: "different path file success", target: `{"log":{"level":"info","output":"replacement.log"}}`,
			token: "replacement-startup-once", wantSuccess: true, wantFile: true,
			execute: func(t *testing.T, executor *Executor, logRoot, _, target, token string) error {
				writeSingBoxTransitionConfig(t, executor, target)
				appendSingBoxTransitionLine(t, filepath.Join(logRoot, "replacement.log"), token)
				return nil
			},
		},
		{
			name: "same path truncated success", target: `{"log":{"level":"warning","output":"runtime.log"}}`,
			token: "truncated-startup-once", wantSuccess: true, wantFile: true,
			execute: func(t *testing.T, executor *Executor, logRoot, _, target, token string) error {
				writeSingBoxTransitionConfig(t, executor, target)
				if err := os.Truncate(filepath.Join(logRoot, "runtime.log"), 0); err != nil {
					t.Fatal(err)
				}
				appendSingBoxTransitionLine(t, filepath.Join(logRoot, "runtime.log"), token)
				return nil
			},
		},
		{
			name: "failed deploy rollback", target: `{"log":{"level":"info","output":"failed.log"}}`,
			token: "rollback-startup-once", wantSuccess: false, wantFile: true,
			execute: func(t *testing.T, executor *Executor, logRoot, oldContent, target, token string) error {
				writeSingBoxTransitionConfig(t, executor, target)
				// On the previous implementation this lets the old binding enter
				// its retry interval before the rollback restores runtime.log.
				time.Sleep(1200 * time.Millisecond)
				writeSingBoxTransitionConfig(t, executor, oldContent)
				appendSingBoxTransitionLine(t, filepath.Join(logRoot, "runtime.log"), token)
				return errors.New("simulated managed restart failure")
			},
		},
		{
			name: "file to console", target: `{"log":{"level":"info","timestamp":true}}`,
			wantSuccess: true, wantFile: false,
			execute: func(t *testing.T, executor *Executor, _, _, target, _ string) error {
				writeSingBoxTransitionConfig(t, executor, target)
				return nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			logRoot := t.TempDir()
			previous := importedSingBoxLogRoot
			importedSingBoxLogRoot = logRoot
			t.Cleanup(func() { importedSingBoxLogRoot = previous })
			executor := newImportedSingBoxLogExecutor(t, oldContent)
			if err := os.WriteFile(filepath.Join(logRoot, "runtime.log"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			collector := NewCoreLogCollectorForServiceManager(defaultSystemdServiceManager(), executor.Specs)
			// Keep the console readiness path deterministic without starting a
			// host journalctl process; the file reader remains the real runtime.
			collector.sources = nil
			collector.setSourceStatus(core.EngineSingBox, "journal", "active", "")
			if err := collector.RefreshImportedSingBoxSource(executor); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { collector.Run(ctx); close(done) }()
			waitForCoreLogSourceStatus(t, collector, core.EngineSingBox, "active")
			key := string(core.EngineSingBox) + "\x00file"
			collector.mu.Lock()
			oldRun := collector.activeFiles[key]
			collector.mu.Unlock()
			if oldRun == nil {
				t.Fatal("initial imported file watcher was not active")
			}

			client := &Client{
				config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "agent-state.json")}, executor: executor, logs: collector,
				executeFunc: func(context.Context, core.Task) (string, error) {
					return "transition attempted", test.execute(t, executor, logRoot, oldContent, test.target, test.token)
				},
			}
			result := client.resultForTask(context.Background(), core.Task{
				ID: fmt.Sprintf("tsk_transition_%02d", index), LeaseID: fmt.Sprintf("lease_transition_%02d", index),
				Action: core.ActionDeploy, Engine: core.EngineSingBox, ConfigContent: test.target,
			})
			if result.Success != test.wantSuccess {
				t.Fatalf("deploy result = %+v, want success=%v", result, test.wantSuccess)
			}
			select {
			case <-oldRun.done:
			default:
				t.Fatal("previous file watcher remained alive after transition completion")
			}

			if test.wantFile {
				waitForCoreLogSourceStatus(t, collector, core.EngineSingBox, "active")
				count := 0
				if test.name == "same path file success" {
					count = deliverCoreLogTransitionAcrossReconnect(t, client, collector, test.token)
				} else {
					count = collectCoreLogTransitionToken(t, collector, test.token)
				}
				if count != 1 {
					t.Fatalf("transition token count = %d, want exactly one", count)
				}
				collector.mu.Lock()
				currentRun := collector.activeFiles[key]
				collector.mu.Unlock()
				if currentRun == nil || currentRun == oldRun || currentRun.source.epoch <= oldRun.source.epoch {
					t.Fatalf("source epoch did not advance: old=%+v current=%+v", oldRun, currentRun)
				}
				collector.setFileSourceStatus(oldRun.source, "failed", "collector-failed")
				if status := collector.Status()[core.EngineSingBox]; status.Status != "active" {
					t.Fatalf("stale source epoch overwrote current status: %+v", status)
				}
			} else {
				collector.mu.Lock()
				active := collector.activeFiles[key]
				preferred := collector.preferredKind[core.EngineSingBox]
				collector.mu.Unlock()
				if active != nil || preferred != "journal" {
					t.Fatalf("file-to-console transition left watcher=%v preferred=%q", active != nil, preferred)
				}
			}

			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("transition collector leaked after cancellation")
			}
			collector.mu.Lock()
			activeCount := len(collector.activeFiles)
			collector.mu.Unlock()
			if activeCount != 0 {
				t.Fatalf("transition collector retained %d active file runs", activeCount)
			}
		})
	}
}

func deliverCoreLogTransitionAcrossReconnect(t *testing.T, client *Client, collector *CoreLogCollector, token string) int {
	t.Helper()
	var connections atomic.Int32
	var messageCount atomic.Int32
	batchIDs := make(chan string, 2)
	firstClosed := make(chan struct{})
	ackSent := make(chan struct{})
	releaseSecond := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
			Subprotocols: []string{"qcontrolhub.agent.v1"},
		})
		if err != nil {
			return
		}
		defer connection.Close(websocket.StatusNormalClosure, "test complete")
		number := connections.Add(1)
		readContext, cancel := context.WithTimeout(request.Context(), 10*time.Second)
		defer cancel()
		for {
			var message core.WireMessage
			if err := wsjson.Read(readContext, connection, &message); err != nil {
				return
			}
			if message.Type != core.WireCoreLogs || message.CoreLogs == nil {
				continue
			}
			matches := 0
			for _, entry := range message.CoreLogs.Entries {
				if entry.Message == token {
					matches++
				}
			}
			if matches != 1 {
				t.Errorf("WSS transition batch token matches = %d, batch=%+v", matches, message.CoreLogs)
			}
			messageCount.Add(int32(matches))
			batchIDs <- message.CoreLogs.ID
			if number == 1 {
				_ = connection.Close(websocket.StatusGoingAway, "simulate reconnect before ACK")
				close(firstClosed)
				return
			}
			if err := wsjson.Write(readContext, connection, core.WireMessage{
				Type: core.WireCoreLogsAck, BatchID: message.CoreLogs.ID,
			}); err != nil {
				t.Errorf("write WSS core log ACK: %v", err)
				return
			}
			close(ackSent)
			<-releaseSecond
			return
		}
	}))
	defer server.Close()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "wss-state.json")
	client.creds = credentials{AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(privateKey)}
	client.http = server.Client()
	client.websocketURL = "ws" + strings.TrimPrefix(server.URL, "http")
	client.config.Version = "transition-test"
	client.config.HeartbeatEvery = 30 * time.Second
	client.config.MetricsEvery = 30 * time.Second
	client.metrics = NewMetricsCollector()
	client.traffic = NewTrafficManager(statePath)

	firstContext, firstCancel := context.WithTimeout(context.Background(), 10*time.Second)
	firstErr := client.runWebSocket(firstContext)
	firstCancel()
	if firstErr == nil {
		t.Fatal("first WSS session did not disconnect before ACK")
	}
	select {
	case <-firstClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("first WSS session did not deliver transition batch")
	}

	secondContext, secondCancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() { secondDone <- client.runWebSocket(secondContext) }()
	select {
	case <-ackSent:
	case <-time.After(10 * time.Second):
		secondCancel()
		close(releaseSecond)
		t.Fatal("reconnected WSS session did not acknowledge transition batch")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		collector.mu.Lock()
		acknowledged := collector.pending == nil
		collector.mu.Unlock()
		if acknowledged {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	collector.mu.Lock()
	acknowledged := collector.pending == nil
	collector.mu.Unlock()
	if !acknowledged {
		secondCancel()
		close(releaseSecond)
		t.Fatal("reconnected WSS ACK did not clear the pending transition batch")
	}
	secondCancel()
	close(releaseSecond)
	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnected WSS session leaked after cancellation")
	}
	firstID, secondID := <-batchIDs, <-batchIDs
	if firstID == "" || secondID != firstID {
		t.Fatalf("WSS reconnect batch IDs = %q and %q", firstID, secondID)
	}
	if messageCount.Load() != 2 {
		t.Fatalf("transition token wire deliveries = %d, want one retry of one logical batch", messageCount.Load())
	}
	return 1
}

func writeSingBoxTransitionConfig(t *testing.T, executor *Executor, content string) {
	t.Helper()
	if err := os.WriteFile(executor.Specs[core.EngineSingBox].ConfigPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendSingBoxTransitionLine(t *testing.T, path, token string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitForCoreLogSourceStatus(t *testing.T, collector *CoreLogCollector, engine core.Engine, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if collector.Status()[engine].Status == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("core log source status = %+v, want %q", collector.Status()[engine], want)
}

func collectCoreLogTransitionToken(t *testing.T, collector *CoreLogCollector, token string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	quietUntil := time.Time{}
	count := 0
	for time.Now().Before(deadline) {
		batch := collector.NextBatch()
		if batch == nil {
			if !quietUntil.IsZero() && time.Now().After(quietUntil) {
				return count
			}
			time.Sleep(25 * time.Millisecond)
			continue
		}
		retry := collector.NextBatch()
		if retry == nil || retry.ID != batch.ID {
			t.Fatalf("pending batch was not stable across reconnect: first=%+v retry=%+v", batch, retry)
		}
		for _, entry := range batch.Entries {
			if entry.Message == token {
				count++
				quietUntil = time.Now().Add(1200 * time.Millisecond)
			}
		}
		if !collector.Acknowledge(batch.ID) {
			t.Fatalf("failed to acknowledge transition batch %q", batch.ID)
		}
	}
	return count
}

func TestTaskExecutionSurvivesWebSocketSessionCancellation(t *testing.T) {
	t.Parallel()
	systemctl := filepath.Join(t.TempDir(), "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/bin/sh\n[ \"$1\" = is-active ] || exit 64\nprintf '%s\\n' active\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	want := credentials{AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(privateKey)}
	if err := saveCredentials(statePath, want); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadCredentials(statePath)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		config: ClientConfig{StatePath: statePath}, creds: loaded,
		executor: &Executor{
			Specs: map[core.Engine]EngineSpec{
				core.EngineMihomo: {Binary: "unused", ConfigPath: "/tmp/config.yaml", Service: "qagent-mihomo.service"},
			},
			Services: &ServiceManager{kind: ServiceManagerSystemd, executable: systemctl},
		},
	}
	deliveryContext, cancelDelivery := context.WithCancel(context.Background())
	cancelDelivery()
	task := core.Task{
		ID: "tsk_1111111111111111", AgentID: want.AgentID, LeaseID: "lease-identifier-that-is-long-enough",
		Action: core.ActionStatus, Engine: core.EngineMihomo, Status: core.TaskRunning,
	}
	client.executeTaskForSession(context.Background(), deliveryContext, task, make(chan core.WireMessage))
	result, ok := client.cachedTaskResult(task)
	if !ok || !result.Success || result.Output == "" {
		t.Fatalf("result after delivery session cancellation = %+v, cached=%t", result, ok)
	}
}

func TestUnsupportedExistingServiceTasksFailClosedInWebSocketResults(t *testing.T) {
	t.Parallel()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	want := credentials{AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(privateKey)}
	if err := saveCredentials(statePath, want); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadCredentials(statePath)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		config: ClientConfig{StatePath: statePath}, creds: loaded,
		executor: &Executor{
			Specs: map[core.Engine]EngineSpec{
				core.EngineSingBox: {Binary: "/unused/sing-box", ConfigPath: "/unused/config.json", Service: "qagent-sing-box.service"},
			},
			ExistingDiscoveryIssues: map[core.Engine]string{core.EngineSingBox: "unsupported executable wrapper"},
		},
	}
	for index, action := range []core.Action{
		core.ActionValidate, core.ActionDeploy, core.ActionStart, core.ActionStop,
		core.ActionRestart, core.ActionStatus, core.ActionInstall, core.ActionReadConfig, core.ActionReadManagedConfig,
		core.ActionImportExisting,
	} {
		t.Run(string(action), func(t *testing.T) {
			outgoing := make(chan core.WireMessage, 1)
			task := core.Task{
				ID: fmt.Sprintf("tsk_%016d", index+1), AgentID: want.AgentID,
				LeaseID: fmt.Sprintf("lease-identifier-%016d", index+1),
				Action:  action, Engine: core.EngineSingBox, Status: core.TaskRunning,
			}
			client.executeTaskForSession(context.Background(), context.Background(), task, outgoing)
			message := <-outgoing
			if message.Result == nil || message.Result.Result.Success || !strings.Contains(message.Result.Result.Error, "core tasks are disabled") {
				t.Fatalf("websocket result for %s = %+v", action, message)
			}
		})
	}
}
