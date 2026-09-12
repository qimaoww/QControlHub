package store

import (
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// TestPurgeUserMovesFleetToAdminScopeAndRevokesAccess proves that deleting an
// account never deletes fleet data: nodes, configurations, templates,
// enrollment credentials, task history and engine ownership join the
// administrator scope, while grants and personal data leave with the account.
func TestPurgeUserMovesFleetToAdminScopeAndRevokesAccess(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	admin := WithConfigScope(ctx, "", true)
	owner, ownerCtx := sharedTestUser(t, db, ctx, "purge-owner")
	recipient, recipientCtx := sharedTestUser(t, db, ctx, "purge-recipient")

	node := sharedTestAgent(t, db, ctx)
	if _, err := db.pool.Exec(ctx, "UPDATE agents SET owner_id=$2 WHERE id=$1", node.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	config, err := db.SaveAgentConfig(ownerCtx, core.Config{
		AgentID: node.ID, Engine: core.EngineMihomo, Name: "purge-config", Content: "mixed-port: 21071\n",
	}, 0)
	if err != nil {
		t.Fatalf("create owned config: %v", err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO agent_engine_ownership
		(agent_id,engine,owner_id,config_id,config_version,running,updated_at)
		VALUES ($1,'mihomo',$2,$3,$4,true,now())
		ON CONFLICT (agent_id,engine) DO UPDATE SET owner_id=EXCLUDED.owner_id,config_id=EXCLUDED.config_id,config_version=EXCLUDED.config_version`,
		node.ID, owner.ID, config.ID, config.Version); err != nil {
		t.Fatal(err)
	}
	template, err := db.CreateConfigTemplate(ownerCtx, "purge-template", string(core.EngineMihomo), "mixed-port: 21072\n")
	if err != nil {
		t.Fatalf("create owned template: %v", err)
	}
	if template.OwnerID != owner.ID {
		t.Fatalf("template owner = %q, want %q", template.OwnerID, owner.ID)
	}
	token, err := db.CreateEnrollmentToken(ownerCtx, core.EnrollmentTokenRequest{Name: "purge-token", TTLMinutes: 30, MaxUses: 1})
	if err != nil {
		t.Fatalf("create owned enrollment token: %v", err)
	}
	task, err := db.CreateTask(ownerCtx, core.TaskRequest{AgentID: node.ID, Engine: core.EngineMihomo, Action: core.ActionStop})
	if err != nil {
		t.Fatalf("create owned task: %v", err)
	}
	panelSettings, err := db.PanelSettings(ownerCtx)
	if err != nil {
		t.Fatal(err)
	}
	panelSettings.PanelName = "purge-panel"
	if _, err := db.SavePanelSettings(ownerCtx, panelSettings); err != nil {
		t.Fatalf("save owned panel settings: %v", err)
	}
	// A grant from the account own node to another account, plus the accounting
	// and task records that must be detached before the share goes.
	access, err := db.SetUserAgentAccess(admin, recipient.ID, core.AgentAccessRequest{Isolated: true,
		Shares: []core.AgentShareRequest{{AgentID: node.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21081}}}})
	if err != nil {
		t.Fatalf("share owned node: %v", err)
	}
	access = acceptSharedTestInvitations(t, db, ctx, recipient.ID, access)
	if len(access.Shares) != 1 || access.Shares[0].Status != core.AgentShareAccepted {
		t.Fatalf("share was not accepted: %+v", access.Shares)
	}
	shareID := access.Shares[0].ID
	if _, err := db.pool.Exec(ctx, "INSERT INTO port_traffic_policies (id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,share_id,created_at,updated_at) VALUES ('ptp_purge_test',$1,'purge quota','mihomo',21081,'tcp','monthly',CURRENT_DATE,0,$2,now(),now())", node.ID, shareID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, "UPDATE tasks SET shared_traffic_id=$2 WHERE id=$1", task.ID, shareID); err != nil {
		t.Fatal(err)
	}

	purged, err := db.PurgeUser(admin, owner.ID)
	if err != nil {
		t.Fatalf("purge user: %v", err)
	}
	if purged.ID != owner.ID || purged.Username != owner.Username {
		t.Fatalf("purged user = %+v, want %+v", purged, owner)
	}
	if _, err := db.UserForSession(ctx, owner.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purged account still resolves a session: %v", err)
	}

	agent, err := db.GetAgent(admin, node.ID)
	if err != nil {
		t.Fatalf("the node did not survive the purge: %v", err)
	}
	if agent.OwnerID != "" {
		t.Fatalf("node owner = %q, want the administrator scope", agent.OwnerID)
	}
	visible, err := db.AgentConfig(admin, node.ID, core.EngineMihomo)
	if err != nil || visible.ID != config.ID || visible.OwnerID != "" {
		t.Fatalf("configuration did not join the administrator scope: %+v %v", visible, err)
	}
	templates, err := db.ListConfigTemplates(admin)
	if err != nil {
		t.Fatal(err)
	}
	if !templateInScope(templates, template.ID) {
		t.Fatalf("template did not join the administrator scope: %+v", templates)
	}
	var tokenOwner, taskOwner string
	if err := db.pool.QueryRow(ctx, "SELECT owner_id FROM enrollment_tokens WHERE id=$1", token.ID).Scan(&tokenOwner); err != nil {
		t.Fatal(err)
	}
	if tokenOwner != "" {
		t.Fatalf("enrollment token owner = %q, want the administrator scope", tokenOwner)
	}
	if err := db.pool.QueryRow(ctx, "SELECT owner_id FROM tasks WHERE id=$1", task.ID).Scan(&taskOwner); err != nil {
		t.Fatal(err)
	}
	if taskOwner != "" {
		t.Fatalf("task owner = %q, want the administrator scope", taskOwner)
	}
	engineRows, err := db.pool.Query(ctx, "SELECT engine,owner_id FROM agent_engine_ownership WHERE agent_id=$1 ORDER BY engine", node.ID)
	if err != nil {
		t.Fatal(err)
	}
	var engineOwners []string
	for engineRows.Next() {
		var engine, engineOwner string
		if err := engineRows.Scan(&engine, &engineOwner); err != nil {
			engineRows.Close()
			t.Fatal(err)
		}
		engineOwners = append(engineOwners, engine+"="+engineOwner)
	}
	engineRows.Close()
	if err := engineRows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(engineOwners) == 0 {
		t.Fatal("the node lost its engine ownership rows during the purge")
	}
	for _, entry := range engineOwners {
		if !hasEmptyOwner(entry) {
			t.Fatalf("engine ownership did not join the administrator scope: %v", engineOwners)
		}
	}

	// Grants, quota links and personal data leave with the account.
	var shares, settingsRows int
	if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM agent_shares WHERE user_id=$1 OR agent_id=$2", owner.ID, node.ID).Scan(&shares); err != nil {
		t.Fatal(err)
	}
	if shares != 0 {
		t.Fatalf("agent_shares left after purge: %d", shares)
	}
	if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM user_panel_settings WHERE owner_id=$1", owner.ID).Scan(&settingsRows); err != nil {
		t.Fatal(err)
	}
	if settingsRows != 0 {
		t.Fatalf("personal settings left after purge: %d", settingsRows)
	}
	var policyShareID *string
	if err := db.pool.QueryRow(ctx, "SELECT share_id FROM port_traffic_policies WHERE id='ptp_purge_test'").Scan(&policyShareID); err != nil {
		t.Fatal(err)
	}
	if policyShareID != nil {
		t.Fatalf("quota still references the deleted grant: %q", *policyShareID)
	}
	var sharedTrafficID string
	if err := db.pool.QueryRow(ctx, "SELECT shared_traffic_id FROM tasks WHERE id=$1", task.ID).Scan(&sharedTrafficID); err != nil {
		t.Fatal(err)
	}
	if sharedTrafficID != "" {
		t.Fatalf("task still references the deleted grant: %q", sharedTrafficID)
	}
	if err := db.CheckAgentEngineAccess(recipientCtx, node.ID, core.EngineMihomo); err == nil {
		t.Fatal("the revoked recipient still reaches the node engine")
	}
}

// TestPurgeUserRefusesNonAdministratorsAndAdministratorAccounts keeps the two
// safety valves: only an administrator may purge, and an administrator account
// can only be disabled, never deleted.
func TestPurgeUserRefusesNonAdministratorsAndAdministratorAccounts(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	admin := WithConfigScope(ctx, "", true)
	user, userCtx := sharedTestUser(t, db, ctx, "purge-guard")
	if _, err := db.PurgeUser(userCtx, user.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-administrator purge error = %v, want ErrForbidden", err)
	}
	if _, err := db.UserForSession(ctx, user.ID); err != nil {
		t.Fatalf("refused purge removed the account: %v", err)
	}
	adminUser, err := db.CreateUser(ctx, core.UserRequest{Username: "purge-admin-guard", Role: core.RoleAdmin}, "test-only-hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.PurgeUser(admin, adminUser.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("administrator purge error = %v, want ErrConflict", err)
	}
	if _, err := db.UserForSession(ctx, adminUser.ID); err != nil {
		t.Fatalf("administrator account was removed: %v", err)
	}
}

// hasEmptyOwner reports whether a rendered engine ownership row has joined the
// administrator scope.
func hasEmptyOwner(entry string) bool {
	return len(entry) > 0 && entry[len(entry)-1] == '='
}

func templateInScope(templates []core.ConfigTemplate, id string) bool {
	for _, template := range templates {
		if template.ID == id {
			return template.OwnerID == ""
		}
	}
	return false
}
