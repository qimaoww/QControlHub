package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionIPGroupingEngineNodeOrder(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	alpha, zulu := sharedTestAgent(t, db, ctx), sharedTestAgent(t, db, ctx)
	for name, id := range map[string]string{"Alpha": alpha.ID, "Zulu": zulu.ID} {
		if _, err := db.pool.Exec(ctx, `UPDATE agents SET name=$1 WHERE id=$2`, name, id); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Minute)
	sourcePort := 50000
	insert := func(ip, agentID string, engine core.Engine, port int) {
		t.Helper()
		sourcePort++
		_, err := db.pool.Exec(ctx, `INSERT INTO client_connections(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port,first_seen,last_seen,source)
 VALUES($1,$2,$3,'','entry','tcp',$4::inet,$5,'192.0.2.1',$6,$2,$2,'core_logs')`, agentID, now, engine, ip, sourcePort, port)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Insertion order and node order conflict with engine order deliberately.
	insert("8.8.8.10", alpha.ID, core.EngineXray, 443)
	insert("8.8.8.2", alpha.ID, core.EngineXray, 8443)
	insert("9.9.9.9", zulu.ID, core.EngineMihomo, 443)
	insert("1.1.1.1", alpha.ID, core.EngineXray, 443)
	insert("1.1.1.1", zulu.ID, core.EngineMihomo, 443)
	insert("2.2.2.2", alpha.ID, core.EngineMihomo, 443)
	insert("4.4.4.4", alpha.ID, core.EngineSingBox, 443)
	insert("8.8.4.4", zulu.ID, core.EngineXray, 443)
	insert("8.8.8.2", alpha.ID, core.EngineXray, 443)
	q := ClientConnectionQuery{GroupByIP: true, Since: now.Add(-time.Hour), Until: now.Add(time.Minute), Limit: 2, Bucket: "hour"}
	var actual []string
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber > 5 {
			t.Fatal("pagination did not terminate")
		}
		page, err := db.ClientConnectionHistory(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if pageNumber == 0 {
			// New activity before this page must not shift its enrichment or the next
			// page. Repeated observations on the same endpoint do not move an IP.
			insert("1.0.0.1", alpha.ID, core.EngineMihomo, 443)
			insert("2.2.2.2", alpha.ID, core.EngineMihomo, 443)
		}
		locationsQuery := q
		locationsQuery.RecordsOnly = true
		locationsQuery.Cursor = page.PageCursor
		locations, err := db.ClientConnectionHistory(ctx, locationsQuery)
		if err != nil || len(locations.Records) != len(page.Records) {
			t.Fatalf("locations=%+v err=%v", locations, err)
		}
		for i, r := range page.Records {
			actual = append(actual, r.ClientIP)
			if locations.Records[i].ID != r.ID {
				t.Fatalf("enrichment moved: %+v vs %+v", r, locations.Records[i])
			}
			if r.ClientIP == "1.1.1.1" && (len(r.Endpoints) != 2 || r.Endpoints[0].Engine != core.EngineMihomo || r.Endpoints[0].AgentID != zulu.ID || r.Endpoints[1].Engine != core.EngineXray) {
				t.Fatalf("endpoints=%+v", r.Endpoints)
			}
			if r.ClientIP == "8.8.8.2" && (len(r.Endpoints) != 2 || r.Endpoints[0].LocalPort != 443 || r.Endpoints[1].LocalPort != 8443) {
				t.Fatalf("ports=%+v", r.Endpoints)
			}
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	expected := []string{"2.2.2.2", "1.1.1.1", "9.9.9.9", "4.4.4.4", "8.8.8.2", "8.8.8.10", "8.8.4.4"}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("order=%v want=%v", actual, expected)
	}
	q.Cursor, q.AgentID, q.Engine = "", alpha.ID, core.EngineXray
	filtered, err := db.ClientConnectionHistory(ctx, q)
	if err != nil || len(filtered.Records) != 2 || filtered.Records[0].ClientIP != "1.1.1.1" || filtered.Records[1].ClientIP != "8.8.8.2" {
		t.Fatalf("filtered=%+v err=%v", filtered, err)
	}
}
