//go:build linux

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

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

func TestSharedFinalInboundDeletionExecutesWithoutOpeningIngress(t *testing.T) {
	for _, test := range []struct {
		name, content, share string
		action               core.Action
		allowed              bool
		frontends            []core.TLSFrontend
	}{
		{"delete-final", `{"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}]}`, "shr_0000000000000001", core.ActionDeploy, true, nil},
		{"validate-empty", `{"inbounds":[]}`, "shr_0000000000000001", core.ActionValidate, true, nil},
		{"missing-list", `{}`, "shr_0000000000000001", core.ActionDeploy, false, nil},
		{"hidden-listener", `{"inbounds":[],"endpoints":[{"type":"tun"}]}`, "shr_0000000000000001", core.ActionDeploy, false, nil},
		{"management-api", `{"inbounds":[],"experimental":{"clash_api":{"external_controller":"127.0.0.1:9090"}}}`, "shr_0000000000000001", core.ActionDeploy, false, nil},
		{"unapproved-port", `{"inbounds":[{"type":"http","listen_port":21001}]}`, "shr_0000000000000001", core.ActionDeploy, false, nil},
		{"unsupported-action", `{"inbounds":[]}`, "shr_0000000000000001", core.ActionStart, false, nil},
		{"invalid-identity", `{"inbounds":[]}`, "invalid", core.ActionDeploy, false, nil},
		{"orphan-frontend", `{"inbounds":[]}`, "shr_0000000000000001", core.ActionDeploy, false, []core.TLSFrontend{{PublicPort: 443}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			executed := false
			client := &Client{config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "state.json")},
				executeFunc: func(_ context.Context, task core.Task) (string, error) {
					executed = true
					if task.ConfigContent != test.content || len(task.TLSFrontends) != 0 {
						t.Fatal("cleanup task was altered")
					}
					return "removed final inbound and TLS route", nil
				}}
			result := client.resultForTask(context.Background(), core.Task{ID: "tsk_0000000000000001",
				Engine: core.EngineSingBox, Action: test.action, ConfigContent: test.content, SharedTrafficID: test.share, TLSFrontends: test.frontends})
			if result.Success != test.allowed || executed != test.allowed {
				t.Fatalf("allowed=%t executed=%t result=%+v", test.allowed, executed, result)
			}
		})
	}
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
