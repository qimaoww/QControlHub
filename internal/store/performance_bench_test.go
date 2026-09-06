package store

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

// Delay actual PostgreSQL protocol writes, including prepare and transaction
// messages. This is synthetic transport latency, not a production measurement.
type delayedDatabaseConn struct {
	net.Conn
	delay  time.Duration
	writes *atomic.Int64
}

func (conn *delayedDatabaseConn) Write(data []byte) (int, error) {
	if conn.delay > 0 {
		time.Sleep(conn.delay)
	}
	conn.writes.Add(1)
	return conn.Conn.Write(data)
}

func openPerformanceStore(tb testing.TB) *Store {
	tb.Helper()
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		tb.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := schema.Close(ctx); err != nil {
			tb.Error(err)
		}
	})
	dataStore, err := OpenWithConfigKey(ctx, schema.URL, true, "performance-test-key-not-for-production")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(dataStore.Close)
	return dataStore
}

func seedPerformanceAgent(tb testing.TB, dataStore *Store, ports int) (string, []core.PortTrafficUsage) {
	tb.Helper()
	ctx := context.Background()
	agentID, err := core.NewID("agt")
	if err != nil {
		tb.Fatal(err)
	}
	_, err = dataStore.pool.Exec(ctx, `INSERT INTO agents
		(id,name,os,arch,capabilities,public_key,last_seen,enrolled_at,runtime)
		VALUES ($1,'performance fixture','linux','amd64','["mihomo"]',decode(repeat(md5($1),2),'hex'),now(),now(),
		'{"mihomo":{"installed":true,"service_status":"active"}}')`, agentID)
	if err != nil {
		tb.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Second)
	usages := make([]core.PortTrafficUsage, 0, ports)
	for index := range ports {
		id, err := core.NewID("trf")
		if err != nil {
			tb.Fatal(err)
		}
		_, err = dataStore.pool.Exec(ctx, `INSERT INTO port_traffic_policies
			(id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,created_at,updated_at)
			VALUES ($1,$2,'fixture','mihomo',$3,'both','monthly',$4,1000000,now(),now())`, id, agentID, 10000+index, start)
		if err != nil {
			tb.Fatal(err)
		}
		usages = append(usages, core.PortTrafficUsage{
			PolicyID: id, ResetGeneration: 1, ReceivedBytes: 100, UsedBytes: 100,
			PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), EnforcementAvailable: true,
		})
	}
	return agentID, usages
}

func delayedPerformanceStore(tb testing.TB, source *Store, latency time.Duration) (*Store, *atomic.Int64) {
	tb.Helper()
	config := source.pool.Config()
	config.MaxConns, config.MinConns = 1, 0
	config.ShouldPing = func(context.Context, pgxpool.ShouldPingParams) bool { return false }
	dial := config.ConnConfig.DialFunc
	writes := &atomic.Int64{}
	config.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &delayedDatabaseConn{Conn: conn, delay: latency, writes: writes}, nil
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(pool.Close)
	return &Store{pool: pool, cryptor: source.cryptor}, writes
}

func BenchmarkRemoteDatabase(b *testing.B) {
	for _, name := range []string{"logs32", "traffic64", "idle-task", "presence64"} {
		b.Run(name, func(b *testing.B) {
			base := openPerformanceStore(b)
			ctx := context.Background()
			ports := 0
			if name == "traffic64" {
				ports = 64
			}
			agentID, usages := seedPerformanceAgent(b, base, ports)
			if name == "presence64" {
				for range 63 {
					seedPerformanceAgent(b, base, 0)
				}
			}
			measured, writes := delayedPerformanceStore(b, base, 20*time.Millisecond)
			now := time.Now().UTC()
			entries := make([]core.CoreLogEntry, core.MaxCoreLogBatchEntries)
			for index := range entries {
				entries[index] = core.CoreLogEntry{Engine: core.EngineMihomo, Level: "warning", Message: "benchmark log", LoggedAt: now}
			}
			operation := func(index int) error {
				switch name {
				case "logs32":
					return measured.StoreCoreLogs(ctx, agentID, core.CoreLogBatch{ID: fmt.Sprintf("log_%016x", index), Entries: entries})
				case "traffic64":
					return measured.UpdatePortTrafficUsage(ctx, agentID, usages, now.Add(time.Duration(index)*time.Second))
				case "idle-task":
					_, err := measured.ClaimTask(ctx, agentID)
					return err
				default:
					_, err := measured.AgentPresenceTransitions(ctx, now, 45*time.Second)
					return err
				}
			}
			// Warm the connection/statement cache outside the timed region.
			if err := operation(0); err != nil {
				b.Fatal(err)
			}
			writes.Store(0)
			b.ResetTimer()
			for index := 1; index <= b.N; index++ {
				if name == "presence64" {
					b.StopTimer()
					if _, err := base.pool.Exec(ctx, `UPDATE agents SET presence_notification_state='offline'`); err != nil {
						b.Fatal(err)
					}
					b.StartTimer()
				}
				if err := operation(index); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(writes.Load())/float64(b.N), "wire-writes/op")
		})
	}
}

func BenchmarkRemoteLogVolume(b *testing.B) {
	for _, total := range []int{1000, 2000} {
		b.Run(fmt.Sprintf("write%d", total), func(b *testing.B) {
			base := openPerformanceStore(b)
			agentID, _ := seedPerformanceAgent(b, base, 0)
			measured, writes := delayedPerformanceStore(b, base, 20*time.Millisecond)
			ctx := context.Background()
			warmID, _ := core.NewID("log")
			if err := measured.StoreCoreLogs(ctx, agentID, core.CoreLogBatch{ID: warmID, Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, Level: "warning", Message: "warm"}}}); err != nil {
				b.Fatal(err)
			}
			writes.Store(0)
			b.ResetTimer()
			for range b.N {
				for offset := 0; offset < total; offset += core.MaxCoreLogBatchEntries {
					entries := make([]core.CoreLogEntry, min(core.MaxCoreLogBatchEntries, total-offset))
					for index := range entries {
						entries[index] = core.CoreLogEntry{Engine: core.EngineMihomo, Level: "warning", Message: "volume benchmark log"}
					}
					id, _ := core.NewID("log")
					if err := measured.StoreCoreLogs(ctx, agentID, core.CoreLogBatch{ID: id, Entries: entries}); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(writes.Load())/float64(b.N), "wire-writes/op")
			b.ReportMetric(float64(total*b.N)/b.Elapsed().Seconds(), "logs/s")
		})
	}
}
