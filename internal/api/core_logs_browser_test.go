package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

// Opt-in interactive evidence server: all APIs use real PostgreSQL and sessions.
func TestCoreLogsBrowserEvidence(t *testing.T) {
	if os.Getenv("QCH_LOG_EVIDENCE") != "1" {
		t.Skip("interactive browser evidence")
	}
	runCoreLogsBrowser(t, "")
}

func TestCoreLogsBrowserSessions(t *testing.T) {
	if os.Getenv("QCH_CORE_LOG_BROWSER") != "1" {
		t.Skip("enable with QCH_CORE_LOG_BROWSER=1")
	}
	for _, mode := range []string{"desktop", "mobile"} {
		t.Run(mode, func(t *testing.T) { runCoreLogsBrowser(t, mode) })
	}
}

func runCoreLogsBrowser(t *testing.T, mode string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	databaseURL := os.Getenv("QCH_LOG_EVIDENCE_URL")
	reuse := databaseURL != ""
	if !reuse {
		schema, err := testdb.IsolatePostgres(ctx, os.Getenv("QCH_TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer schema.Close(ctx)
		databaseURL = schema.URL
	}
	db, err := store.Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	handler := New(db, Config{AdminToken: "core-log-evidence-admin-token-32bytes"}).Handler()
	admin := configScopeAPIClient{t: t, handler: handler, token: "core-log-evidence-admin-token-32bytes"}
	if !reuse {
		users := map[string]core.User{}
		for _, name := range []string{"logs-admin", "alice", "bob"} {
			role := core.RoleUser
			if name == "logs-admin" {
				role = core.RoleAdmin
			}
			var user core.User
			permissions := []core.Permission{core.PermissionOverviewRead, core.PermissionAgentsRead, core.PermissionCoreLogsRead}
			if mode != "" && name == "alice" {
				permissions = []core.Permission{core.PermissionCoreLogsRead}
			}
			admin.call("POST", "/users", core.UserRequest{Username: name, Password: "core logs evidence password", Role: role, Permissions: permissions}, http.StatusCreated, &user)
			users[name] = user
		}
		for _, name := range []string{"logs-admin", "alice", "bob"} {
			owner := users[name].ID
			credential, err := db.CreateEnrollmentToken(store.WithConfigScope(ctx, owner, name == "logs-admin"), core.EnrollmentTokenRequest{Name: name + " 节点 · 日志隔离验证"})
			if err != nil {
				t.Fatal(err)
			}
			agent := enrollTaskAPIAgent(t, ctx, db, credential.Token, name+" 节点 · 日志隔离验证")
			if _, err := conn.Exec(ctx, `UPDATE agents SET capabilities='["mihomo","xray","sing-box","ss-rust"]'::jsonb,supported_capabilities='["mihomo","xray","sing-box","ss-rust"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
				t.Fatal(err)
			}
			runtime := map[core.Engine]core.RuntimeState{}
			for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
				runtime[engine] = core.RuntimeState{Installed: true, ServiceStatus: "active", CoreLogStatus: "active"}
			}
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Runtime: runtime, Features: []string{core.AgentFeatureCoreLogs, core.AgentFeatureCoreLogStatus, core.AgentFeatureCoreLogInstances, core.AgentFeatureSharedEngines, core.AgentFeatureSharedTraffic, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare, core.AgentFeatureIndependentEgress}}); err != nil {
				t.Fatal(err)
			}
			entries := []core.CoreLogEntry{{Engine: core.EngineMihomo, InstanceID: "main", Service: "qagent-mihomo.service", Level: "info", Message: name + " 主实例 · service started"}, {Engine: core.EngineXray, InstanceID: "main", Service: "qagent-xray.service", Level: "warning", Message: name + " 主实例 · upstream retry"}}
			id, _ := core.NewID("log")
			if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: id, Entries: entries}); err != nil {
				t.Fatal(err)
			}
			if name == "logs-admin" {
				for i, recipient := range []string{"alice", "bob"} {
					share := []string{"shr_0123456789abcdef", "shr_1123456789abcdef"}[i]
					_, err = conn.Exec(ctx, `INSERT INTO agent_shares(id,user_id,agent_id,status,engines) VALUES($1,$2,$3,'accepted',ARRAY['mihomo','xray','sing-box','ss-rust']);`, share, users[recipient].ID, agent.ID)
					if err != nil {
						t.Fatal(err)
					}
					for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
						_, err = conn.Exec(ctx, `INSERT INTO agent_shared_instance_ownership(agent_id,engine,share_id,owner_id,config_id,config_version,updated_at) VALUES($1,$2,$3,$4,'evidence',1,now())`, agent.ID, engine, share, users[recipient].ID)
						if err != nil {
							t.Fatal(err)
						}
						if _, err := conn.Exec(ctx, `INSERT INTO core_log_source_states(agent_id,engine,instance_id,status) VALUES($1,$2,$3,'active')`, agent.ID, engine, share); err != nil {
							t.Fatal(err)
						}
						// Many records exercise UI pagination and bounded history.
						count := 1
						if engine == core.EngineXray && recipient == "alice" && mode != "" {
							count = 210
						}
						for offset := 0; offset < count; offset += core.MaxCoreLogBatchEntries {
							entries := []core.CoreLogEntry{}
							for n := offset; n < min(offset+core.MaxCoreLogBatchEntries, count); n++ {
								entries = append(entries, core.CoreLogEntry{Engine: engine, InstanceID: share, Service: core.CoreLogService(engine, share, "systemd"), Level: "info", Message: recipient + " · 独立实例 service started"})
							}
							id, _ := core.NewID("log")
							if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: id, Entries: entries}); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
			}
		}
	}
	root := filepath.Join("..", "..", "frontend")
	if baseline := os.Getenv("QCH_LOG_BASELINE_ROOT"); baseline != "" {
		root = baseline
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", handler)
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(root))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, filepath.Join(root, "index.html")) })
	address := "127.0.0.1:18549"
	if mode != "" {
		address = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = conn.Exec(ctx, `UPDATE agents SET last_seen=now(); UPDATE core_log_source_states SET updated_at=now()`)
			}
		}
	}()
	go server.Serve(listener)
	defer cancel()
	base := "http://" + listener.Addr().String()
	if mode == "" {
		t.Log("evidence " + base)
		time.Sleep(25 * time.Minute)
		return
	}
	nodes := map[string]string{}
	rows, err := conn.Query(ctx, `SELECT u.username,a.id FROM agents a JOIN panel_users u ON u.id=a.owner_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var user, id string
		if err := rows.Scan(&user, &id); err != nil {
			t.Fatal(err)
		}
		nodes[user] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(nodes)
	command := exec.CommandContext(ctx, "node", filepath.Join("..", "..", "frontend", "core_logs_browser_session.mjs"), base, mode)
	command.Env = append(os.Environ(), "QCH_CORE_LOG_FIXTURE="+string(encoded))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("core logs browser: %v\n%s", err, output)
	}
	t.Log(string(output))
}
