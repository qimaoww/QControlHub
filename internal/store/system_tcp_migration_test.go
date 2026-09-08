package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestOpenMigratesSystemTCPTasks(t *testing.T) {
	for _, version := range []int{40, 41} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			testOpenMigratesSystemTCPTasks(t, version)
		})
	}
}

func testOpenMigratesSystemTCPTasks(t *testing.T, version int) {
	t.Helper()
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(context.Background())
	db, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	connection, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	_, err = connection.Exec(ctx, `
		DELETE FROM qcontrolhub_schema_migrations;
		DROP INDEX tasks_system_tcp_recent_idx;
		ALTER TABLE tasks DROP COLUMN tcp_settings;
		ALTER TABLE tasks DROP CONSTRAINT tasks_action_check;
		ALTER TABLE tasks ADD CONSTRAINT tasks_action_check CHECK(action IN ('validate','deploy','import-existing','read-config','read-managed-config','start','stop','restart','status','install','upgrade-agent'));
		ALTER TABLE tasks DROP CONSTRAINT tasks_engine_check;
		ALTER TABLE tasks ADD CONSTRAINT tasks_engine_check CHECK(engine IN ('mihomo','xray','sing-box','ss-rust') OR (action='upgrade-agent' AND engine=''));
		INSERT INTO agents(id,name,version,os,arch,capabilities,features,public_key,last_seen,enrolled_at)
		VALUES('agt_tcp_migration','legacy','test','linux','amd64','["mihomo"]','["system-bbr-v1"]',decode(repeat('01',32),'hex'),now(),now());
		INSERT INTO tasks(id,agent_id,action,engine,status,created_at)
		VALUES('tsk_tcp_old','agt_tcp_migration','status','mihomo','succeeded',now());
	`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `INSERT INTO qcontrolhub_schema_migrations(version) VALUES($1)`, version); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var migratedVersion int
	var taskIndexExists bool
	if err := connection.QueryRow(ctx, `SELECT max(version), to_regclass('tasks_system_tcp_recent_idx') IS NOT NULL FROM qcontrolhub_schema_migrations`).Scan(&migratedVersion, &taskIndexExists); err != nil || migratedVersion != currentSchemaVersion || !taskIndexExists {
		t.Fatalf("incomplete schema migration: version=%d index=%v err=%v", migratedVersion, taskIndexExists, err)
	}
	old, err := db.GetTask(ctx, "tsk_tcp_old")
	if err != nil || old.Status != core.TaskSucceeded || len(old.TCPSettings) != 0 {
		t.Fatalf("legacy task lost: %+v %v", old, err)
	}
	for _, action := range []core.Action{core.ActionConfigureTCP, core.ActionEnableBBR, core.ActionDisableBBR} {
		request := core.TaskRequest{AgentID: "agt_tcp_migration", Action: action}
		if action == core.ActionConfigureTCP {
			request.TCPSettings = core.TCPSettings{"net.ipv4.tcp_ecn": "1"}
		}
		task, err := db.CreateTask(ctx, request)
		if err != nil {
			t.Fatalf("v%d migration rejects %s: %v", version, action, err)
		}
		if err := db.CancelTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
}
