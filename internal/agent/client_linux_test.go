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

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
		if !stringInSlice(core.AgentFeatureSelfUpgrade, captured.Features) || !stringInSlice(core.AgentFeaturePortTraffic, captured.Features) {
			t.Fatalf("self-upgrade or port-traffic feature missing: %v", captured.Features)
		}
	}

	t.Run("systemd", func(t *testing.T) { run(t, ServiceManagerSystemd, true) })
	t.Run("openrc", func(t *testing.T) { run(t, ServiceManagerOpenRC, false) })
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
		if !stringInSlice(core.AgentFeatureSelfUpgrade, message.Heartbeat.Features) || !stringInSlice(core.AgentFeaturePortTraffic, message.Heartbeat.Features) {
			t.Fatalf("heartbeat self-upgrade or port-traffic feature missing: %v", message.Heartbeat.Features)
		}
	}

	t.Run("systemd", func(t *testing.T) { run(t, ServiceManagerSystemd, true) })
	t.Run("openrc", func(t *testing.T) { run(t, ServiceManagerOpenRC, false) })
}

func TestTaskExecutionSurvivesWebSocketSessionCancellation(t *testing.T) {
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
		executor: &Executor{Specs: map[core.Engine]EngineSpec{
			core.EngineMihomo: {Binary: "unused", ConfigPath: "/tmp/config.yaml", Service: "qagent-mihomo.service"},
		}},
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
		core.ActionRestart, core.ActionStatus, core.ActionInstall, core.ActionReadConfig,
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
