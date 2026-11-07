package store

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Opt-in latency samples use real PostgreSQL, synthetic log messages and an
// explicitly artificial protocol-write delay. These are request samples,
// unlike the averages reported by testing.B; never label them production SLOs.
func TestLogIngestionMeasurements(t *testing.T) {
	if os.Getenv("QCH_LOG_INGEST_MEASURE") != "1" {
		t.Skip("opt-in performance measurement")
	}
	for _, latency := range []time.Duration{0, 20 * time.Millisecond} {
		for _, workers := range []int{1, 4} {
			for _, shared := range []bool{false, true} {
				t.Run(fmt.Sprintf("delay=%s/workers=%d/shared=%t", latency, workers, shared), func(t *testing.T) {
					base := openPerformanceStore(t)
					ctx := context.Background()
					agents := make([]string, workers)
					entries := make([][]core.CoreLogEntry, workers)
					for worker := range workers {
						agents[worker], _ = seedPerformanceAgent(t, base, 0)
						var instance, owner string
						if shared {
							user, _ := sharedTestUser(t, base, ctx, fmt.Sprintf("measure-user-%d", worker))
							owner = user.ID
							instance = fmt.Sprintf("shr_%016x", worker+1)
							if _, err := base.pool.Exec(ctx, `INSERT INTO agent_shares(id,agent_id,user_id,status,engines) VALUES($1,$2,$3,'accepted',ARRAY['mihomo'])`, instance, agents[worker], owner); err != nil {
								t.Fatal(err)
							}
							if _, err := base.pool.Exec(ctx, `INSERT INTO agent_shared_instance_ownership(agent_id,engine,share_id,owner_id,config_id,config_version,updated_at) VALUES($1,'mihomo',$2,$3,'measurement',1,now())`, agents[worker], instance, owner); err != nil {
								t.Fatal(err)
							}
						}
						for range core.MaxCoreLogBatchEntries {
							entry := core.CoreLogEntry{Engine: core.EngineMihomo, Level: "warning", Message: "measurement diagnostic line", LoggedAt: time.Now()}
							if shared {
								entry.InstanceID = instance
								entry.Service = core.CoreLogService(entry.Engine, instance, "systemd")
							}
							entries[worker] = append(entries[worker], entry)
						}
					}
					// Each worker owns a one-connection pool, representing independent
					// node writers without an unmeasured pool-size change.
					stores := make([]*Store, workers)
					writes := make([]*atomic.Int64, workers)
					for i := range workers {
						stores[i], writes[i] = delayedPerformanceStore(t, base, latency)
					}
					const samples = 128
					for repeat := range 3 {
						for i := range workers {
							id, _ := core.NewID("log")
							if err := stores[i].StoreCoreLogs(ctx, agents[i], core.CoreLogBatch{ID: id, Entries: entries[i]}); err != nil {
								t.Fatal(err)
							}
							writes[i].Store(0)
						}
						beforePool := make([]*pgxpool.Stat, workers)
						for i := range stores {
							beforePool[i] = stores[i].pool.Stat()
						}
						var before, after runtime.MemStats
						runtime.ReadMemStats(&before)
						elapsed := make([]time.Duration, samples)
						errors := make(chan error, workers)
						var group sync.WaitGroup
						start := time.Now()
						for worker := range workers {
							group.Go(func() {
								for index := worker; index < samples; index += workers {
									id, err := core.NewID("log")
									if err != nil {
										errors <- err
										return
									}
									began := time.Now()
									err = stores[worker].StoreCoreLogs(ctx, agents[worker], core.CoreLogBatch{ID: id, Entries: entries[worker]})
									elapsed[index] = time.Since(began)
									if err != nil {
										errors <- err
										return
									}
								}
							})
						}
						group.Wait()
						duration := time.Since(start)
						close(errors)
						for err := range errors {
							t.Fatal(err)
						}
						runtime.ReadMemStats(&after)
						var wire int64
						var wait time.Duration
						for i := range stores {
							wire += writes[i].Load()
							wait += stores[i].pool.Stat().EmptyAcquireWaitTime() - beforePool[i].EmptyAcquireWaitTime()
						}
						sort.Slice(elapsed, func(i, j int) bool { return elapsed[i] < elapsed[j] })
						var count int
						if err := base.pool.QueryRow(ctx, `SELECT count(*) FROM core_logs WHERE agent_id=ANY($1)`, agents).Scan(&count); err != nil {
							t.Fatal(err)
						}
						want := (repeat + 1) * (samples + workers) * core.MaxCoreLogBatchEntries
						if count != want {
							t.Fatalf("rows=%d want=%d", count, want)
						}
						t.Logf("repeat=%d samples=%d p50_ms=%.3f p95_ms=%.3f min_ms=%.3f max_ms=%.3f batches_per_second=%.2f wire_writes_per_batch=%.2f bytes_per_batch=%d allocs_per_batch=%d gc=%d pool_wait_ms=%.3f rows=%d", repeat, samples, float64(elapsed[samples/2])/float64(time.Millisecond), float64(elapsed[(samples*95+99)/100-1])/float64(time.Millisecond), float64(elapsed[0])/float64(time.Millisecond), float64(elapsed[samples-1])/float64(time.Millisecond), samples/duration.Seconds(), float64(wire)/samples, (after.TotalAlloc-before.TotalAlloc)/samples, (after.Mallocs-before.Mallocs)/samples, after.NumGC-before.NumGC, float64(wait)/float64(time.Millisecond), count)
					}
				})
			}
		}
	}
}

func TestCoreLogPipelinedRollbackAndConcurrentDeduplication(t *testing.T) {
	db := openPerformanceStore(t)
	ctx := context.Background()
	agent, _ := seedPerformanceAgent(t, db, 0)
	batch := core.CoreLogBatch{ID: "log_0000000000000001", Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, Level: "info", Message: "transaction test"}}}
	// Force failure after both pipelined statements. A retry must commit one
	// marker and one row, rather than lose the line behind a committed marker.
	if _, err := db.pool.Exec(ctx, `CREATE FUNCTION reject_log_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture log insert failure'; END $$; CREATE TRIGGER reject_log_insert BEFORE INSERT ON core_logs FOR EACH ROW EXECUTE FUNCTION reject_log_insert()`); err != nil {
		t.Fatal(err)
	}
	if err := db.StoreCoreLogs(ctx, agent, batch); err == nil {
		t.Fatal("injected failure succeeded")
	}
	var markers int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM core_log_batches WHERE id=$1`, batch.ID).Scan(&markers); err != nil || markers != 0 {
		t.Fatalf("failed upload retained marker: %d %v", markers, err)
	}
	if _, err := db.pool.Exec(ctx, `DROP TRIGGER reject_log_insert ON core_logs`); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if err := db.StoreCoreLogs(ctx, agent, batch); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	var rows int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM core_logs WHERE batch_id=$1`, batch.ID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("concurrent retry rows=%d %v", rows, err)
	}
	// A cancelled lock wait cannot leave a dedup marker, nor can a revoked
	// node recreate logs even though the pipelined insert has been sent.
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE agents SET revoked_at=now() WHERE id=$1`, agent); err != nil {
		t.Fatal(err)
	}
	batch.ID = "log_0000000000000002"
	cancelled, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	err = db.StoreCoreLogs(cancelled, agent, batch)
	cancel()
	if err == nil {
		t.Fatal("cancelled lock wait succeeded")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.StoreCoreLogs(ctx, agent, batch); err == nil {
		t.Fatal("revoked agent upload succeeded")
	}
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM core_log_batches WHERE id=$1`, batch.ID).Scan(&markers); err != nil || markers != 0 {
		t.Fatalf("cancelled/revoked upload retained marker: %d %v", markers, err)
	}
}
