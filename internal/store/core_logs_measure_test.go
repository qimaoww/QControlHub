package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Opt-in measurements use the production SQL and an isolated PostgreSQL schema.
func TestCoreLogWindowMeasurements(t *testing.T) {
	if os.Getenv("QCH_CORE_LOG_MEASURE") != "1" || testing.Short() {
		t.Skip("performance fixture")
	}
	s := openPerformanceStore(t)
	ctx := context.Background()
	if os.Getenv("QCH_CORE_LOG_BASELINE") == "1" {
		if _, err := s.pool.Exec(ctx, `DROP INDEX core_logs_engine_covering_idx`); err != nil {
			t.Fatal(err)
		}
	}
	engines := []string{"mihomo", "xray", "sing-box", "ss-rust"}
	ids := []string{}
	now := time.Now().UTC()
	if err := s.ensureCoreLogPartitionsFrom(ctx, now.Truncate(24*time.Hour).AddDate(0, 0, -6)); err != nil {
		t.Fatal(err)
	}
	for node := 0; node < 4; node++ {
		id, _ := seedPerformanceAgent(t, s, 0)
		ids = append(ids, id)
		batch := fmt.Sprintf("log_%016x", node+8000)
		if _, err := s.pool.Exec(ctx, `INSERT INTO core_log_batches(id,agent_id,received_at) VALUES($1,$2,$3)`, batch, id, now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO core_logs(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at)
   SELECT $1,i%32000,$2,CASE WHEN $6::boolean THEN CASE WHEN i%40=0 THEN 'xray' ELSE 'mihomo' END ELSE ($4::text[])[(i%4)+1] END,'info',$5,$3::timestamptz,$3::timestamptz-(i%7)*interval '1 day' FROM generate_series(1,40000) AS i`, batch, id, now, engines, strings.Repeat("window measurement ", 50), os.Getenv("QCH_CORE_LOG_SKEW") == "1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.pool.Exec(ctx, `VACUUM (ANALYZE) core_logs`); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{ids[0], ""} {
		for _, limit := range []int{200, 1000, 2000} {
			q := CoreLogQuery{AgentID: agent, Limit: limit}
			statement, args := coreLogWindowStatement(q, engines)
			if os.Getenv("QCH_CORE_LOG_BASELINE") == "1" && agent == "" {
				statement = fmt.Sprintf(coreLogWindowSQL, "engine=selected.engine")
				args = []any{engines, limit}
			}
			var raw []byte
			if err := s.pool.QueryRow(ctx, "EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) "+statement, args...).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var plan []map[string]any
			if err := json.Unmarshal(raw, &plan); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			rows, err := s.pool.Query(ctx, statement, args...)
			if err != nil {
				t.Fatal(err)
			}
			entries := []core.CoreLogEntry{}
			for rows.Next() {
				var entry core.CoreLogEntry
				if err := rows.Scan(&entry.ID, &entry.AgentID, &entry.Engine, &entry.Level, &entry.Message, &entry.LoggedAt, &entry.ReceivedAt); err != nil {
					t.Fatal(err)
				}
				entry.Message = sanitizeCoreLogMessage(entry.Message)
				entries = append(entries, entry)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			read := time.Since(start)
			start = time.Now()
			body, err := json.Marshal(entries)
			if err != nil {
				t.Fatal(err)
			}
			encode := time.Since(start)
			scope := "node"
			if agent == "" {
				scope = "all"
			}
			t.Logf("scope=%s limit=%d rows=%d sql_ms=%v read_ms=%.2f json_ms=%.2f bytes=%d plan=%s", scope, limit, len(entries), plan[0]["Execution Time"], float64(read.Microseconds())/1000, float64(encode.Microseconds())/1000, len(body), raw)
		}
	}
}
