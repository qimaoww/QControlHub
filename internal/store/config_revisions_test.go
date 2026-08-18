package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestConfigRevisionLifecycleWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()
	baselineOverview, err := dataStore.Overview(ctx)
	if err != nil {
		t.Fatalf("baseline overview: %v", err)
	}

	created, err := dataStore.CreateConfig(ctx, core.Config{
		Name: "revision integration", Engine: core.EngineMihomo,
		Content: "mixed-port: 7890\nmode: rule\nrules:\n  - MATCH,DIRECT\n",
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	defer cleanupRevisionTestConfig(dataStore, created.ID)
	withGlobalConfig, err := dataStore.Overview(ctx)
	if err != nil || withGlobalConfig.Configs != baselineOverview.Configs+1 || withGlobalConfig.NodeConfigs != baselineOverview.NodeConfigs {
		t.Fatalf("overview after global config = %+v, %v; baseline = %+v", withGlobalConfig, err, baselineOverview)
	}

	first, err := dataStore.ConfigRevision(ctx, created.ID, 1)
	if err != nil || first.Content != created.Content || first.Version != 1 {
		t.Fatalf("initial revision = %+v, %v", first, err)
	}
	updated, err := dataStore.UpdateConfig(ctx, created.ID, core.Config{
		Version: 1, Name: "revision integration v2", Engine: core.EngineMihomo,
		Content: "mixed-port: 7891\nmode: global\nrules:\n  - MATCH,DIRECT\n",
	})
	if err != nil || updated.Version != 2 {
		t.Fatalf("update config = %+v, %v", updated, err)
	}
	revisions, err := dataStore.ListConfigRevisions(ctx, created.ID, 20)
	if err != nil || len(revisions) != 2 || revisions[0].Version != 2 || revisions[1].Version != 1 {
		t.Fatalf("revisions after update = %+v, %v", revisions, err)
	}

	restored, err := dataStore.RestoreConfigRevision(ctx, created.ID, 1, 2)
	if err != nil || restored.Version != 3 || restored.Content != first.Content || restored.Name != first.Name {
		t.Fatalf("restore revision = %+v, %v", restored, err)
	}
	third, err := dataStore.ConfigRevision(ctx, created.ID, 3)
	if err != nil || third.Content != first.Content {
		t.Fatalf("restored revision was not recorded: %+v, %v", third, err)
	}
	if _, err := dataStore.RestoreConfigRevision(ctx, created.ID, 2, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale restore error = %v, want conflict", err)
	}

	if _, err := dataStore.pool.Exec(ctx, `UPDATE config_revisions SET content='invalid: [' WHERE config_id=$1 AND version=1`, created.ID); err != nil {
		t.Fatalf("corrupt revision fixture: %v", err)
	}
	if _, err := dataStore.RestoreConfigRevision(ctx, created.ID, 1, 3); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid revision restore error = %v, want invalid", err)
	}
	if _, err := dataStore.ConfigRevision(ctx, created.ID, 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed restore created revision 4: %v", err)
	}
	configs, err := dataStore.ListConfigs(ctx)
	if err != nil {
		t.Fatalf("list configs: %v", err)
	}
	assertConfigVersion(t, configs, created.ID, 3)

	missingID, err := core.NewID("cfg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.ListConfigRevisions(ctx, missingID, 20); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing revision list error = %v, want not found", err)
	}

	testAgentConfigRevisionLifecycle(t, ctx, dataStore)
	testConfigRevisionMigrationBackfill(t, ctx, databaseURL)
}

func testAgentConfigRevisionLifecycle(t *testing.T, ctx context.Context, dataStore *Store) {
	t.Helper()
	baselineOverview, err := dataStore.Overview(ctx)
	if err != nil {
		t.Fatalf("baseline agent config overview: %v", err)
	}
	enrollment, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{
		Name: "revision integration", TTLMinutes: 5, MaxUses: 1,
	})
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatal(err)
	}
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "revision-agent", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineXray},
		PublicKey:    base64.RawURLEncoding.EncodeToString(publicKey),
	}, enrollment.Token)
	if err != nil {
		t.Fatalf("enroll agent: %v", err)
	}
	defer cleanupRevisionTestAgent(dataStore, agent.ID, enrollment.ID)

	first, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "node xray v1", Engine: core.EngineXray,
		Content: `{"log":{"loglevel":"warning"},"inbounds":[],"outbounds":[]}`,
	}, 0)
	if err != nil {
		t.Fatalf("save first agent config: %v", err)
	}
	withAgentConfig, err := dataStore.Overview(ctx)
	if err != nil || withAgentConfig.Configs != baselineOverview.Configs || withAgentConfig.NodeConfigs != baselineOverview.NodeConfigs+1 {
		t.Fatalf("overview after node config = %+v, %v; baseline = %+v", withAgentConfig, err, baselineOverview)
	}
	second, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "node xray v2", Engine: core.EngineXray,
		Content: `{"log":{"loglevel":"info"},"inbounds":[],"outbounds":[]}`,
	}, 1)
	if err != nil || second.Version != 2 {
		t.Fatalf("save second agent config = %+v, %v", second, err)
	}
	restored, err := dataStore.RestoreConfigRevision(ctx, first.ID, 1, 2)
	if err != nil || restored.Version != 3 || restored.AgentID != agent.ID || restored.Content != first.Content {
		t.Fatalf("restore agent revision = %+v, %v", restored, err)
	}
	listed, err := dataStore.ListAgentConfigs(ctx)
	if err != nil {
		t.Fatalf("list agent configs: %v", err)
	}
	found := false
	for _, config := range listed {
		if config.ID == restored.ID {
			found = config.AgentID == agent.ID && config.Version == 3 && config.Content == restored.Content
			break
		}
	}
	if !found {
		t.Fatalf("restored node configuration is missing from ListAgentConfigs: %+v", listed)
	}
	if err := dataStore.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	afterDelete, err := dataStore.Overview(ctx)
	if err != nil || afterDelete.Configs != baselineOverview.Configs || afterDelete.NodeConfigs != baselineOverview.NodeConfigs {
		t.Fatalf("overview after deleting node config = %+v, %v; baseline = %+v", afterDelete, err, baselineOverview)
	}
	if _, err := dataStore.ConfigRevision(ctx, first.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted agent retained revisions: %v", err)
	}
}

func testConfigRevisionMigrationBackfill(t *testing.T, ctx context.Context, databaseURL string) {
	t.Helper()
	schemaID, err := core.NewID("migration")
	if err != nil {
		t.Fatal(err)
	}
	schemaName := pgx.Identifier{schemaID}.Sanitize()
	setup, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect for legacy migration fixture: %v", err)
	}
	defer setup.Close(ctx)
	if _, err := setup.Exec(ctx, `CREATE SCHEMA `+schemaName); err != nil {
		t.Fatalf("create legacy migration schema: %v", err)
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := setup.Exec(cleanupContext, `DROP SCHEMA `+schemaName+` CASCADE`); err != nil {
			t.Errorf("drop legacy migration schema: %v", err)
		}
	}()
	if _, err := setup.Exec(ctx, `SET search_path TO `+schemaName); err != nil {
		t.Fatalf("select legacy migration schema: %v", err)
	}
	if _, err := setup.Exec(ctx, `
		CREATE TABLE agents (
			id text PRIMARY KEY,
			name varchar(100) NOT NULL,
			version varchar(100) NOT NULL DEFAULT '',
			os varchar(50) NOT NULL,
			arch varchar(50) NOT NULL,
			capabilities jsonb NOT NULL,
			labels jsonb NOT NULL DEFAULT '{}'::jsonb,
			runtime jsonb NOT NULL DEFAULT '{}'::jsonb,
			public_key bytea NOT NULL CHECK (octet_length(public_key) = 32),
			last_seen timestamptz NOT NULL,
			enrolled_at timestamptz NOT NULL,
			revoked_at timestamptz
		);
		CREATE TABLE configs (
			id text PRIMARY KEY,
			name varchar(100) NOT NULL,
			description varchar(300) NOT NULL DEFAULT '',
			engine varchar(20) NOT NULL CHECK (engine IN ('mihomo','xray','sing-box','ss-rust')),
			content text NOT NULL CHECK (octet_length(content) <= 2097152),
			version integer NOT NULL CHECK (version > 0),
			created_at timestamptz NOT NULL,
			updated_at timestamptz NOT NULL,
			deleted_at timestamptz
		)`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	id, err := core.NewID("cfg")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = setup.Exec(ctx, `
		INSERT INTO configs (id,name,description,engine,content,version,created_at,updated_at)
		VALUES ($1,$2,'','sing-box',$3,7,$4,$4)`, id, "migration backfill",
		`{"log":{"level":"info"},"inbounds":[],"outbounds":[]}`, now)
	if err != nil {
		t.Fatalf("insert pre-migration config: %v", err)
	}
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse legacy migration database URL: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schemaID
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("open legacy migration pool: %v", err)
	}
	dataStore := &Store{pool: pool}
	defer dataStore.Close()
	if err := dataStore.Ping(ctx); err != nil {
		t.Fatalf("ping legacy migration pool: %v", err)
	}
	if err := dataStore.migrate(ctx); err != nil {
		t.Fatalf("rerun migration: %v", err)
	}
	revision, err := dataStore.ConfigRevision(ctx, id, 7)
	if err != nil || revision.Name != "migration backfill" || revision.Version != 7 {
		t.Fatalf("backfilled revision = %+v, %v", revision, err)
	}
}

func assertConfigVersion(t *testing.T, configs []core.Config, id string, version int) {
	t.Helper()
	for _, config := range configs {
		if config.ID == id {
			if config.Version != version {
				t.Fatalf("config %s version = %d, want %d", id, config.Version, version)
			}
			return
		}
	}
	t.Fatalf("config %s not found", id)
}

func cleanupRevisionTestConfig(dataStore *Store, configID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM config_revisions WHERE config_id=$1`, configID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM tasks WHERE config_id=$1`, configID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM configs WHERE id=$1`, configID)
}

func cleanupRevisionTestAgent(dataStore *Store, agentID, enrollmentID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM tasks WHERE agent_id=$1`, agentID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM config_revisions WHERE agent_id=$1`, agentID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM configs WHERE agent_id=$1`, agentID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM agent_nonces WHERE agent_id=$1`, agentID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM agents WHERE id=$1`, agentID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM enrollment_tokens WHERE id=$1`, enrollmentID)
}
