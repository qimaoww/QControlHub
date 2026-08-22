//go:build linux

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
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
	executor := &Executor{}
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
	executor := &Executor{}
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
