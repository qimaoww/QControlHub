//go:build linux

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

var upgradeCandidateBuild struct {
	sync.Once
	content []byte
	err     error
}

func upgradeCandidate(t *testing.T) []byte {
	t.Helper()
	upgradeCandidateBuild.Do(func() {
		root, err := os.MkdirTemp("", "qagent-candidate-test-")
		if err != nil {
			upgradeCandidateBuild.err = err
			return
		}
		defer os.RemoveAll(root)
		path := filepath.Join(root, "qagent")
		command := exec.Command("go", "build", "-buildvcs=false", "-ldflags=-s -w -X main.version=upgrade-test", "-o", path, "../../cmd/agent")
		if output, err := command.CombinedOutput(); err != nil {
			upgradeCandidateBuild.err = fmt.Errorf("%w: %s", err, output)
			return
		}
		upgradeCandidateBuild.content, upgradeCandidateBuild.err = os.ReadFile(path)
	})
	if upgradeCandidateBuild.err != nil {
		t.Fatal(upgradeCandidateBuild.err)
	}
	return upgradeCandidateBuild.content
}

func TestAgentUpgradeCandidatePreflightCommitAndRollback(t *testing.T) {
	requireAgentRoot(t)
	for _, manager := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		t.Run(manager, func(t *testing.T) {
			root := t.TempDir()
			current := filepath.Join(root, "qagent")
			candidate := filepath.Join(root, "candidate")
			if err := os.WriteFile(current, existingDiscoveryCoreHelper, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(candidate, upgradeCandidate(t), 0o600); err != nil {
				t.Fatal(err)
			}
			assets := filepath.Join(root, "new-private-directory", "core-install")
			transaction, err := prepareAgentUpgradeWithAssets(context.Background(), current, candidate, "upgrade-test", manager, assets)
			if err != nil {
				t.Fatal(err)
			}
			if err := transaction.commit(); err != nil {
				t.Fatal(err)
			}
			content, _ := os.ReadFile(current)
			if string(content) != string(upgradeCandidate(t)) {
				t.Fatal("candidate was not installed")
			}
			if err := transaction.rollback(); err != nil {
				t.Fatal(err)
			}
			content, _ = os.ReadFile(current)
			info, _ := os.Stat(current)
			if string(content) != string(existingDiscoveryCoreHelper) || info.Mode().Perm() != 0o750 {
				t.Fatal("rollback did not restore executable and mode")
			}
			if err := transaction.rollback(); err != nil {
				t.Fatal("rollback is not idempotent", err)
			}
		})
	}
}

func TestAgentUpgradeRejectsCandidateWithoutChangingCurrent(t *testing.T) {
	requireAgentRoot(t)
	for _, scenario := range []string{"text", "wrong-architecture", "wrong-version", "unsafe-assets", "tamper-after-preflight", "unsafe-backup"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			current := filepath.Join(root, "qagent")
			candidate := filepath.Join(root, "candidate")
			assets := filepath.Join(root, "assets")
			if err := os.WriteFile(current, existingDiscoveryCoreHelper, 0o750); err != nil {
				t.Fatal(err)
			}
			content := append([]byte(nil), upgradeCandidate(t)...)
			version := "upgrade-test"
			switch scenario {
			case "text":
				content = []byte("not executable")
			case "wrong-architecture":
				content[18] = 0
				content[19] = 0
			case "wrong-version":
				version = "other-version"
			case "unsafe-assets":
				if err := os.Symlink(root, assets); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(candidate, content, 0o600); err != nil {
				t.Fatal(err)
			}
			transaction, err := prepareAgentUpgradeWithAssets(context.Background(), current, candidate, version, ServiceManagerSystemd, assets)
			if scenario == "tamper-after-preflight" || scenario == "unsafe-backup" {
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "unsafe-backup" {
					err = os.Symlink(current, transaction.backup)
				} else {
					err = os.WriteFile(candidate, existingDiscoveryCoreHelper, 0o750)
				}
				if err != nil {
					t.Fatal(err)
				}
				err = transaction.commit()
			}
			if err == nil {
				t.Fatal("invalid upgrade accepted")
			}
			after, _ := os.ReadFile(current)
			if string(after) != string(existingDiscoveryCoreHelper) {
				t.Fatal("current binary changed on failed upgrade")
			}
		})
	}
}

func TestAgentUpgradeDownloadChecksIntegrityAndCleanup(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	payload := []byte("candidate payload")
	for _, scenario := range []string{"valid", "missing-checksum", "wrong-checksum", "oversized", "truncated", "http-error", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "http-error" {
					http.Error(w, "unavailable", 503)
					return
				}
				if scenario != "missing-checksum" {
					w.Header().Set("X-QControlHub-Agent-SHA256", fmt.Sprintf("%x", sha256.Sum256(payload)))
				}
				if scenario == "wrong-checksum" {
					w.Header().Set("X-QControlHub-Agent-SHA256", strings.Repeat("0", 64))
				}
				if scenario == "oversized" {
					w.Header().Set("Content-Length", fmt.Sprint(core.MaxAgentBinaryBytes+1))
				}
				if scenario == "truncated" {
					w.Header().Set("Content-Length", fmt.Sprint(len(payload)+10))
				}
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			client := &Client{config: ClientConfig{ServerURL: server.URL}, creds: credentials{AgentID: "agt_0123456789abcdef", PrivateKey: authn.EncodePrivateKey(privateKey)}, http: server.Client()}
			root := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			path, _, _, err := client.downloadAgentBinary(ctx, root)
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				content, _ := os.ReadFile(path)
				if string(content) != string(payload) {
					t.Fatal("payload changed")
				}
			} else {
				if err == nil {
					t.Fatal("bad download accepted")
				}
				entries, _ := os.ReadDir(root)
				if len(entries) != 0 {
					t.Fatal("failed download left files")
				}
			}
		})
	}
}

func TestAgentUpgradePendingBlocksDifferentTasks(t *testing.T) {
	client := &Client{upgradePending: true, config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "state.json")}, executeFunc: func(context.Context, core.Task) (string, error) {
		t.Fatal("executed during pending restart")
		return "", nil
	}}
	result := client.resultForTask(context.Background(), core.Task{ID: "tsk_0123456789abcdef", Action: core.ActionRestart, Engine: core.EngineXray})
	if result.Success || !strings.Contains(result.Error, "waiting for restart") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestAgentUpgradeReconnectRetainsRestartIntent(t *testing.T) {
	client := &Client{upgradePending: true, upgradeCommitted: &agentUpgradeTransaction{},
		creds: credentials{CompletedTasks: map[string]completedTask{"tsk_0123456789abcdef": {Success: true}}}}
	if !client.committedUpgradeAwaitingRestart() {
		t.Fatal("lost ACK leaves old process running")
	}
	result, ok := client.cachedTaskResult(core.Task{ID: "tsk_0123456789abcdef", LeaseID: "replacement-lease"})
	if !ok || !result.Success || result.LeaseID != "replacement-lease" {
		t.Fatal("upgrade retry lost cached result")
	}
	client.upgradePending = false
	if client.committedUpgradeAwaitingRestart() {
		t.Fatal("rolled-back upgrade schedules another restart")
	}
}

func TestAgentUpgradeRestartProbeDoesNotBlockHeartbeat(t *testing.T) {
	client := &Client{upgradePending: true, upgradeCommitted: &agentUpgradeTransaction{}}
	client.taskLifecycleMu.Lock()
	done := make(chan bool, 1)
	go func() { done <- client.committedUpgradeAwaitingRestart() }()
	select {
	case ready := <-done:
		if ready {
			t.Error("restart probe ignored active mutation")
		}
	case <-time.After(time.Second):
		client.taskLifecycleMu.Unlock()
		t.Fatal("restart probe blocked network loop")
	}
	client.taskLifecycleMu.Unlock()
	if !client.committedUpgradeAwaitingRestart() {
		t.Fatal("next heartbeat lost restart intent")
	}
}

func TestAgentUpgradeRollbackRefusesExternalEdits(t *testing.T) {
	requireAgentRoot(t)
	for _, changeBackup := range []bool{false, true} {
		root := t.TempDir()
		current := filepath.Join(root, "qagent")
		candidate := filepath.Join(root, "candidate")
		if err := os.WriteFile(current, existingDiscoveryCoreHelper, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(candidate, upgradeCandidate(t), 0o600); err != nil {
			t.Fatal(err)
		}
		transaction, err := prepareAgentUpgradeWithAssets(context.Background(), current, candidate, "upgrade-test", ServiceManagerSystemd, filepath.Join(root, "assets"))
		if err != nil {
			t.Fatal(err)
		}
		if err := transaction.commit(); err != nil {
			t.Fatal(err)
		}
		target := current
		if changeBackup {
			target = transaction.backup
		}
		if err := os.WriteFile(target, []byte("administrator edit"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := transaction.rollback(); err == nil {
			t.Fatal("rollback accepted external mutation")
		}
		content, _ := os.ReadFile(target)
		if string(content) != "administrator edit" {
			t.Fatal("rollback overwrote administrator change")
		}
	}
}

func TestAgentUpgradeCancelledPreflightPreservesCurrent(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	current := filepath.Join(root, "qagent")
	candidate := filepath.Join(root, "candidate")
	if err := os.WriteFile(current, existingDiscoveryCoreHelper, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, upgradeCandidate(t), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepareAgentUpgradeWithAssets(ctx, current, candidate, "upgrade-test", ServiceManagerSystemd, filepath.Join(root, "assets")); err == nil {
		t.Fatal("cancelled preflight succeeded")
	}
	content, _ := os.ReadFile(current)
	if string(content) != string(existingDiscoveryCoreHelper) {
		t.Fatal("cancelled preflight changed executable")
	}
}

func TestAgentUpgradeExecFailureRestoresOriginal(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	current := filepath.Join(root, "qagent")
	candidate := filepath.Join(root, "candidate")
	if err := os.WriteFile(current, existingDiscoveryCoreHelper, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, upgradeCandidate(t), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := prepareAgentUpgradeWithAssets(context.Background(), current, candidate, "upgrade-test", ServiceManagerSystemd, filepath.Join(root, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.commit(); err != nil {
		t.Fatal(err)
	}
	called := false
	client := &Client{upgradePending: true, upgradeCommitted: transaction, reexecFunc: func(path string, _ []string, _ []string) error {
		called = true
		if path != current {
			t.Error("wrong restart executable")
		}
		return errors.New("simulated exec failure")
	}}
	client.reexecAfterUpgrade()
	content, _ := os.ReadFile(current)
	if !called || client.upgradePending || string(content) != string(existingDiscoveryCoreHelper) {
		t.Fatal("exec failure did not safely restore the old Agent")
	}
}
