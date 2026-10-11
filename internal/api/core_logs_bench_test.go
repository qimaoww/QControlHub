package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

// Real PostgreSQL and HTTP middleware, synthetic messages. The unconditional
// handler isolates the cost of validators while sharing the current store,
// connection, rows and gzip settings with conditional reads.
func BenchmarkCoreLogRefresh(b *testing.B) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		b.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	for _, messageBytes := range []int{160, 4096} {
		b.Run(fmt.Sprintf("message-%d", messageBytes), func(b *testing.B) {
			schema, err := testdb.IsolatePostgres(ctx, databaseURL)
			if err != nil {
				b.Fatal(err)
			}
			defer schema.Close(ctx)
			db, err := store.Open(ctx, schema.URL, true)
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			connection, err := pgx.Connect(ctx, schema.URL)
			if err != nil {
				b.Fatal(err)
			}
			defer connection.Close(ctx)
			// Seed three nodes with 2000 lines for each of the four engines.
			// Vary a prefix so compression does not reduce every line to one copy.
			for node := range 3 {
				agentID := fmt.Sprintf("agt_%016x", node+1)
				batchID := fmt.Sprintf("log_%016x", node+1)
				if _, err := connection.Exec(ctx, `INSERT INTO agents
					(id,name,os,arch,capabilities,public_key,last_seen,enrolled_at)
					VALUES($1,'log benchmark','linux','amd64','["mihomo"]',decode(repeat(md5($1),2),'hex'),now(),now());`, agentID); err != nil {
					b.Fatal(err)
				}
				if _, err := connection.Exec(ctx, `INSERT INTO core_log_batches(id,agent_id,received_at) VALUES($1,$2,now())`, batchID, agentID); err != nil {
					b.Fatal(err)
				}
				if _, err := connection.Exec(ctx, `INSERT INTO core_logs
					(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at)
					SELECT $1,n-1,$2,(ARRAY['mihomo','xray','sing-box','ss-rust'])[(n-1)/2000+1],
					'warning',left(md5(n::text)||repeat(' long diagnostic line',200),$3),now(),now()
					FROM generate_series(1,8000) n`, batchID, agentID, messageBytes); err != nil {
					b.Fatal(err)
				}
			}
			if _, err := connection.Exec(ctx, `VACUUM (ANALYZE) core_logs`); err != nil {
				b.Fatal(err)
			}
			token := strings.Repeat("b", 48)
			server := New(db, Config{AdminToken: token})
			current := server.Handler()
			legacy := server.middleware(server.requirePermission(core.PermissionCoreLogsRead, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entries, err := db.ListCoreLogs(r.Context(), store.CoreLogQuery{AgentID: "agt_0000000000000001", Limit: 2000})
				if err != nil {
					writeStoreError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, entries)
			})))
			fetch := func(handler http.Handler, etag string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(http.MethodGet, "/api/v1/core-logs?agent_id=agt_0000000000000001&limit=2000", nil)
				request.Header.Set("Authorization", "Bearer "+token)
				request.Header.Set("Accept-Encoding", "gzip")
				request.Header.Set("If-None-Match", etag)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				return response
			}
			etag := fetch(current, "").Header().Get("ETag")
			for _, scenario := range []struct {
				name    string
				handler http.Handler
				etag    string
				status  int
			}{
				{"unconditional", legacy, "", http.StatusOK},
				{"modified", current, "", http.StatusOK},
				{"unchanged", current, etag, http.StatusNotModified},
			} {
				b.Run(scenario.name, func(b *testing.B) {
					b.ReportAllocs()
					var bodyBytes int
					for b.Loop() {
						response := fetch(scenario.handler, scenario.etag)
						if response.Code != scenario.status {
							b.Fatalf("status=%d, want %d: %s", response.Code, scenario.status, response.Body.String())
						}
						bodyBytes = response.Body.Len()
					}
					b.ReportMetric(float64(bodyBytes), "body-bytes/op")
				})
			}
		})
	}
}
