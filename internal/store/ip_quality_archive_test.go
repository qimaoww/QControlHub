package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestIPQualityArchiveOwnershipAndRetention(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	qualityHeartbeat(t, db, ctx, agent.ID)
	task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil {
		t.Fatalf("claim: %+v %v", lease, err)
	}
	if allowed, err := db.CanArchiveIPQualityResult(ctx, agent.ID, task.ID, "wrong-lease"); err != nil || allowed {
		t.Fatalf("stale lease authorized: %v %v", allowed, err)
	}
	if allowed, err := db.CanArchiveIPQualityResult(ctx, agent.ID, task.ID, lease.LeaseID); err != nil || !allowed {
		t.Fatalf("live lease rejected: %v %v", allowed, err)
	}
	report := storeQualityResult(t)
	content := []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
	digest := sha256.Sum256(content)
	archive := core.IPQualityArchive{IPQualityArchiveInfo: core.IPQualityArchiveInfo{
		Family: 4, SHA256: hex.EncodeToString(digest[:]), Size: len(content), RenderedAt: time.Now().UTC(),
	}, Content: content}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, IPQuality: report}, archive); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetIPQualityArchive(ctx, task.ID, 4)
	if err != nil || string(got.Content) != string(content) || got.SHA256 != archive.SHA256 {
		t.Fatalf("archive: %+v %v", got, err)
	}
	if _, err := db.GetIPQualityArchive(WithConfigScope(ctx, "another-owner", false), task.ID, 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archive leaked to another owner: %v", err)
	}
	if allowed, err := db.CanArchiveIPQualityResult(ctx, agent.ID, task.ID, lease.LeaseID); err != nil || allowed {
		t.Fatalf("completed task authorized a new render: %v %v", allowed, err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET revoked_at=now() WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetIPQualityArchive(ctx, task.ID, 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked node archive remained visible: %v", err)
	}
	if _, err := db.pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM ip_quality_archives WHERE task_id=$1`, task.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("task deletion left SVG bytes: %d %v", count, err)
	}
}

func TestIPQualityArchiveMigrationFrom62(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	if _, err := db.pool.Exec(ctx, `DROP TABLE ip_quality_archives;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=63;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(62) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := db.pool.QueryRow(ctx, `SELECT to_regclass('ip_quality_archives') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("schema 62 did not gain SVG archives: %v", err)
	}
}

// v65 replaced the downloaded upstream SVG with a panel-rendered one: the
// report link is gone and the timestamp is a render time.
func TestIPQualityArchiveRenderMigrationFrom64(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	if _, err := db.pool.Exec(ctx, `DROP TABLE ip_quality_archives;
		CREATE TABLE ip_quality_archives (
			task_id text NOT NULL,
			family integer NOT NULL,
			source_url text NOT NULL,
			sha256 text NOT NULL,
			downloaded_at timestamptz NOT NULL,
			content bytea NOT NULL,
			PRIMARY KEY(task_id,family));
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=65;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(64) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var rendered, source bool
	if err := db.pool.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='ip_quality_archives' AND column_name='rendered_at'),
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='ip_quality_archives' AND column_name='source_url')`).Scan(&rendered, &source); err != nil {
		t.Fatal(err)
	}
	if !rendered || source {
		t.Fatalf("v65 migration left rendered_at=%v source_url=%v", rendered, source)
	}
}
