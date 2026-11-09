package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Dense main history, empty legacy aliases and sparse shares of the SAME
// engine exposed repeated history scans. Always exercise the authenticated
// query; the unscoped fleet query has a different plan and hid this regression.
func TestCoreLogAuthorizedSparseFleetWindow(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	owner, _ := sharedTestUser(t, s, ctx, "fleet-owner")
	recipient, recipientScope := sharedTestUser(t, s, ctx, "fleet-recipient")
	scope := WithConfigScope(ctx, owner.ID, true)
	engines := []string{"mihomo", "xray", "sing-box", "ss-rust"}
	now := time.Now().UTC()
	if err := s.ensureCoreLogPartitionsFrom(ctx, now.Truncate(24*time.Hour).AddDate(0, 0, -13)); err != nil {
		t.Fatal(err)
	}
	rowsPerNode := 2048
	measure := os.Getenv("QCH_CORE_LOG_MEASURE") == "1"
	if measure {
		rowsPerNode = 20000
		if raw := os.Getenv("QCH_CORE_LOG_ROWS"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 2048 {
				t.Fatal("invalid measurement row count")
			}
			rowsPerNode = value
		}
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	for i := 0; i < 15; i++ {
		id, _ := seedPerformanceAgent(t, s, 0)
		ids = append(ids, id)
		exec(`UPDATE agents SET owner_id=$2 WHERE id=$1`, id, owner.ID)
		if i < 5 {
			share := fmt.Sprintf("shr_%016x", i+1)
			exec(`WITH sh AS (INSERT INTO agent_shares(id,agent_id,user_id,status,engines) VALUES($1,$2,$3,'accepted',ARRAY['mihomo']) RETURNING id)
    INSERT INTO agent_shared_instance_ownership(agent_id,engine,share_id,owner_id,config_id,config_version,updated_at)
    SELECT $2,'mihomo',id,$3,'fixture',1,now() FROM sh`, share, id, recipient.ID)
		}
		batch := fmt.Sprintf("log_%016x", 9000+i)
		exec(`INSERT INTO core_log_batches(id,agent_id,received_at) VALUES($1,$2,$3)`, batch, id, now)
		exec(`INSERT INTO core_logs(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at,instance_id,node_owner_id,owner_id)
   SELECT $1,i%32000,$2,CASE WHEN i%40=0 THEN 'xray' ELSE 'mihomo' END,'info',repeat('history ',8),$3::timestamptz,$3::timestamptz-(i%14)*interval '1 day','main',$4,$4 FROM generate_series(1,$5::int) i`, batch, id, now, owner.ID, rowsPerNode)
		if i == 0 {
			exec(`INSERT INTO core_logs(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at,instance_id,node_owner_id,owner_id)
    SELECT $1,32000+i,$2,'mihomo','info','shared historical line',$3::timestamptz,$3::timestamptz-interval '13 days','shr_0000000000000001',$4,$5 FROM generate_series(1,13) i`, batch, id, now, owner.ID, recipient.ID)
		}
	}
	// Collection status cannot discard authorized history.
	exec(`UPDATE agents SET last_seen=now()-interval '2 days',runtime='{"mihomo":{"installed":false}}' WHERE id=$1`, ids[14])
	for _, analyzed := range []bool{false, true} {
		if analyzed {
			exec(`VACUUM (ANALYZE) core_logs`)
		}
		for _, q := range []CoreLogQuery{{Limit: 100}, {Limit: 200}, {Limit: 500}, {Limit: 1000}, {Limit: 2000}, {AgentID: ids[14], Limit: 1000}, {AgentID: ids[0], InstanceID: "main", Limit: 1000}, {Engine: core.EngineXray, Limit: 1000}, {InstanceID: "shr_0000000000000001", Limit: 1000}} {
			selected := engines
			if q.Engine != "" {
				selected = []string{string(q.Engine)}
			}
			started := time.Now()
			poolBefore := s.pool.Stat().AcquireDuration()
			entries, err := s.ListCoreLogs(scope, q)
			elapsed := time.Since(started)
			poolWait := s.pool.Stat().AcquireDuration() - poolBefore
			if err != nil {
				t.Fatal(err)
			}
			counts := map[core.Engine]int{}
			for i, entry := range entries {
				counts[entry.Engine]++
				if i > 0 && entries[i-1].ID <= entry.ID {
					t.Fatal("window is not newest first")
				}
				if entry.OwnerID != owner.ID && entry.OwnerID != recipient.ID {
					t.Fatal("foreign history")
				}
			}
			wantM, wantX := q.Limit, q.Limit
			if q.AgentID != "" {
				wantX = min(q.Limit, rowsPerNode/40)
			} else {
				wantX = min(q.Limit, 15*(rowsPerNode/40))
			}
			if q.Engine == core.EngineXray {
				wantM = 0
			}
			if q.InstanceID != "" && q.InstanceID != "main" {
				wantM = 13
				wantX = 0
			}
			if counts[core.EngineMihomo] != wantM || counts[core.EngineXray] != wantX || len(entries) != wantM+wantX {
				t.Fatalf("q=%+v counts=%v want=%d/%d", q, counts, wantM, wantX)
			}
			if measure {
				t.Logf("analyzed=%t q=%+v rows=%d read_ms=%.2f pool_wait_ms=%.3f", analyzed, q, len(entries), float64(elapsed.Microseconds())/1000, float64(poolWait.Microseconds())/1000)
			}
			if q.AgentID == "" && q.Engine == "" && q.InstanceID == "" && (q.Limit == 100 || q.Limit == 1000) {
				sql, args := scopedCoreLogWindowStatement(scope, q, selected)
				var raw []byte
				if err := s.pool.QueryRow(ctx, "EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) "+sql, args...).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var plans []struct {
					Plan          coreLogWindowPlan
					ExecutionTime float64 `json:"Execution Time"`
				}
				if err := json.Unmarshal(raw, &plans); err != nil {
					t.Fatal(err)
				}
				scanned, filtered := plans[0].Plan.work()
				// Main data matches all predicates. Empty identities must seek their own
				// range, instead of filtering the dense main history once per alias/share.
				if filtered > float64(rowsPerNode*15)/10 {
					t.Fatalf("empty identities scanned history: filtered=%.0f scanned=%.0f", filtered, scanned)
				}
				t.Logf("analyzed=%t limit=%d sql_ms=%.2f scanned=%.0f filtered=%.0f", analyzed, q.Limit, plans[0].ExecutionTime, scanned, filtered)
				if measure {
					compact, _ := json.Marshal(json.RawMessage(raw))
					t.Logf("plan=%s", compact)
				}
			}
		}
	}
	shared, err := s.ListCoreLogs(recipientScope, CoreLogQuery{Limit: 2000})
	if err != nil || len(shared) != 13 {
		t.Fatalf("recipient history=%d: %v", len(shared), err)
	}
	for _, entry := range shared {
		if entry.OwnerID != recipient.ID || entry.InstanceID != "shr_0000000000000001" {
			t.Fatal("recipient escaped its instance")
		}
	}
}

type coreLogWindowPlan struct {
	NodeType string              `json:"Node Type"`
	Relation string              `json:"Relation Name"`
	Rows     float64             `json:"Actual Rows"`
	Loops    float64             `json:"Actual Loops"`
	Filtered float64             `json:"Rows Removed by Filter"`
	Plans    []coreLogWindowPlan `json:"Plans"`
}

func (p coreLogWindowPlan) work() (rows, filtered float64) {
	if strings.Contains(p.NodeType, "Scan") && strings.HasPrefix(p.Relation, "core_logs") {
		rows = p.Rows * p.Loops
		filtered = p.Filtered * p.Loops
	}
	for _, child := range p.Plans {
		r, f := child.work()
		rows += r
		filtered += f
	}
	return
}
