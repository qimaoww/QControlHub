package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	var reads atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/api-key" && r.Method == http.MethodGet && r.Header.Get("Authorization") == "Bearer "+secret {
			rec := httptest.NewRecorder()
			key.handler.ServeHTTP(rec, r)
			if rec.Code == http.StatusOK {
				reads.Add(1)
			}
			for k, vs := range rec.Header() {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
		key.handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	if os.Getenv("QCH_TEST_SKILL_SESSIONS") == "1" {
		for _, client := range []string{"codex", "dsh"} {
			t.Run(client+"-session", func(t *testing.T) {
				start := reads.Load()
				cmd := exec.CommandContext(ctx, "python3", "../../scripts/check-management-skill-session.py", root, client)
				cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "QCH_API_BASE=" + httpServer.URL + "/api/v1", "QCH_API_KEY=" + secret}
				err := cmd.Run()
				if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 && client == "dsh" && reads.Load() == start {
					t.Skip("DSH loaded the Skill; its Bash subprocess filtered QCH_API_KEY; no authorized read")
				}
				if reads.Load() == start || err != nil {
					t.Errorf("fresh %s session did not complete an authorized read", client)
				}
			})
		}
	}
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
