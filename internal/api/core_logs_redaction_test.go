package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestCoreLogLongWindowAuthentication(t *testing.T) {
	if os.Getenv("QCH_TEST_DATABASE_URL") == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	schema, err := testdb.IsolatePostgres(ctx, os.Getenv("QCH_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(ctx)
	db, err := store.Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, `INSERT INTO agents(id,name,os,arch,capabilities,public_key,last_seen,enrolled_at) VALUES('agt_0000000000000001','probe','linux','amd64','["mihomo"]',decode(repeat('ab',32),'hex'),now(),now()); INSERT INTO core_log_batches(id,agent_id,received_at) VALUES('log_0000000000000001','agt_0000000000000001',now())`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO core_logs(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at,instance_id,owner_id,node_owner_id) SELECT 'log_0000000000000001',n-1,'agt_0000000000000001',(ARRAY['mihomo','xray','sing-box','ss-rust'])[(n-1)/2000+1],'info',left(md5(n::text)||repeat(' [TCP] 192.0.2.1:443 --> probe.invalid:8443 using DIRECT',100),4096),now(),now(),'main','','' FROM generate_series(1,8000) n`)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("p", 48)
	server := New(db, Config{AdminToken: token})
	handler := server.Handler()
	req := httptest.NewRequest("POST", "/api/v1/settings/api-keys", strings.NewReader(`{"name":"probe","permissions":["core-logs.read"],"mode":"read_only"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("key: %d %s", rec.Code, rec.Body)
	}
	var key struct {
		Secret string `json:"secret"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
	login.Header.Set("Content-Type", "application/json")
	loggedIn := httptest.NewRecorder()
	handler.ServeHTTP(loggedIn, login)
	if loggedIn.Code != http.StatusOK || len(loggedIn.Result().Cookies()) == 0 {
		t.Fatal("cookie login failed")
	}
	cookie := loggedIn.Result().Cookies()[0]
	for _, auth := range []struct{ name, token string }{{"cookie", ""}, {"api-key", key.Secret}} {
		for _, limit := range []int{100, 200, 500, 1000, 2000} {
			req = httptest.NewRequest("GET", fmt.Sprintf("/api/v1/core-logs?limit=%d", limit), nil)
			if auth.token != "" {
				req.Header.Set("Authorization", "Bearer "+auth.token)
			} else {
				req.AddCookie(cookie)
			}
			req.Header.Set("Accept-Encoding", "gzip")
			rec = httptest.NewRecorder()
			start := time.Now()
			handler.ServeHTTP(rec, req)
			elapsed, wireBytes := time.Since(start), rec.Body.Len()
			if rec.Code != http.StatusOK {
				t.Fatalf("auth=%s status=%d: %s", auth.name, rec.Code, rec.Body)
			}
			if elapsed > 12*time.Second {
				t.Fatalf("auth=%s exceeded existing read deadline", auth.name)
			}
			var body io.Reader = rec.Body
			if rec.Header().Get("Content-Encoding") == "gzip" {
				gz, e := gzip.NewReader(rec.Body)
				if e != nil {
					t.Fatal(e)
				}
				defer gz.Close()
				body = gz
			}
			var entries []core.CoreLogEntry
			if err := json.NewDecoder(body).Decode(&entries); err != nil {
				t.Fatal(err)
			}
			counts := map[core.Engine]int{}
			for i, entry := range entries {
				counts[entry.Engine]++
				if len(entry.Message) != 4096 {
					t.Fatal("message truncated")
				}
				if i > 0 && entries[i-1].ID <= entry.ID {
					t.Fatal("order changed")
				}
			}
			for _, engine := range core.AllEngines() {
				if counts[engine] != limit {
					t.Fatalf("%s count=%d want=%d", engine, counts[engine], limit)
				}
			}
			t.Logf("auth=%s limit=%d status=%d elapsed=%s bytes=%d", auth.name, limit, rec.Code, elapsed, wireBytes)
		}
	}
	// Exercise the real route/authentication chain for every bounded caller.
	// An exclusive resource lock must not outlive either kind of read.
	lock, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err = lock.Exec(ctx, `LOCK TABLE agents IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/core-logs", "/core-log-sources", "/client-connections"} {
		for _, apiKey := range []bool{false, true} {
			deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			req := httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil).WithContext(deadline)
			if apiKey {
				req.Header.Set("Authorization", "Bearer "+key.Secret)
			} else {
				req.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			started := time.Now()
			handler.ServeHTTP(rec, req)
			cancel()
			if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), `"code":"read_timeout"`) || rec.Header().Get("X-Request-ID") == "" || time.Since(started) > time.Second {
				t.Fatalf("path=%s key=%t did not finish timed out read: %d %s", path, apiKey, rec.Code, rec.Body)
			}
		}
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for stop := time.Now().Add(time.Second); ; {
		var waiting int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE relation='agents'::regclass AND NOT granted`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 0 {
			break
		}
		if time.Now().After(stop) {
			t.Fatal("expired reads retained database lock waiters")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, path := range []string{"/core-logs?limit=100", "/core-log-sources", "/client-connections"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil)
		req.Header.Set("Authorization", "Bearer "+key.Secret)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("fresh %s did not recover: %d %s", path, rec.Code, rec.Body)
		}
	}
}
