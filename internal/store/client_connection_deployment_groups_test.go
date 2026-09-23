package store

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionGroupedDeploymentBoundaries(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET capabilities='["xray"]',supported_capabilities='["xray"]' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	config, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "listener", Content: `{"inbounds":[{"tag":"entry","protocol":"vless","port":8443}]}`}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The failed task wins the equal-time tie, canceled tasks are ignored, and
	// running/pending tasks cannot supply a historical listener port.
	for i, task := range []struct {
		id, status    string
		start, finish any
	}{
		{"tsk_a", "succeeded", old, old},
		{"tsk_b", "succeeded", old.Add(10 * time.Minute), old.Add(10 * time.Minute)},
		{"tsk_c", "failed", old.Add(10 * time.Minute), old.Add(10 * time.Minute)},
		{"tsk_d", "succeeded", old.Add(20 * time.Minute), old.Add(21 * time.Minute)},
		{"tsk_e", "canceled", old.Add(30 * time.Minute), old.Add(30 * time.Minute)},
		{"tsk_f", "running", old.Add(40 * time.Minute), nil},
		{"tsk_g", "pending", nil, nil},
	} {
		if _, err := db.pool.Exec(ctx, `INSERT INTO tasks(id,agent_id,action,engine,config_id,config_version,status,created_at,started_at,finished_at)
 VALUES($1,$2,'deploy','xray',$3,1,$4,$5,$6,$7)`, task.id, agent.ID, config.ID, task.status, old.Add(time.Duration(i)*time.Minute), task.start, task.finish); err != nil {
			t.Fatal(err)
		}
	}
	entries := []core.CoreLogEntry{}
	for i, offset := range []time.Duration{-time.Minute, time.Minute, 10 * time.Minute, 20*time.Minute + time.Second, 22 * time.Minute, 31 * time.Minute, 41 * time.Minute} {
		entries = append(entries, core.CoreLogEntry{Engine: core.EngineXray, Level: "info", LoggedAt: old.Add(offset), Message: fmt.Sprintf("from 8.8.8.8:%d accepted tcp:1.1.1.1:443 [entry -> direct]", 50123+i)})
	}
	if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: "log_1123456789abcdef", Entries: entries}); err != nil {
		t.Fatal(err)
	}
	q := ClientConnectionQuery{AgentID: agent.ID, Since: old.Add(-2 * time.Minute), Until: time.Now(), Limit: 100, Bucket: "hour"}
	raw, err := db.ClientConnectionHistory(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[int]bool{}
	for _, r := range raw.Records {
		expected[r.LocalPort] = true
	}
	if len(expected) != 2 || !expected[0] || !expected[8443] {
		t.Fatalf("raw ports=%v", expected)
	}
	q.GroupByIP = true
	grouped, err := db.ClientConnectionHistory(ctx, q)
	if err != nil || len(grouped.Records) != 1 {
		t.Fatalf("grouped=%+v err=%v", grouped, err)
	}
	actual := map[int]bool{}
	for _, endpoint := range grouped.Records[0].Endpoints {
		actual[endpoint.LocalPort] = true
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("grouped ports=%v raw ports=%v", actual, expected)
	}
}
