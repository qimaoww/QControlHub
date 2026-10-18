//go:build ignore

// Task-owned local HTTP/PostgreSQL fixture. Deployments use simulated Agent acknowledgements.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/api"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func main() {
	u, _ := url.Parse(os.Getenv("QCH_DATABASE_URL"))
	if u == nil || u.Hostname() != "127.0.0.1" || u.Port() != "55591" || u.Path != "/qch_traffic_fixture" {
		log.Fatal("dedicated local fixture database required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	db, err := store.OpenWithConfigKey(ctx, u.String(), true, "traffic-owner-bulk-fixture-key-2026")
	must(err)
	defer db.Close()
	users, err := db.ListUsers(ctx)
	must(err)
	ids := map[string]string{}
	for _, user := range users {
		ids[user.Username] = user.ID
	}
	hash, err := authn.HashPassword("traffic fixture password")
	must(err)
	permissions := []core.Permission{}
	for _, p := range core.AllPermissions() {
		if p != core.PermissionUsersManage {
			permissions = append(permissions, p)
		}
	}
	for _, name := range []string{"traffic-admin", "traffic-alice", "traffic-bob"} {
		if ids[name] != "" {
			continue
		}
		role := core.RoleUser
		if name == "traffic-admin" {
			role = core.RoleAdmin
		}
		user, e := db.CreateUser(ctx, core.UserRequest{Username: name, DisplayName: map[string]string{"traffic-admin": "管理员", "traffic-alice": "Alice", "traffic-bob": "Bob"}[name], Role: role, Permissions: permissions}, hash)
		must(e)
		ids[name] = user.ID
	}
	nodes, err := db.ListAgents(ctx)
	must(err)
	if len(nodes) == 0 {
		for _, spec := range []struct{ name, owner string }{{"香港 · 共享节点", "traffic-admin"}, {"东京 · Alice 节点", "traffic-alice"}} {
			scoped := store.WithConfigScope(ctx, ids[spec.owner], spec.owner == "traffic-admin")
			token, e := db.CreateEnrollmentToken(scoped, core.EnrollmentTokenRequest{Name: spec.name})
			must(e)
			pub, _, e := ed25519.GenerateKey(rand.Reader)
			must(e)
			a, e := db.EnrollAgent(ctx, core.EnrollRequest{Name: spec.name, OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineXray}, Features: []string{core.AgentFeaturePortTraffic, core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCoreLifecycle, core.AgentFeatureSharedCorePrepare}, PublicKey: authn.EncodePublicKey(pub)}, token.Token)
			must(e)
			must(db.Heartbeat(ctx, a.ID, core.HeartbeatRequest{Features: a.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, ServiceStatus: "active"}}}))
			nodes = append(nodes, a)
		}
		grant := func(name string, a core.Agent, ports []int) {
			access, e := db.SetUserAgentAccess(ctx, ids[name], core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: a.ID, Engines: a.Capabilities, Ports: ports, LimitBytes: 10 << 30}}})
			must(e)
			for _, sh := range access.Shares {
				_, e = db.RespondAgentShare(store.WithConfigScope(ctx, ids[name], name == "traffic-admin"), sh.ID, core.AgentShareResponseRequest{Revision: sh.InvitationRevision, Decision: "accept"})
				must(e)
			}
		}
		grant("traffic-alice", nodes[0], []int{22001, 22002})
		grant("traffic-bob", nodes[0], []int{22011})
		deploy := func(name string, a core.Agent, ports []int) {
			scoped := store.WithConfigScope(ctx, ids[name], name == "traffic-admin")
			var in []map[string]any
			for _, port := range ports {
				in = append(in, map[string]any{"tag": fmt.Sprintf("in-%d", port), "protocol": "http", "port": port, "settings": map[string]any{"auth": "noauth"}})
			}
			raw, _ := json.Marshal(map[string]any{"inbounds": in, "outbounds": []map[string]any{{"tag": "direct", "protocol": "freedom"}}})
			c, e := db.SaveAgentConfig(scoped, core.Config{AgentID: a.ID, Engine: core.EngineXray, Name: "流量验证", Content: string(raw)}, 0)
			must(e)
			_, e = db.CreateTask(scoped, core.TaskRequest{AgentID: a.ID, Engine: c.Engine, ConfigID: c.ID, Action: core.ActionDeploy, ExpectedConfigVersion: c.Version})
			must(e)
			complete(db, ctx, a)
		}
		deploy("traffic-admin", nodes[0], []int{21001, 21002})
		deploy("traffic-alice", nodes[0], []int{22001})
		deploy("traffic-bob", nodes[0], []int{22011})
		deploy("traffic-alice", nodes[1], []int{21101})
		off := false
		_, e := db.CreatePortTrafficPolicy(store.WithConfigScope(ctx, ids["traffic-admin"], true), core.PortTrafficPolicyRequest{AgentID: nodes[0].ID, Name: "未部署的残留配额", Engine: core.EngineXray, Port: 21003, Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: time.Now().UTC().AddDate(0, 0, -1), LimitBytes: 1 << 30, AutoBlock: &off})
		must(e)
	}
	handler := api.New(db, api.Config{AdminToken: "traffic-local-admin-token", ConfigEncryptionConfigured: true}).Handler()
	mux := http.NewServeMux()
	mux.Handle("/api/", handler)
	mux.Handle("/agent/", handler)
	root := os.Getenv("QCH_FIXTURE_FRONTEND")
	if root == "" {
		root = "frontend"
	}
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(root))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, filepath.Join(root, "index.html"))
	})
	server := &http.Server{Addr: "127.0.0.1:55592", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = server.Close() }()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, a := range nodes {
					must(db.Heartbeat(ctx, a.ID, core.HeartbeatRequest{Features: a.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true, ServiceStatus: "active"}}}))
					complete(db, ctx, a)
				}
			}
		}
	}()
	raw, _ := json.MarshalIndent(map[string]any{"users": ids, "agents": nodes}, "", "  ")
	must(os.WriteFile("output/playwright/traffic-owner-bulk/fixture.json", raw, 0600))
	log.Print("fixture ready http://127.0.0.1:55592; simulated Agent results")
	if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
func complete(db *store.Store, ctx context.Context, a core.Agent) {
	lease, e := db.ClaimTask(ctx, a.ID)
	must(e)
	if lease == nil {
		return
	}
	result := core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, TrafficSettled: true}
	if lease.SharedInstance {
		status := "active"
		if lease.Action == core.ActionStop {
			status = "inactive"
		}
		applied := ""
		if lease.Action == core.ActionDeploy {
			applied = lease.ID
		}
		result.InstanceStatus = &core.SharedInstanceStatus{Engine: lease.Engine, ShareID: lease.SharedTrafficID, ConfigID: lease.ConfigID, ConfigVersion: lease.ConfigVersion, TaskID: lease.ID, AppliedTaskID: applied, Status: status, ObservedAt: time.Now().UTC()}
	}
	must(db.CompleteTask(ctx, a.ID, lease.ID, result))
}
func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
