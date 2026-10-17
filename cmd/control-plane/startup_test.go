//go:build linux

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestDatabaseStartupTimeout(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  time.Duration
	}{
		{"", 5 * time.Minute}, {" 8m ", 8 * time.Minute},
		{"1s", time.Second}, {"1h", time.Hour},
		{"0", 0}, {"-1s", 0}, {"500ms", 0}, {"61m", 0}, {"invalid", 0},
	} {
		t.Run(tt.value, func(t *testing.T) {
			got, err := databaseStartupTimeout(tt.value)
			if got != tt.want || (err != nil) != (tt.want == 0) {
				t.Fatalf("timeout(%q) = %s, %v; want %s", tt.value, got, err, tt.want)
			}
		})
	}
}

// Run the actual main in a subprocess: a store-only test with a longer caller
// context would miss the old hard-coded 20-second deadline in main itself.
func TestControlPlaneStartupProcess(t *testing.T) {
	if os.Getenv("QCH_TEST_STARTUP_PROCESS") == "1" {
		main()
		os.Exit(0)
	}
}

func TestControlPlaneWaitsForMigrationAndBoundsFailure(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	for _, timeout := range []string{"", "2s"} {
		t.Run("timeout="+timeout, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			schema, err := testdb.IsolatePostgres(ctx, databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
				defer done()
				if err := schema.Close(cleanup); err != nil {
					t.Error(err)
				}
			}()
			db, err := store.Open(ctx, schema.URL, true)
			if err != nil {
				t.Fatal(err)
			}
			db.Close()
			connection, err := pgx.Connect(ctx, schema.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close(context.Background())
			// Replaying the historical schema is the expensive upgrade path on
			// the affected installation, which still reports schema v73.
			if _, err := connection.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations;
				INSERT INTO qcontrolhub_schema_migrations(version) VALUES(73)`); err != nil {
				t.Fatal(err)
			}
			blocker, err := connection.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			if _, err := blocker.Exec(ctx, `LOCK TABLE agents IN ACCESS SHARE MODE`); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			logPath := filepath.Join(t.TempDir(), "startup.log")
			log, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestControlPlaneStartupProcess$")
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, "QCH_") {
					command.Env = append(command.Env, value)
				}
			}
			command.Env = append(command.Env, "QCH_TEST_STARTUP_PROCESS=1", "QCH_DATABASE_URL="+schema.URL,
				"QCH_ALLOW_INSECURE_DATABASE=true", "QCH_ADMIN_TOKEN="+strings.Repeat("t", 48),
				"QCH_LISTEN="+address, "QCH_DATABASE_STARTUP_TIMEOUT="+timeout, "QCH_AGENT_PUBLIC_IP_PROBE_ENABLED=false")
			command.Stdout, command.Stderr = log, log
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { result <- command.Wait() }()
			defer func() {
				command.Process.Signal(os.Interrupt)
				select {
				case <-result:
				case <-time.After(5 * time.Second):
					command.Process.Kill()
				}
			}()
			client := &http.Client{Timeout: time.Second}
			if timeout != "" {
				select {
				case err := <-result:
					result <- err // Allow the deferred process cleanup to observe completion.
					if err == nil {
						t.Fatal("blocked migration unexpectedly succeeded")
					}
				case <-time.After(10 * time.Second):
					t.Fatal("configured migration deadline did not terminate startup")
				}
				output, _ := os.ReadFile(logPath)
				if !strings.Contains(string(output), "context deadline exceeded") || !strings.Contains(string(output), "v73 ->") {
					t.Fatalf("missing migration diagnostics: %s", output)
				}
			} else {
				select {
				case err := <-result:
					result <- err
					output, _ := os.ReadFile(logPath)
					t.Fatalf("startup exited before migration could finish: %v\n%s", err, output)
				case <-time.After(22 * time.Second):
				}
				if response, err := client.Get("http://" + address + "/readyz"); err == nil {
					response.Body.Close()
					t.Fatal("readiness was exposed before migration committed")
				}
			}
			if err := blocker.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if timeout == "" {
				deadline := time.Now().Add(10 * time.Second)
				for {
					response, err := client.Get("http://" + address + "/readyz")
					if err == nil {
						response.Body.Close()
						if response.StatusCode == http.StatusOK {
							break
						}
					}
					if time.Now().After(deadline) {
						output, _ := os.ReadFile(logPath)
						t.Fatalf("startup did not recover after lock release: %s", output)
					}
					time.Sleep(50 * time.Millisecond)
				}
			} else {
				var version int
				if err := connection.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil || version != 73 {
					t.Fatalf("failed migration changed the ledger: v%d, %v", version, err)
				}
				retried, err := store.Open(ctx, schema.URL, true)
				if err != nil {
					t.Fatal(fmt.Errorf("retry canceled migration: %w", err))
				}
				retried.Close()
			}
		})
	}
}
