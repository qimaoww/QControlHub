package webui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTaskStatusSnapshotContainsOnlyLiveFeedbackMetadata(t *testing.T) {
	t.Parallel()
	snapshot := taskStatusSnapshot(core.Task{
		ID: "tsk_0123456789abcdef", AgentID: "agt_secret", Action: core.ActionDeploy,
		Engine: core.EngineXray, Status: core.TaskRunning, Simulated: true, Attempt: 2,
		Output: "sensitive output", Error: "sensitive error", ConfigContent: "sensitive configuration",
	}, core.Overview{TasksPending: 4, TasksQueued: 3, TasksRunning: 1})
	if snapshot.ID != "tsk_0123456789abcdef" || snapshot.Status != core.TaskRunning || snapshot.Action != core.ActionDeploy ||
		snapshot.Engine != core.EngineXray || !snapshot.Simulated || snapshot.Attempt != 2 || snapshot.Timing == "" || !snapshot.HasResult ||
		snapshot.TasksActive != 4 || snapshot.TasksQueued != 3 || snapshot.TasksRunning != 1 {
		t.Fatalf("task status snapshot = %+v", snapshot)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"sensitive output", "sensitive error", "sensitive configuration", "agt_secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("task status response leaked %q: %s", secret, encoded)
		}
	}
}
