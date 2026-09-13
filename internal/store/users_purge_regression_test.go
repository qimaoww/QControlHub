package store

import (
	"encoding/base64"
	"errors"
	"slices"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPurgeUserRevokesInstallationCredentials(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "active"
		if disabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			user, owner := sharedTestUser(t, db, ctx, "credential-owner")
			token, err := db.CreateProtectedEnrollmentToken(owner, core.EnrollmentTokenRequest{Name: "retained-node", Reusable: true})
			if err != nil {
				t.Fatal(err)
			}
			key := testEnrollmentPublicKey(t)
			request := core.EnrollRequest{Name: token.Name, OS: "linux", Arch: "amd64",
				Capabilities: []core.Engine{core.EngineMihomo}, PublicKey: base64.RawURLEncoding.EncodeToString(key)}
			agent, err := db.EnrollAgent(ctx, request, token.Token)
			if err != nil {
				t.Fatal(err)
			}
			// An unbound reusable command may have the same name in two
			// workspaces. Revocation must not collide with the admin's command.
			spare, err := db.CreateProtectedEnrollmentToken(owner, core.EnrollmentTokenRequest{Name: "spare-node", Reusable: true})
			if err != nil {
				t.Fatal(err)
			}
			adminName := spare.Name
			if !disabled {
				adminName += "-admin"
			}
			adminToken, err := db.CreateProtectedEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: adminName, Reusable: true})
			if err != nil {
				t.Fatal(err)
			}
			if disabled {
				if _, err := db.SetUserDisabled(ctx, user.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.PurgeUser(ctx, user.ID); err != nil {
				t.Fatalf("purge with same-named commands: %v", err)
			}
			for _, credential := range []core.EnrollmentTokenCreated{token, spare} {
				if db.EnrollmentTokenUsable(ctx, credential.Token) {
					t.Errorf("deleted user's installation credential %s is still usable", credential.ID)
				}
				var revoked, erased bool
				var ownerID string
				if err := db.pool.QueryRow(ctx, `SELECT owner_id,revoked_at IS NOT NULL,token_ciphertext IS NULL
					FROM enrollment_tokens WHERE id=$1`, credential.ID).Scan(&ownerID, &revoked, &erased); err != nil {
					t.Fatal(err)
				}
				if ownerID != "" || !revoked || !erased {
					t.Errorf("credential was not safely retired: owner=%q revoked=%t erased=%t", ownerID, revoked, erased)
				}
			}
			request.PublicKey = base64.RawURLEncoding.EncodeToString(testEnrollmentPublicKey(t))
			if _, err := db.EnrollAgent(ctx, request, token.Token); !errors.Is(err, ErrNotFound) {
				t.Fatalf("former owner replaced the retained Agent identity: %v", err)
			}
			if retained, err := db.AgentPublicKey(ctx, agent.ID); err != nil || !slices.Equal(retained, key) {
				t.Fatalf("purge changed the live Agent identity: %v", err)
			}
			if !db.EnrollmentTokenUsable(ctx, adminToken.Token) {
				t.Fatal("purge revoked an unrelated administrator credential")
			}
		})
	}
}

func TestPurgeUserInvalidatesWorkBeforeTransferringOwnership(t *testing.T) {
	for _, purgeOwner := range []bool{false, true} {
		name := "recipient"
		if purgeOwner {
			name = "owner"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			ownerUser, owner := sharedTestUser(t, db, ctx, "task-owner")
			recipientUser, recipient := sharedTestUser(t, db, ctx, "task-recipient")
			agent := sharedTestAgent(t, db, owner)
			sharedTestAllocation(t, db, ctx, recipientUser.ID, agent.ID, 0, 21001)
			config := sharedTestConfig(t, db, recipient, agent.ID, 21001)
			running, err := db.CreateTask(recipient, core.TaskRequest{
				AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy,
			})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || lease == nil || lease.ID != running.ID {
				t.Fatalf("claim deployment: %+v %v", lease, err)
			}
			queued, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStatus})
			if err != nil {
				t.Fatal(err)
			}
			ownerTask, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStop})
			if err != nil {
				t.Fatal(err)
			}
			adminTask, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStatus})
			if err != nil {
				t.Fatal(err)
			}
			id := recipientUser.ID
			if purgeOwner {
				id = ownerUser.ID
			}
			if _, err := db.PurgeUser(ctx, id); err != nil {
				t.Fatal(err)
			}
			// Inspect immediately, before any Agent poll has a chance to
			// cancel work. Revocation must be part of the purge transaction.
			for _, task := range []core.Task{running, queued, ownerTask, adminTask} {
				want := core.TaskCanceled
				if task.ID == running.ID {
					want = core.TaskFailed
				} else if task.ID == adminTask.ID || (task.ID == ownerTask.ID && !purgeOwner) {
					want = core.TaskPending
				}
				got, err := db.GetTask(ctx, task.ID)
				if err != nil || got.Status != want {
					t.Errorf("task %s status=%s, want %s: %v", task.ID, got.Status, want, err)
				}
			}
			if resumed, err := db.RunningTask(ctx, agent.ID); err != nil || resumed != nil {
				t.Errorf("purge kept an old running lease: %+v %v", resumed, err)
			}
			if err := db.CompleteTask(ctx, agent.ID, running.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); !errors.Is(err, ErrConflict) {
				t.Errorf("purge accepted the old deployment result: %v", err)
			}
			var uncertain, configUncertain bool
			if err := db.pool.QueryRow(ctx, `SELECT uncertain,config_uncertain FROM agent_engine_ownership
				WHERE agent_id=$1 AND engine=$2`, agent.ID, config.Engine).Scan(&uncertain, &configUncertain); err != nil {
				t.Fatal(err)
			}
			if !uncertain || !configUncertain {
				t.Fatal("purge incorrectly marked in-flight execution as settled")
			}
		})
	}
}

func TestPurgeUserPreservesCollidingConfigurationWorkspaces(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, owner := sharedTestUser(t, db, ctx, "workspace-owner")
	admin := WithConfigScope(ctx, "", true)
	agent := sharedTestAgent(t, db, owner)
	source := sharedTestConfig(t, db, owner, agent.ID, 21001)
	firstContent := source.Content
	source.Content += "# retained revision\n"
	source, err := db.SaveAgentConfigWithClientMetadata(owner, source, source.Version,
		ConfigClientMetadataMutation{Tag: "p21001", Content: `{"client":"retained-metadata"}`})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := db.CreateTask(owner, core.TaskRequest{
		AgentID: agent.ID, Engine: source.Engine, ConfigID: source.ID, Action: core.ActionDeploy,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || lease.ID != deployment.ID {
		t.Fatalf("claim retained deployment: %+v %v", lease, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, deployment.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	existing := sharedTestConfig(t, db, admin, agent.ID, 21002)
	if _, err := db.PurgeUser(admin, user.ID); err != nil {
		t.Fatalf("purge with an existing administrator workspace: %v", err)
	}
	current, err := db.AgentConfig(admin, agent.ID, source.Engine)
	if err != nil || current.ID != existing.ID || current.Content != existing.Content {
		t.Fatalf("purge overwrote the administrator's workspace: %+v %v", current, err)
	}
	archives, err := db.ListConfigs(admin)
	if err != nil || !slices.ContainsFunc(archives, func(config core.Config) bool {
		return config.ID == source.ID && config.AgentID == "" && config.OwnerID == "" && config.Content == source.Content
	}) {
		t.Fatalf("colliding workspace was not preserved as an archive: %+v %v", archives, err)
	}
	if revision, err := db.ConfigRevision(admin, source.ID, 1); err != nil || revision.Content != firstContent {
		t.Fatalf("transferred configuration lost its revision: %+v %v", revision, err)
	}
	if metadata, err := db.ConfigClientMetadata(admin, source.ID, source.Version); err != nil || metadata["p21001"] != `{"client":"retained-metadata"}` {
		t.Fatalf("transferred configuration lost its encrypted metadata: %+v %v", metadata, err)
	}
	deployed, err := db.DeployedConfigs(admin)
	if err != nil || len(deployed) != 1 || deployed[0].Config.ID != source.ID || deployed[0].Config.Content != source.Content {
		t.Fatalf("archiving a collision lost the active deployment: %+v %v", deployed, err)
	}
	if task, err := db.GetTask(admin, deployment.ID); err != nil || task.Status != core.TaskSucceeded {
		t.Fatalf("purge changed completed task history: %+v %v", task, err)
	}
}

func TestPurgeUserUnhidesTransferredNodesForNamedAdministrators(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, owner := sharedTestUser(t, db, ctx, "hidden-owner")
	_, otherOwner := sharedTestUser(t, db, ctx, "other-hidden-owner")
	administrator, err := db.CreateUser(ctx, core.UserRequest{Username: "named-admin", Role: core.RoleAdmin}, "test-only-hash")
	if err != nil {
		t.Fatal(err)
	}
	admin := WithConfigScope(ctx, administrator.ID, true)
	agent, other, own := sharedTestAgent(t, db, owner), sharedTestAgent(t, db, otherOwner), sharedTestAgent(t, db, admin)
	for _, id := range []string{agent.ID, other.ID} {
		if _, err := db.pool.Exec(ctx, "UPDATE agents SET admin_hidden=true WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.PurgeUser(admin, user.ID); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAgent(admin, agent.ID)
	if err != nil || got.AdminHidden || got.OwnerID != "" {
		t.Errorf("named administrator cannot manage the transferred node: %+v %v", got, err)
	}
	if _, err := db.GetAgent(admin, other.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("purge exposed another account's hidden node: %v", err)
	}
	directory, err := db.ListAgentDirectory(admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(directory) != 1 || directory[0].ID != other.ID {
		t.Errorf("directory includes transferred or current administrator nodes (%s, %s): %+v", agent.ID, own.ID, directory)
	}
}

func TestPurgeUserCannotTurnAnInflightUserIntoACompatibilityToken(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, stale := sharedTestUser(t, db, ctx, "inflight-user")
	own, foreign := sharedTestAgent(t, db, stale), sharedTestAgent(t, db, ctx)
	if _, err := db.PurgeUser(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	// A request may have passed session middleware before waiting for the
	// purge's user lock. Missing durable users must not become legacy tokens.
	if agents, err := db.ListAgents(stale); err != nil || len(agents) != 0 {
		t.Errorf("purged request inherited fleet-wide visibility: %d nodes, %v", len(agents), err)
	}
	for _, id := range []string{own.ID, foreign.ID} {
		if _, err := db.CreateTask(stale, core.TaskRequest{AgentID: id, Engine: core.EngineMihomo, Action: core.ActionStop}); err == nil {
			t.Errorf("purged request created a host task on %s", id)
		}
		if _, err := db.CreateAgentEnrollmentToken(stale, id); err == nil {
			t.Errorf("purged request issued a reinstall credential for %s", id)
		}
	}
	if _, err := db.CreateEnrollmentToken(stale, core.EnrollmentTokenRequest{Name: "late-install"}); err == nil {
		t.Error("purged request created an orphan installation credential")
	}
	compatibility := WithConfigScope(ctx, "token_purge_regression", false)
	if _, err := db.CreateTask(compatibility, core.TaskRequest{AgentID: foreign.ID, Engine: core.EngineMihomo, Action: core.ActionStatus}); err != nil {
		t.Fatalf("explicit compatibility token lost its supported access: %v", err)
	}
}

func TestPurgeUserInvalidatesSurvivingSharingRevisions(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, owner := sharedTestUser(t, db, ctx, "revision-owner")
	recipient, _ := sharedTestUser(t, db, ctx, "revision-recipient")
	agent := sharedTestAgent(t, db, owner)
	access := sharedTestAllocation(t, db, ctx, recipient.ID, agent.ID, 0, 21001)
	sharing, err := db.AgentSharing(owner, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.PurgeUser(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	surviving, err := db.UserAgentAccess(ctx, recipient.ID)
	if err != nil || surviving.Revision <= access.Revision || len(surviving.Shares) != 0 {
		t.Errorf("purge did not invalidate the recipient's allocation draft: %+v %v", surviving, err)
	}
	if _, err := db.SetUserAgentAccess(ctx, recipient.ID, core.AgentAccessRequest{
		Isolated: true, Revision: access.Revision,
		Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}}},
	}); !errors.Is(err, ErrConflict) {
		t.Errorf("a stale allocation draft restored a purged share: %v", err)
	}
	updated, err := db.AgentSharing(ctx, agent.ID)
	if err != nil || updated.Revision <= sharing.Revision {
		t.Errorf("purge did not invalidate the node's sharing draft: %+v %v", updated, err)
	}
}
