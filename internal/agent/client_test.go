package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
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

func requireAgentRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("agent execution tests require root")
	}
}

func testClientExecutor(t *testing.T) *Executor {
	t.Helper()
	manager := defaultSystemdServiceManager()
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		manager, err = NewServiceManager(ServiceManagerOpenRC)
		if err != nil {
			t.Fatal(err)
		}
	}
	return &Executor{Services: manager}
}

func TestClientShouldReenroll(t *testing.T) {
	tests := []struct {
		name     string
		client   *Client
		loaded   credentials
		wantTrue bool
	}{
		{
			name:     "host change with token migrates",
			client:   &Client{config: ClientConfig{EnrollmentToken: "tok"}, serverHost: "new.example.test"},
			loaded:   credentials{Server: "old.example.test"},
			wantTrue: true,
		},
		{
			name:     "same host with token is a normal restart",
			client:   &Client{config: ClientConfig{EnrollmentToken: "tok"}, serverHost: "h.example.test"},
			loaded:   credentials{Server: "h.example.test"},
			wantTrue: false,
		},
		{
			name:     "legacy credentials without host are adopted without rotation",
			client:   &Client{config: ClientConfig{EnrollmentToken: "tok"}, serverHost: "h.example.test"},
			loaded:   credentials{},
			wantTrue: false,
		},
		{
			name:     "host change without token cannot migrate",
			client:   &Client{config: ClientConfig{}, serverHost: "new.example.test"},
			loaded:   credentials{Server: "old.example.test"},
			wantTrue: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.client.shouldReenroll(test.loaded); got != test.wantTrue {
				t.Fatalf("shouldReenroll() = %v, want %v", got, test.wantTrue)
			}
		})
	}
}

func TestReenrollRotatesIdentityAndDropsPriorPanelTasks(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	initial := credentials{
		AgentID:    "agt_old",
		PrivateKey: authn.EncodePrivateKey(privateKey),
		Server:     "old.example.test",
		CompletedTasks: map[string]completedTask{
			"t1": {Success: true, CompletedAt: time.Now()},
		},
	}
	if err := saveCredentials(statePath, initial); err != nil {
		t.Fatal(err)
	}

	enrollCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/agent/v1/enroll" {
			http.NotFound(w, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer new-token" {
			t.Errorf("enroll authorization = %q, want Bearer new-token", request.Header.Get("Authorization"))
		}
		enrollCalled = true
		_ = json.NewEncoder(w).Encode(core.EnrollResponse{AgentID: "agt_new"})
	}))
	defer server.Close()

	parsedHost := strings.TrimPrefix(server.URL, "http://")
	client, err := NewClient(ClientConfig{
		ServerURL: server.URL, StatePath: statePath, EnrollmentToken: "new-token",
		HeartbeatEvery: 30 * time.Second, MetricsEvery: 30 * time.Second,
	}, testClientExecutor(t))
	if err != nil {
		t.Fatalf("new Client: %v", err)
	}
	enrolled, err := client.reenroll(context.Background())
	if err != nil {
		t.Fatalf("reenroll: %v", err)
	}
	if !enrollCalled {
		t.Fatal("enroll endpoint was not reached")
	}
	if enrolled.Server != parsedHost {
		t.Errorf("reenrolled server = %q, want %q", enrolled.Server, parsedHost)
	}
	if len(enrolled.CompletedTasks) != 0 {
		t.Errorf("prior panel completed tasks should be dropped, got %d", len(enrolled.CompletedTasks))
	}
	stored, err := loadCredentials(statePath)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if stored.AgentID != "agt_new" {
		t.Errorf("stored agent id = %q, want agt_new", stored.AgentID)
	}
	if stored.Server != parsedHost {
		t.Errorf("stored server = %q, want %q", stored.Server, parsedHost)
	}
}

func TestNewClientDerivesHTTPSAndWSSOrigins(t *testing.T) {
	requireAgentRoot(t)
	executor := testClientExecutor(t)
	client, err := NewClient(ClientConfig{ServerURL: "wss://control.example.com", HeartbeatEvery: 10 * time.Second}, executor)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client.config.ServerURL != "https://control.example.com" {
		t.Fatalf("enrollment origin = %q", client.config.ServerURL)
	}
	if client.websocketURL != "wss://control.example.com/agent/v1/connect" {
		t.Fatalf("WebSocket URL = %q", client.websocketURL)
	}
}

func TestNewClientRejectsHeartbeatIntervalsOutsideServerDeadline(t *testing.T) {
	requireAgentRoot(t)
	executor := testClientExecutor(t)
	for _, interval := range []time.Duration{time.Millisecond, 31 * time.Second, time.Minute} {
		if _, err := NewClient(ClientConfig{ServerURL: "wss://control.example.com", HeartbeatEvery: interval}, executor); err == nil || !strings.Contains(err.Error(), "between 1s and 30s") {
			t.Fatalf("NewClient(HeartbeatEvery=%s) error = %v", interval, err)
		}
	}
}

func TestNewClientDefaultsAndValidatesMetricsInterval(t *testing.T) {
	requireAgentRoot(t)
	executor := testClientExecutor(t)
	client, err := NewClient(ClientConfig{ServerURL: "wss://control.example.com", HeartbeatEvery: 10 * time.Second}, executor)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client.config.MetricsEvery != time.Second {
		t.Fatalf("default MetricsEvery = %s", client.config.MetricsEvery)
	}
	for _, interval := range []time.Duration{time.Millisecond, 31 * time.Second, time.Minute} {
		if _, err := NewClient(ClientConfig{ServerURL: "wss://control.example.com", HeartbeatEvery: 10 * time.Second, MetricsEvery: interval}, executor); err == nil || !strings.Contains(err.Error(), "QCH_METRICS_INTERVAL") {
			t.Fatalf("NewClient(MetricsEvery=%s) error = %v", interval, err)
		}
	}
}

func TestNewClientRejectsUnsafeRemoteOrigins(t *testing.T) {
	requireAgentRoot(t)
	executor := testClientExecutor(t)
	for _, value := range []string{
		"wss://user:password@control.example.com",
		"wss://control.example.com/base",
		"wss://control.example.com?token=secret",
		"ftp://control.example.com",
	} {
		if _, err := NewClient(ClientConfig{ServerURL: value}, executor); err == nil {
			t.Fatalf("NewClient() accepted unsafe URL %q", value)
		}
	}
	if _, err := NewClient(ClientConfig{ServerURL: "ws://192.0.2.10", AllowHTTP: true}, executor); err == nil {
		t.Fatal("NewClient() allowed live execution over remote ws://")
	}
}

func TestReconnectedSessionJoinsInFlightTaskExecution(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	client := &Client{
		creds:    credentials{AgentID: "agt_0123456789abcdef"},
		executor: &Executor{},
		executeFunc: func(context.Context, core.Task) (string, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			<-release
			return "current configuration", nil
		},
	}
	task := core.Task{
		ID: "tsk_2222222222222222", AgentID: client.creds.AgentID,
		LeaseID: "lease-identifier-that-is-long-enough", Action: core.ActionReadConfig,
		Engine: core.EngineMihomo, Status: core.TaskRunning,
	}
	first := make(chan core.WireMessage, 1)
	second := make(chan core.WireMessage, 1)
	go client.executeTaskForSession(ctx, ctx, task, first)
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first task execution did not start")
	}
	go client.executeTaskForSession(ctx, ctx, task, second)
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("reconnected session started %d executions, want 1", got)
	}
	close(release)
	for index, outgoing := range []chan core.WireMessage{first, second} {
		select {
		case message := <-outgoing:
			if message.Result == nil || !message.Result.Result.Success || message.Result.Result.Output != "current configuration" || message.Result.Result.LeaseID != task.LeaseID {
				t.Fatalf("session %d result = %+v", index+1, message)
			}
		case <-ctx.Done():
			t.Fatalf("session %d did not receive the shared result", index+1)
		}
	}
	client.acknowledgeTaskResult(task.ID)
	client.executionsMu.Lock()
	_, retained := client.executions[task.ID]
	client.executionsMu.Unlock()
	if retained {
		t.Fatal("acknowledged task execution was retained in memory")
	}
}
