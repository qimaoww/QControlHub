package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// runner is a local wrapper for the operator's disposable node. Never forward
// the raw lease or command diagnostics to a test failure or client response.
func isolatedTLSLease(t *testing.T, ctx context.Context, runner string, task *core.Task) *core.TLSMigrationResult {
	t.Helper()
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal("invalid lease")
	}
	cmd := exec.CommandContext(ctx, runner)
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.Output()
	if err != nil {
		t.Fatal("isolated lease runner failed for", task.Action, task.Engine)
	}
	if task.Action != core.ActionMigrateTLS {
		return nil
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if raw, ok := strings.CutPrefix(line, "QCH_RECEIPT "); ok {
			var result core.TLSMigrationResult
			if json.Unmarshal([]byte(raw), &result) == nil {
				return &result
			}
		}
	}
	t.Fatal("missing structured Agent receipt")
	return nil
}

func releaseTLSClient(t *testing.T, ctx context.Context, key configScopeAPIClient, secret string) func(string, string, any, bool) map[string]any {
	root := installReleaseSkill(t, ctx, key)
	httpServer := httptest.NewServer(key.handler)
	t.Cleanup(httpServer.Close)
	return func(method, path string, payload any, wait bool) map[string]any {
		t.Helper()
		args := []string{filepath.Join(root, "scripts/qch.py"), method, path}
		if payload != nil {
			args = append(args, "--stdin")
		}
		if wait {
			args = append(args, "--wait", "--budget", "100")
		}
		cmd := exec.CommandContext(ctx, "python3", args...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "QCH_API_BASE=" + httpServer.URL + "/api/v1", "QCH_API_KEY=" + secret}
		if payload != nil {
			raw, _ := json.Marshal(payload)
			cmd.Stdin = bytes.NewReader(raw)
		}
		raw, err := cmd.Output()
		if err != nil {
			t.Fatal("release helper request failed", method)
		}
		var result map[string]any
		if json.Unmarshal(raw, &result) != nil {
			t.Fatal("invalid release helper output")
		}
		return result
	}
}
