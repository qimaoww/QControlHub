//go:build ignore

// Local evidence harness. Database and fixtures are task-owned and retained by
// the separately installed cleanup timer. This program never touches prod.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"log"
	"net"
	"net/http"
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
	if os.Getenv("QCH_CERTIFICATE_LOCAL_ACME") == "1" {
		certificates.Directories["letsencrypt-staging"] = "https://localhost:35481/dir"
		net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:35483")
		}}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	db, err := store.OpenWithConfigKey(ctx, os.Getenv("QCH_DATABASE_URL"), true, "certificate-local-fixture-encryption-key-2026")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	hash, err := authn.HashPassword("certificate local fixture password")
	if err != nil {
		log.Fatal(err)
	}
	users, err := db.ListUsers(ctx)
	if err != nil {
		log.Fatal(err)
	}
	ids := map[string]string{}
	for _, u := range users {
		ids[u.Username] = u.ID
	}
	permissions := []core.Permission{}
	for _, p := range core.AllPermissions() {
		if p != core.PermissionUsersManage {
			permissions = append(permissions, p)
		}
	}
	for _, name := range []string{"cert-admin", "cert-alice", "cert-bob"} {
		if ids[name] != "" {
			continue
		}
		role := core.RoleUser
		if name == "cert-admin" {
			role = core.RoleAdmin
		}
		user, err := db.CreateUser(ctx, core.UserRequest{Username: name, DisplayName: map[string]string{"cert-admin": "证书管理员", "cert-alice": "Alice", "cert-bob": "Bob"}[name], Role: role, Permissions: permissions}, hash)
		if err != nil {
			log.Fatal(err)
		}
		ids[name] = user.ID
	}
	nodes, err := db.ListAgents(ctx)
	if err != nil {
		log.Fatal(err)
	}
	byName := map[string]core.Agent{}
	for _, a := range nodes {
		byName[a.Name] = a
	}
	for _, name := range []string{"HTTP 验证节点 · 香港", "TLS 部署节点 · 东京", "共享实例 · 新加坡"} {
		if byName[name].ID != "" {
			continue
		}
		owner := "cert-alice"
		if name == "共享实例 · 新加坡" {
			owner = "cert-admin"
		}
		ownerCtx := store.WithConfigScope(ctx, ids[owner], owner == "cert-admin")
		token, err := db.CreateEnrollmentToken(ownerCtx, core.EnrollmentTokenRequest{Name: name})
		if err != nil {
			log.Fatal(err)
		}
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			log.Fatal(err)
		}
		a, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: name, OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineMihomo, core.EngineSingBox}, Features: []string{core.AgentFeatureCertificates, core.AgentFeatureIndependentEgress, core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare}, PublicKey: authn.EncodePublicKey(pub)}, token.Token)
		if err != nil {
			log.Fatal(err)
		}
		byName[name] = a
	}
	shared := byName["共享实例 · 新加坡"]
	for index, name := range []string{"cert-alice", "cert-bob"} {
		access, err := db.UserAgentAccess(ctx, ids[name])
		if err != nil {
			log.Fatal(err)
		}
		if len(access.Shares) == 0 {
			access, err = db.SetUserAgentAccess(ctx, ids[name], core.AgentAccessRequest{Revision: access.Revision, Isolated: true, Shares: []core.AgentShareRequest{{AgentID: shared.ID, Engines: shared.Capabilities, Ports: []int{24443 + index, 24453 + index}}}})
			if err != nil {
				log.Fatal(err)
			}
		}
		for _, sh := range access.Shares {
			if sh.Status == core.AgentSharePending {
				_, err = db.RespondAgentShare(store.WithConfigScope(ctx, ids[name], false), sh.ID, core.AgentShareResponseRequest{Revision: sh.InvitationRevision, Decision: "accept"})
				if err != nil {
					log.Fatal(err)
				}
			}
		}
	}
	handler := api.New(db, api.Config{ConfigEncryptionConfigured: true}).Handler()
	serve := func(port, root string) {
		mux := http.NewServeMux()
		mux.Handle("/api/", handler)
		mux.Handle("/agent/", handler)
		mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(root))))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, filepath.Join(root, "index.html"))
		})
		server := &http.Server{Addr: "127.0.0.1:" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() { <-ctx.Done(); _ = server.Close() }()
		go server.ListenAndServe()
	}
	serve("35479", "frontend")
	serve("35480", ".preview/certificate-center-a978/baseline/frontend")
	raw, _ := json.Marshal(map[string]any{"users": ids, "nodes": byName})
	_ = os.WriteFile(".preview/certificate-center-a978/fixture.json", raw, 0600)
	go func() {
		for {
			if err := db.RunCertificateMaintenance(ctx); err != nil && ctx.Err() == nil {
				log.Print(err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		for _, a := range byName {
			_ = db.Heartbeat(ctx, a.ID, core.HeartbeatRequest{Features: a.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true, ServiceStatus: "inactive"}, core.EngineSingBox: {Installed: true, ServiceStatus: "inactive"}}})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
