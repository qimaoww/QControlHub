package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Legacy hosts can have running, anonymous ownership rows and no retained
// identity. A fresh socket observation resolves only port safety, for both
// host administrators and isolated recipients, without importing that source.
func TestLegacyHostPortRecoveryAcrossEnginesAndInstances(t *testing.T) {
	for _, original := range sharedEngineConfigs() {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%v", original.Engine, shared), func(t *testing.T) {
				db, ctx, _ := isolatedConfigScopeStore(t)
				_, owner := sharedTestUser(t, db, ctx, "port-owner")
				user, recipient := sharedTestUser(t, db, ctx, "port-recipient")
				node := allEnginesSharedTestAgent(t, db, owner)
				scope := owner
				if shared {
					access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: node.ID, Engines: core.AllEngines(), Ports: []int{0}}}})
					if err != nil {
						t.Fatal(err)
					}
					acceptSharedTestInvitations(t, db, ctx, user.ID, access)
					scope = recipient
				}
				features := []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCoreLifecycle, core.AgentFeatureInstanceStatus, core.AgentFeatureHostPorts}
				if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features}); err != nil {
					t.Fatal(err)
				}
				foreign := core.EngineMihomo
				if original.Engine == foreign {
					foreign = core.EngineXray
				}
				if _, err := db.pool.Exec(ctx, `INSERT INTO agent_engine_ownership(agent_id,engine,owner_id,config_id,config_version,running,uncertain,config_uncertain,updated_at) VALUES($1,$2,'','',0,true,false,true,now())`, node.ID, foreign); err != nil {
					t.Fatal(err)
				}
				original.AgentID = node.ID
				// Each supported fixture has one distinct port from this range.
				for port := 21001; port <= 21004; port++ {
					original.Content = strings.ReplaceAll(original.Content, fmt.Sprint(port), "32411")
				}
				config, err := db.SaveAgentConfig(scope, original, 0)
				if err != nil {
					t.Fatal(err)
				}
				check := func(wantConflict bool) {
					t.Helper()
					_, err := db.CreateTask(scope, core.TaskRequest{AgentID: node.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionValidate})
					if errors.Is(err, ErrConflict) != wantConflict || err != nil && !wantConflict {
						t.Fatalf("conflict=%v: %v", wantConflict, err)
					}
					if wantConflict {
						var conflict *PortConflictError
						if !errors.As(err, &conflict) {
							t.Fatalf("missing structured conflict: %v", err)
						}
					}
				}
				check(true)
				deployed := false
				refresh := func(ports []int) {
					t.Helper()
					task, err := db.CreateTask(scope, core.TaskRequest{AgentID: node.ID, Engine: config.Engine, Action: core.ActionStatus})
					if err != nil {
						t.Fatal(err)
					}
					lease, err := db.ClaimTask(ctx, node.ID)
					if err != nil || lease == nil || lease.ID != task.ID {
						t.Fatalf("status claim: %v", err)
					}
					observation := core.SharedInstanceStatus{Engine: config.Engine, ShareID: lease.SharedTrafficID, TaskID: task.ID, Status: "inactive", ObservedAt: time.Now().UTC()}
					if deployed {
						observation.ConfigID = config.ID
						observation.ConfigVersion = config.Version
					}
					if err := db.CompleteTask(ctx, node.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, InstanceStatus: &observation, HostPorts: &core.HostPortObservation{ObservedAt: time.Now().UTC(), Ports: ports}}); err != nil {
						t.Fatal(err)
					}
				}
				refresh([]int{32411})
				check(true)
				refresh([]int{22})
				check(false)
				// Finish the read-only validation, then retain a real deployment identity.
				validate, err := db.ClaimTask(ctx, node.ID)
				if err != nil || validate == nil {
					t.Fatalf("claim validation: %v", err)
				}
				if err := completeSharedTestTask(db, ctx, node.ID, validate.ID, core.TaskResultRequest{LeaseID: validate.LeaseID, Success: true}); err != nil {
					t.Fatal(err)
				}
				task, err := db.CreateTask(scope, core.TaskRequest{AgentID: node.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
				if err != nil {
					t.Fatal(err)
				}
				lease, err := db.ClaimTask(ctx, node.ID)
				if err != nil || lease == nil || lease.ID != task.ID {
					t.Fatalf("claim deploy: %v", err)
				}
				if err := completeSharedTestTask(db, ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
					t.Fatal(err)
				}
				deployed = true
				refresh([]int{32411})
				check(false)
				// An intervening service operation invalidates even a new observation.
				// Finish the previous validation so the status lease is the next task.
				validate, err = db.ClaimTask(ctx, node.ID)
				if err != nil || validate == nil {
					t.Fatalf("claim retained validation: %v", err)
				}
				if err := completeSharedTestTask(db, ctx, node.ID, validate.ID, core.TaskResultRequest{LeaseID: validate.LeaseID, Success: true}); err != nil {
					t.Fatal(err)
				}
				if _, err := db.pool.Exec(ctx, `UPDATE agent_host_port_observations SET observed_at=now()-interval '16 seconds' WHERE agent_id=$1`, node.ID); err != nil {
					t.Fatal(err)
				}
				check(true)
				refresh([]int{32411})
				if _, err := db.CreateTask(scope, core.TaskRequest{AgentID: node.ID, Engine: config.Engine, Action: core.ActionRestart}); err != nil {
					t.Fatal(err)
				}
				check(true)
				var uncertain bool
				if err := db.pool.QueryRow(ctx, `SELECT config_uncertain FROM agent_engine_ownership WHERE agent_id=$1 AND engine=$2`, node.ID, foreign).Scan(&uncertain); err != nil || !uncertain {
					t.Fatalf("port proof reassigned legacy configuration identity: %v", err)
				}
			})
		}
	}
}

func TestHostPortsUpgradeFromV91(t *testing.T) {
	db, ctx, url := isolatedConfigScopeStore(t)
	if _, err := db.pool.Exec(ctx, `DROP TABLE agent_host_port_observations; DELETE FROM qcontrolhub_schema_migrations WHERE version>=92; INSERT INTO qcontrolhub_schema_migrations(version) VALUES(91) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(ctx, url, true)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	var count int
	if err := migrated.pool.QueryRow(ctx, `SELECT count(*) FROM agent_host_port_observations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("upgrade failed: %v", err)
	}
}
