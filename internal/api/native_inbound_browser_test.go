package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestNativeInboundWorkspaceBrowserSessions(t *testing.T) {
	if os.Getenv("QCH_MULTI_USER_BROWSER") != "1" {
		t.Skip("enable with QCH_MULTI_USER_BROWSER=1")
	}
	for _, mode := range []string{"desktop", "mobile"} {
		t.Run(mode, func(t *testing.T) {
			db, ctx, _, owner, _ := newConfigScopeAPIFixtureWithin(t, 90*time.Second)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "配置验收节点", core.EngineXray)
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{
				Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, ServiceStatus: "active"}},
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.SetAgentClientAddress(ctx, agent.ID, "native.example.test"); err != nil {
				t.Fatal(err)
			}
			base := "/agents/" + agent.ID + "/configs/xray"
			owner.call("PUT", base, core.Config{Name: "混合入站", Content: mixedNativeXraySource}, 200, nil)
			taskCtx, stop := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() {
				defer close(done)
				ticker := time.NewTicker(200 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-taskCtx.Done():
						return
					case <-ticker.C:
						task, err := db.ClaimTask(taskCtx, agent.ID)
						if err == nil && task != nil {
							if err := db.CompleteTask(taskCtx, agent.ID, task.ID, core.TaskResultRequest{
								LeaseID: task.LeaseID, Success: true, TrafficSettled: true,
							}); err != nil && taskCtx.Err() == nil {
								t.Error("simulate Agent deployment:", err)
							}
						}
					}
				}
			}()
			defer func() { stop(); <-done }()

			frontend := filepath.Join("..", "..", "frontend")
			mux := http.NewServeMux()
			mux.Handle("/api/", owner.handler)
			mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(frontend))))
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				http.ServeFile(w, r, filepath.Join(frontend, "index.html"))
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			fixture, err := json.Marshal(map[string]string{"nativeInboundAgent": agent.ID})
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, "node", filepath.Join(frontend, "multi_user_browser_session.mjs"), server.URL, mode)
			command.Env = append(os.Environ(), "QCH_MULTI_USER_FIXTURE="+string(fixture))
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("native inbound browser: %v\n%s", err, output)
			}
			t.Log(string(output))
			tasks, err := db.ListTasks(ctx, agent.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			if count := len(executionTasks(t, tasks)); count != 3 {
				t.Fatalf("expected three explicit deployments, got %d", count)
			}
		})
	}
}
