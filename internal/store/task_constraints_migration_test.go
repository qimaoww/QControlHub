package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// This is the unnamed, two-column inline rule shipped before schema v41.
// Let PostgreSQL generate its name, just as CREATE TABLE did historically.
const legacyTaskEngineCheck = `ALTER TABLE tasks ADD CHECK
 (engine IN ('mihomo','xray','sing-box','ss-rust') OR (action='upgrade-agent' AND engine=''))`

func restoreTaskConstraintVersion(t *testing.T, db *Store, ctx context.Context, version int, legacy, modern bool) {
	t.Helper()
	if _, err := db.pool.Exec(ctx, `ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_check;
 ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_engine_check;
 DELETE FROM qcontrolhub_schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if legacy {
		if _, err := db.pool.Exec(ctx, legacyTaskEngineCheck); err != nil {
			t.Fatal(err)
		}
	}
	if modern {
		if _, err := db.pool.Exec(ctx, `ALTER TABLE tasks ADD CONSTRAINT tasks_engine_check CHECK
   (engine IN ('mihomo','xray','sing-box','ss-rust') OR (action IN ('upgrade-agent','enable-bbr','disable-bbr','configure-tcp','ip-quality') AND engine=''))`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO qcontrolhub_schema_migrations(version) VALUES($1)`, version); err != nil {
		t.Fatal(err)
	}
}

func taskConstraintSet(t *testing.T, db *Store, ctx context.Context) []string {
	t.Helper()
	rows, err := db.pool.Query(ctx, `SELECT conname,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='tasks'::regclass AND contype='c' ORDER BY conname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
		t.Logf("%s: %s", name, definition)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

func assertTaskConstraintSet(t *testing.T, db *Store, ctx context.Context) {
	t.Helper()
	want := []string{"tasks_action_check", "tasks_engine_check", "tasks_install_if_missing_check", "tasks_status_check"}
	if got := taskConstraintSet(t, db, ctx); !reflect.DeepEqual(got, want) {
		t.Fatalf("task constraints=%v, want %v", got, want)
	}
}

func migrationBusinessSnapshot(t *testing.T, db *Store, ctx context.Context) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, table := range []string{"agents", "agent_live_state", "enrollment_tokens", "panel_users", "panel_settings", "configs", "config_revisions", "tasks", "ip_quality_reports", "ip_quality_archives", "ip_quality_schedules", "agent_shares", "agent_share_ports"} {
		var rows string
		if err := db.pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(row_data ORDER BY row_data::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) row_data FROM `+table+` t) snapshot`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		result[table] = rows
	}
	return result
}

func seedTaskMigrationBusiness(t *testing.T, db *Store, ctx context.Context) string {
	t.Helper()
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	qualityHeartbeat(t, db, ctx, agent.ID)
	if _, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Name: "preserved", Engine: core.EngineMihomo, Content: "mixed-port: 21001\n"}, 0); err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(ctx, core.UserRequest{Username: "migration-recipient", Role: core.RoleUser, Permissions: core.GrantablePermissions()}, "test-only-hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO agent_shares(id,user_id,agent_id,enabled,status,engines) VALUES('shr_migration',$1,$2,true,'accepted',ARRAY['mihomo']);
 `, user.ID, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO agent_share_ports(agent_id,port,share_id) VALUES($1,21002,'shr_migration')`, agent.ID); err != nil {
		t.Fatal(err)
	}
	task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v %v", claimed, err)
	}
	report := storeQualityResult(t)
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, IPQuality: report}, storeQualityArchives(t, report)...); err != nil {
		t.Fatal(err)
	}
	// The historical DB rule allows a supported engine for every action. Keep
	// an archived row compatible with that rule while testing empty-engine inserts.
	if _, err := db.pool.Exec(ctx, `UPDATE tasks SET engine='mihomo' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetIPQualitySchedule(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	return agent.ID
}

func TestTaskConstraintsUpgradeResidualAndRepairedDatabases(t *testing.T) {
	for _, test := range []struct {
		name           string
		version        int
		legacy, modern bool
	}{
		{"v69_residual", 69, true, true}, {"v73_residual", 73, true, true},
		{"v73_manual_repair", 73, false, true}, {"v73_only_legacy", 73, true, false},
		{"v73_missing_engine_checks", 73, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, ctx, url := isolatedConfigScopeStore(t)
			agentID := seedTaskMigrationBusiness(t, db, ctx)
			restoreTaskConstraintVersion(t, db, ctx, test.version, test.legacy, test.modern)
			taskConstraintSet(t, db, ctx)
			input := core.TaskRequest{AgentID: agentID, Action: core.ActionIPQuality}
			if test.legacy {
				_, err := db.CreateTask(ctx, input)
				var pgError *pgconn.PgError
				var schemaError *TaskSchemaError
				if !errors.As(err, &pgError) || pgError.Code != "23514" || pgError.ConstraintName != "tasks_check" || !errors.As(err, &schemaError) {
					t.Fatalf("fixture did not reproduce tasks_check/23514: %v", err)
				}
			}
			before := migrationBusinessSnapshot(t, db, ctx)
			upgraded, err := OpenWithConfigKey(ctx, url, true, testEncryptionKey("config-scope"))
			if err != nil {
				t.Fatal(err)
			}
			upgraded.Close()
			assertTaskConstraintSet(t, db, ctx)
			if after := migrationBusinessSnapshot(t, db, ctx); !reflect.DeepEqual(before, after) {
				t.Fatalf("migration changed business data")
			}
			for range 2 {
				if err := db.migrate(ctx); err != nil {
					t.Fatal(err)
				}
				assertTaskConstraintSet(t, db, ctx)
				if after := migrationBusinessSnapshot(t, db, ctx); !reflect.DeepEqual(before, after) {
					t.Fatal("repeated migration changed data")
				}
			}
			if err := db.QueueDueIPQualityChecks(ctx, time.Now().UTC()); err != nil {
				t.Fatalf("schedule after upgrade: %v", err)
			}
			scheduled, err := db.CreateTask(ctx, input)
			if err != nil || !scheduled.Reused {
				t.Fatalf("manual request did not reuse scheduled check: %+v %v", scheduled, err)
			}
			claimed, err := db.ClaimTask(ctx, agentID)
			if err != nil || claimed == nil || claimed.ID != scheduled.ID || claimed.Engine != "" {
				t.Fatalf("claim after upgrade: %+v %v", claimed, err)
			}
			report := storeQualityResult(t)
			if err := db.CompleteTask(ctx, agentID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, IPQuality: report}, storeQualityArchives(t, report)...); err != nil {
				t.Fatal(err)
			}
			history, err := db.ListIPQualityRecords(ctx, "", "UTC")
			if err != nil || len(history) != 1 || history[0].TaskID != claimed.ID || history[0].Status != core.TaskSucceeded || len(history[0].Archives) != 1 || history[0].Result == nil {
				t.Fatalf("upgraded report history: %+v %v", history, err)
			}
		})
	}
}

func TestTaskConstraintsFreshAndRepeatedSchema(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	assertTaskConstraintSet(t, db, ctx)
	// Future schema bumps rerun this bootstrap SQL too.
	if _, err := db.pool.Exec(ctx, schemaSQL); err != nil {
		t.Fatal(err)
	}
	assertTaskConstraintSet(t, db, ctx)
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertTaskConstraintSet(t, db, ctx)
}

func TestTaskConstraintsMigrationRollbackAndRetry(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	seedTaskMigrationBusiness(t, db, ctx)
	restoreTaskConstraintVersion(t, db, ctx, 73, true, true)
	before := migrationBusinessSnapshot(t, db, ctx)
	if _, err := db.pool.Exec(ctx, `CREATE FUNCTION reject_v74() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected ledger failure'; END $$;
 CREATE TRIGGER reject_v74 BEFORE INSERT ON qcontrolhub_schema_migrations FOR EACH ROW EXECUTE FUNCTION reject_v74();
 ALTER TABLE tasks ADD CONSTRAINT tasks_custom_guard CHECK(attempt>=0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err == nil || !strings.Contains(err.Error(), "record schema migration version") {
		t.Fatalf("missing failed migration step: %v", err)
	}
	var version int
	if err := db.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil || version != 73 {
		t.Fatalf("ledger=%d %v", version, err)
	}
	names := taskConstraintSet(t, db, ctx)
	if !strings.Contains(strings.Join(names, ","), "tasks_check") {
		t.Fatal("DDL was not rolled back")
	}
	if after := migrationBusinessSnapshot(t, db, ctx); !reflect.DeepEqual(before, after) {
		t.Fatal("failed migration changed business rows")
	}
	if _, err := db.pool.Exec(ctx, `DROP TRIGGER reject_v74 ON qcontrolhub_schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	names = taskConstraintSet(t, db, ctx)
	want := []string{"tasks_action_check", "tasks_custom_guard", "tasks_engine_check", "tasks_install_if_missing_check", "tasks_status_check"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("constraints after retry = %v, want %v", names, want)
	}
	if after := migrationBusinessSnapshot(t, db, ctx); !reflect.DeepEqual(before, after) {
		t.Fatal("retry changed business rows")
	}
}

func TestTaskConstraintsConcurrentMigrationSerializes(t *testing.T) {
	db, ctx, url := isolatedConfigScopeStore(t)
	restoreTaskConstraintVersion(t, db, ctx, 73, true, true)
	blocker, err := db.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	const key = int64(0x52464f524745)
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatal(err)
	}
	defer blocker.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			opened, err := Open(ctx, url, true)
			if err == nil {
				opened.Close()
			}
			results <- err
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	waiting := 0
	for time.Now().Before(deadline) {
		if err := blocker.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND classid=$1::oid AND objid=$2::oid AND NOT granted`, key>>32, key&0xffffffff).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
	if waiting < 2 {
		t.Fatalf("did not observe both migration sessions waiting on advisory lock: %d", waiting)
	}
	assertTaskConstraintSet(t, db, ctx)
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM qcontrolhub_schema_migrations WHERE version=74`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("v74 entries=%d %v", count, err)
	}
}

func TestTaskConstraintsNodeTaskLifecycleAndRejections(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: []string{core.AgentFeatureIPQuality, core.AgentFeatureSelfUpgrade, core.AgentFeatureSystemBBR}}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []core.Action{core.ActionUpgradeAgent, core.ActionEnableBBR, core.ActionDisableBBR, core.ActionConfigureTCP, core.ActionIPQuality} {
		t.Run(string(action), func(t *testing.T) {
			input := core.TaskRequest{AgentID: agent.ID, Action: action}
			if action == core.ActionConfigureTCP {
				input.TCPSettings = core.TCPSettings{"net.ipv4.tcp_ecn": "1"}
			}
			task, err := db.CreateTask(ctx, input)
			if err != nil || task.Engine != "" {
				t.Fatalf("create=%+v %v", task, err)
			}
			claimed, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || claimed == nil || claimed.ID != task.ID || claimed.Engine != "" {
				t.Fatalf("claim=%+v %v", claimed, err)
			}
			result := core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}
			var archives []core.IPQualityArchive
			if action == core.ActionIPQuality {
				result.IPQuality = storeQualityResult(t)
				archives = storeQualityArchives(t, result.IPQuality)
			}
			if err := db.CompleteTask(ctx, agent.ID, task.ID, result, archives...); err != nil {
				t.Fatal(err)
			}
			completed, err := db.GetTask(ctx, task.ID)
			if err != nil || completed.Status != core.TaskSucceeded {
				t.Fatalf("completed=%+v %v", completed, err)
			}
		})
	}
	for _, test := range []struct{ name, action, engine, extra, constraint string }{
		{"kernel_requires_engine", "status", "", "", "tasks_engine_check"},
		{"unknown_action", "unknown", "mihomo", "", "tasks_action_check"},
		{"unknown_engine", "status", "unknown", "", "tasks_engine_check"},
		{"invalid_status", "status", "mihomo", ",status='unknown'", "tasks_status_check"},
		{"automatic_install_action", "status", "mihomo", ",install_if_missing=true", "tasks_install_if_missing_check"},
		{"automatic_install_revision", "deploy", "mihomo", ",install_if_missing=true,config_version=0", "tasks_install_if_missing_check"},
		{"automatic_install_missing_config", "validate", "mihomo", ",install_if_missing=true,config_version=1", "tasks_install_if_missing_check"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Each negative insertion has its own transaction: 23514 aborts only this one.
			tx, err := db.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			_, err = tx.Exec(ctx, `INSERT INTO tasks(id,agent_id,action,engine,status,created_at) VALUES('invalid',$1,$2,$3,'pending',now())`, agent.ID, test.action, test.engine)
			if test.extra != "" {
				if err != nil {
					t.Fatal(err)
				}
				_, err = tx.Exec(ctx, `UPDATE tasks SET attempt=0`+test.extra+` WHERE id='invalid'`)
			}
			var pgError *pgconn.PgError
			if !errors.As(err, &pgError) || pgError.Code != "23514" || pgError.ConstraintName != test.constraint {
				t.Fatalf("expected %s/23514, got %v", test.constraint, err)
			}
		})
	}
	for _, input := range []core.TaskRequest{
		{AgentID: agent.ID, Action: core.ActionStatus},
		{AgentID: agent.ID, Action: "unknown", Engine: core.EngineMihomo},
		{AgentID: agent.ID, Action: core.ActionStatus, Engine: "unknown"},
		{AgentID: agent.ID, Action: core.ActionIPQuality, Engine: core.EngineMihomo},
		{AgentID: agent.ID, Action: core.ActionStatus, Engine: core.EngineMihomo, ExpectedConfigVersion: -1},
		{AgentID: agent.ID, Action: core.ActionStatus, Engine: core.EngineMihomo, InstallIfMissing: true},
	} {
		if _, err := db.CreateTask(ctx, input); !errors.Is(err, ErrInvalid) {
			t.Errorf("API/store input %+v: %v", input, err)
		}
	}
	// Every currently supported action is accepted by the actual action CHECK.
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy, core.ActionImportExisting, core.ActionReadConfig, core.ActionReadManagedConfig, core.ActionStart, core.ActionStop, core.ActionRestart, core.ActionStatus, core.ActionInstall, core.ActionUninstall} {
		tx, err := db.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO tasks(id,agent_id,action,engine,status,created_at) VALUES($1,$2,$3,'mihomo','pending',now())`, fmt.Sprint(action), agent.ID, action)
		_ = tx.Rollback(ctx)
		if err != nil {
			t.Errorf("supported action %s: %v", action, err)
		}
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		tx, err := db.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO tasks(id,agent_id,action,engine,status,created_at) VALUES('supported-engine',$1,'status',$2,'pending',now())`, agent.ID, engine)
		_ = tx.Rollback(ctx)
		if err != nil {
			t.Errorf("supported engine %s: %v", engine, err)
		}
	}
	config, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Name: "install snapshot", Engine: core.EngineMihomo, Content: "mixed-port: 21001\n"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		tx, err := db.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO tasks(id,agent_id,action,engine,status,created_at,config_id,config_version,install_if_missing)
			VALUES('supported-install',$1,$2,'mihomo','pending',now(),$3,$4,true)`, agent.ID, action, config.ID, config.Version)
		_ = tx.Rollback(ctx)
		if err != nil {
			t.Errorf("valid automatic installation %s: %v", action, err)
		}
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE configs SET version=0 WHERE id=$1`, config.ID)
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23514" || pgError.ConstraintName != "configs_version_check" {
		t.Fatalf("configuration version constraint was weakened: %v", err)
	}
}
