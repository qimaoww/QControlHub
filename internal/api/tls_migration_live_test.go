package api

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
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
