package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestNewClientDerivesHTTPSAndWSSOrigins(t *testing.T) {
	executor := &Executor{}
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
	executor := &Executor{}
	for _, interval := range []time.Duration{time.Millisecond, 31 * time.Second, time.Minute} {
		if _, err := NewClient(ClientConfig{ServerURL: "wss://control.example.com", HeartbeatEvery: interval}, executor); err == nil || !strings.Contains(err.Error(), "between 1s and 30s") {
			t.Fatalf("NewClient(HeartbeatEvery=%s) error = %v", interval, err)
		}
	}
}

func TestNewClientRejectsUnsafeRemoteOrigins(t *testing.T) {
	executor := &Executor{}
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
