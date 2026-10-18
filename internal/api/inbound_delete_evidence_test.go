package api

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// Opt-in real HTTP/PostgreSQL fixture. Task acknowledgements are simulated;
// this captures panel behavior, not service or network accounting evidence.
func TestInboundDeleteEvidence(t *testing.T) {
	if os.Getenv("QCH_DELETE_EVIDENCE") != "1" {
		t.Skip("interactive deletion evidence")
	}
	ctx := context.Background()
	url := os.Getenv("QCH_DELETE_EVIDENCE_URL")
	if url == "" {
		url = os.Getenv("QCH_TEST_DATABASE_URL")
	}
	db, err := store.Open(ctx, url, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := New(db, Config{AdminToken: "delete-evidence-administrator-token"}).Handler()
	admin := configScopeAPIClient{t: t, handler: handler, token: "delete-evidence-administrator-token"}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM agents`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		var user core.User
		admin.call("POST", "/users", core.UserRequest{Username: "delete-review", Password: "delete evidence password", Role: core.RoleAdmin}, http.StatusCreated, &user)
		owner := store.WithConfigScope(ctx, user.ID, true)
		credential, err := db.CreateEnrollmentToken(owner, core.EnrollmentTokenRequest{Name: "delete evidence"})
		if err != nil {
			t.Fatal(err)
		}
		agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "删除链路验证", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineXray}, Features: []string{core.AgentFeatureIndependentEgress, core.AgentFeaturePortTraffic}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, credential.Token)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, ServiceStatus: "active"}}}); err != nil {
			t.Fatal(err)
		}
		content := `{"inbounds":[{"tag":"keep","protocol":"socks","port":21001,"settings":{"auth":"noauth","udp":true}},{"tag":"remove","protocol":"socks","port":21002,"settings":{"auth":"noauth","udp":true}}],"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"exit","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["remove"],"outboundTag":"exit"},{"type":"field","domain":["example.test"],"outboundTag":"direct"}]}}`
		// Persist the old exact generated revision, then emulate an old manual save
		// changing the port without regenerating the managed exits and routes.
		plan, err := evidencePlan(content)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := db.SaveAgentConfig(owner, core.Config{AgentID: agent.ID, Name: "删除链路验证", Engine: core.EngineXray, Content: plan}, 0)
		if err != nil {
			t.Fatal(err)
		}
		task, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineXray, Action: core.ActionDeploy, ConfigID: saved.ID})
		if err != nil {
			t.Fatal(err)
		}
		lease, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || lease == nil {
			t.Fatalf("lease %v", err)
		}
		if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, TrafficSettled: true}); err != nil {
			t.Fatal(err)
		}
		saved.Content = strings.Replace(saved.Content, `"port": 21002`, `"port": 21003`, 1)
		if _, err := db.SaveAgentConfig(owner, saved, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `UPDATE port_traffic_policies SET quota_enabled=true,limit_bytes=1073741824,used_bytes=12345,received_bytes=12345,receive_bps=2048,last_reported_at=now() WHERE port=21002`); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join("..", "..", "frontend")
	mux := http.NewServeMux()
	mux.Handle("/api/", handler)
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(root))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, filepath.Join(root, "index.html")) })
	listener, err := net.Listen("tcp", "127.0.0.1:18584")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go server.Serve(listener)
	t.Log("delete evidence http://127.0.0.1:18584")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		<-ticker.C
		_, _ = conn.Exec(ctx, `UPDATE agents SET last_seen=now()`)
		// Deliberately leave time to observe the deployment pending feedback.
		lease, err := db.ClaimTask(ctx, evidenceAgentID(ctx, conn))
		if err != nil || lease == nil {
			continue
		}
		time.Sleep(3 * time.Second)
		result := core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}
		if lease.Action == core.ActionReadConfig || lease.Action == core.ActionReadManagedConfig {
			_ = conn.QueryRow(ctx, `SELECT content FROM configs WHERE agent_id=$1 AND engine=$2 AND deleted_at IS NULL LIMIT 1`, lease.AgentID, lease.Engine).Scan(&result.Output)
		}
		if err := db.CompleteTask(ctx, lease.AgentID, lease.ID, result); err != nil {
			t.Error(err)
		}
	}
}
func evidenceAgentID(ctx context.Context, c *pgx.Conn) string {
	var id string
	_ = c.QueryRow(ctx, `SELECT id FROM agents LIMIT 1`).Scan(&id)
	return id
}

func evidencePlan(content string) (string, error) {
	p, e := serverconfig.PrepareAccounting(core.EngineXray, content)
	return p.Content, e
}
