package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

type pressureDelayWriter struct {
	io.Writer
	delay time.Duration
}

func (writer pressureDelayWriter) Write(data []byte) (int, error) {
	time.Sleep(writer.delay)
	return writer.Writer.Write(data)
}

// The proxy is loopback-only. Both the direct seed connection and delayed
// application connection point at the same random, disposable test schema.
func pressureDatabaseProxy(t *testing.T, databaseURL string, delay time.Duration) string {
	t.Helper()
	if delay == 0 {
		return databaseURL
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	target := parsed.Host
	if parsed.Port() == "" {
		target = net.JoinHostPort(parsed.Hostname(), "5432")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sockets []net.Conn
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			upstream, err := net.DialTimeout("tcp", target, 5*time.Second)
			if err != nil {
				client.Close()
				continue
			}
			mu.Lock()
			sockets = append(sockets, client, upstream)
			mu.Unlock()
			group.Add(2)
			go func() {
				defer group.Done()
				defer client.Close()
				defer upstream.Close()
				_, _ = io.Copy(pressureDelayWriter{upstream, delay}, client)
			}()
			go func() {
				defer group.Done()
				defer client.Close()
				defer upstream.Close()
				_, _ = io.Copy(client, upstream)
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for _, socket := range sockets {
			socket.Close()
		}
		mu.Unlock()
		group.Wait()
	})
	parsed.Host = listener.Addr().String()
	return parsed.String()
}

type pressureResult struct {
	latency    []time.Duration
	failures   int
	firstError string
}

// Opt-in mixed load: real HTTP GETs compete with durable telemetry writes for
// the normal 20-connection pool. Never use a production database account.
func TestRemoteDatabasePressure(t *testing.T) {
	if os.Getenv("QCH_TEST_PRESSURE") != "1" {
		t.Skip("set QCH_TEST_PRESSURE=1 for the mixed-load test")
	}
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("QCH_TEST_DATABASE_URL is required")
	}
	duration := 30 * time.Second
	if value := os.Getenv("QCH_PRESSURE_DURATION"); value != "" {
		var err error
		duration, err = time.ParseDuration(value)
		if err != nil || duration < 10*time.Second || duration > 2*time.Minute {
			t.Fatal("QCH_PRESSURE_DURATION must be 10s through 2m")
		}
	}
	ctx := context.Background()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := schema.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	seedStore, err := store.Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	seedStore.Close()
	seed, err := pgxpool.New(ctx, schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(seed.Close)
	// Use set-based fixture construction, not the code under measurement.
	_, err = seed.Exec(ctx, `
		INSERT INTO agents(id,name,os,arch,capabilities,public_key,last_seen,enrolled_at,runtime,metrics)
		SELECT 'agt_'||lpad(to_hex(n),16,'0'),'load node '||n,'linux','amd64','["mihomo","xray","sing-box","ss-rust"]',
		       decode(repeat(md5(n::text),2),'hex'),now(),now(),'{"mihomo":{"installed":true}}',
		       '{"cpu_available":true,"cpu_percent":25,"memory_available":true,"memory_total_bytes":4294967296,"memory_used_bytes":1073741824}'
		FROM generate_series(1,200) n;
		INSERT INTO configs(id,agent_id,name,description,engine,content,version,created_at,updated_at)
		SELECT 'cfg_'||lpad(to_hex(n),16,'0'),'agt_'||lpad(to_hex(n),16,'0'),'load config','','mihomo',E'mixed-port: 7890\n',1,now(),now()
		FROM generate_series(1,200) n;
		INSERT INTO config_revisions(config_id,version,agent_id,name,description,engine,content,created_at)
		SELECT id,version,agent_id,name,description,engine,content,updated_at FROM configs;
		INSERT INTO tasks(id,agent_id,action,engine,config_id,config_version,status,created_at,finished_at)
		SELECT 'tsk_'||lpad(to_hex(n),16,'0'),'agt_'||lpad(to_hex((n-1)%200+1),16,'0'),
		       CASE WHEN n<=200 THEN 'deploy' ELSE 'status' END,'mihomo',
		       'cfg_'||lpad(to_hex((n-1)%200+1),16,'0'),1,CASE WHEN n<=200 THEN 'succeeded' ELSE 'failed' END,
		       now()-n*interval '1 second',now()-n*interval '1 second'
		FROM generate_series(1,50000) n;
		INSERT INTO port_traffic_policies(id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,created_at,updated_at)
		SELECT 'trf_'||lpad(to_hex(n),16,'0'),'agt_'||lpad(to_hex((n-1)/16+1),16,'0'),'load port','mihomo',
		       10000+(n-1)%16,'both','monthly',date_trunc('month',now()),1000000000,now(),now()
		FROM generate_series(1,3200) n;
		INSERT INTO port_traffic_daily_usage(policy_id,reset_generation,usage_date,agent_id,name,engine,port,protocol,
		       received_bytes,sent_bytes,used_bytes,sample_count,first_reported_at,last_reported_at)
		SELECT policy.id,1,(current_date-days)::date,policy.agent_id,policy.name,policy.engine,policy.port,policy.protocol,
		       100000,100000,200000,10,now(),now()
		FROM port_traffic_policies policy CROSS JOIN generate_series(1,31) days;
		INSERT INTO core_log_batches(id,agent_id,received_at)
		SELECT 'log_'||lpad(to_hex(n),16,'0'),'agt_'||lpad(to_hex(n),16,'0'),now() FROM generate_series(1,200) n;
		INSERT INTO core_logs(batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at)
		SELECT 'log_'||lpad(to_hex((n-1)%200+1),16,'0'),(n-1)/200,'agt_'||lpad(to_hex((n-1)%200+1),16,'0'),
		       (ARRAY['mihomo','xray','sing-box','ss-rust'])[((n-1)/200)%4+1],'warning',
		       'load fixture '||n||' '||repeat('x',96),now()-n*interval '1 millisecond',now()
		FROM generate_series(1,400000) n;
		INSERT INTO metric_samples(agent_id,sampled_at,cpu_percent,memory_percent,rx_rate_bps,tx_rate_bps)
		SELECT agent.id,now()-n*interval '1 minute',25,25,1000,1000 FROM agents agent CROSS JOIN generate_series(1,300) n;
		ANALYZE;
	`)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("fixture: 200 agents, 3200 ports, 50000 tasks, 400000 logs, 99200 daily usage rows, 60000 metric samples")
	for _, phase := range []struct {
		workers int
		delay   time.Duration
	}{{64, 0}, {64, 20 * time.Millisecond}, {128, 20 * time.Millisecond}} {
		t.Run(fmt.Sprintf("workers%d-delay%s", phase.workers, phase.delay), func(t *testing.T) {
			url := pressureDatabaseProxy(t, schema.URL, phase.delay)
			dataStore, err := store.Open(ctx, url, true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(dataStore.Close)
			token := strings.Repeat("pressure-test-only-", 3)
			server := httptest.NewServer(New(dataStore, Config{AdminToken: token}).Handler())
			t.Cleanup(server.Close)
			transport := &http.Transport{MaxIdleConns: phase.workers, MaxIdleConnsPerHost: phase.workers}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			month := time.Now().UTC().Format("2006-01")
			paths := []string{"/agents", "/overview", "/tasks?status=failed&limit=100", "/client-access",
				"/agents/agt_0000000000000001/configs", "/core-logs?limit=1000", "/core-logs?limit=2000",
				"/core-logs?agent_id=agt_0000000000000001&engine=mihomo&limit=2000",
				"/traffic-usage?month=" + month, "/metrics/agt_0000000000000001", "/access-controls"}
			var mu sync.Mutex
			results := map[string]*pressureResult{}
			var group sync.WaitGroup
			start := time.Now()
			deadline := start.Add(duration)
			for worker := 0; worker < phase.workers; worker++ {
				group.Add(1)
				go func(worker int) {
					defer group.Done()
					agentNumber := worker + 1
					agentID := fmt.Sprintf("agt_%016x", agentNumber)
					period := time.Now().UTC().Truncate(time.Second)
					for iteration := 0; time.Now().Before(deadline); iteration++ {
						operationCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
						began := time.Now()
						var name string
						var operationErr error
						if worker%2 == 0 {
							name = paths[(worker/2+iteration)%len(paths)]
							request, _ := http.NewRequestWithContext(operationCtx, http.MethodGet, server.URL+"/api/v1"+name, nil)
							request.Header.Set("Authorization", "Bearer "+token)
							response, err := client.Do(request)
							operationErr = err
							if err == nil {
								_, operationErr = io.Copy(io.Discard, response.Body)
								response.Body.Close()
								if response.StatusCode != http.StatusOK {
									operationErr = fmt.Errorf("HTTP %d", response.StatusCode)
								}
							}
						} else {
							switch iteration % 5 {
							case 0:
								name = "write/logs32"
								id, _ := core.NewID("log")
								entries := make([]core.CoreLogEntry, core.MaxCoreLogBatchEntries)
								for i := range entries {
									entries[i] = core.CoreLogEntry{Engine: core.EngineMihomo, Level: "warning", Message: "concurrent telemetry pressure fixture"}
								}
								operationErr = dataStore.StoreCoreLogs(operationCtx, agentID, core.CoreLogBatch{ID: id, Entries: entries})
							case 1:
								name = "write/traffic16"
								usages := make([]core.PortTrafficUsage, 16)
								for i := range usages {
									usages[i] = core.PortTrafficUsage{PolicyID: fmt.Sprintf("trf_%016x", (agentNumber-1)*16+i+1), ResetGeneration: 1, ReceivedBytes: uint64(iteration+1) * 100, UsedBytes: uint64(iteration+1) * 100, PeriodStart: period, PeriodEnd: period.AddDate(0, 1, 0)}
								}
								operationErr = dataStore.UpdatePortTrafficUsage(operationCtx, agentID, usages, time.Now())
							case 2:
								name = "write/metrics"
								operationErr = dataStore.UpdateAgentMetrics(operationCtx, agentID, core.HostMetrics{CPUAvailable: true, CPUPercent: 25})
							case 3:
								name = "poll/idle-task"
								_, operationErr = dataStore.ClaimTask(operationCtx, agentID)
							case 4:
								name = "poll/presence"
								_, operationErr = dataStore.AgentPresenceTransitions(operationCtx, time.Now(), 45*time.Second)
							}
						}
						elapsed := time.Since(began)
						cancel()
						mu.Lock()
						result := results[name]
						if result == nil {
							result = &pressureResult{}
							results[name] = result
						}
						result.latency = append(result.latency, elapsed)
						if operationErr != nil {
							result.failures++
							if result.firstError == "" {
								result.firstError = operationErr.Error()
							}
						}
						mu.Unlock()
					}
				}(worker)
			}
			group.Wait()
			elapsed := time.Since(start)
			names := make([]string, 0, len(results))
			for name := range results {
				names = append(names, name)
			}
			sort.Strings(names)
			total, failures := 0, 0
			for _, name := range names {
				result := results[name]
				sort.Slice(result.latency, func(i, j int) bool { return result.latency[i] < result.latency[j] })
				n := len(result.latency)
				total += n
				failures += result.failures
				t.Logf("%s requests=%d errors=%d p50=%s p95=%s p99=%s max=%s", name, n, result.failures, result.latency[(n-1)*50/100].Round(time.Millisecond), result.latency[(n-1)*95/100].Round(time.Millisecond), result.latency[(n-1)*99/100].Round(time.Millisecond), result.latency[n-1].Round(time.Millisecond))
				if result.failures > 0 {
					t.Errorf("%s: %s", name, result.firstError)
				}
			}
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			t.Logf("SUMMARY workers=%d transport_delay=%s operations=%d throughput=%.1f/s errors=%d elapsed=%s heap_end=%.1fMiB", phase.workers, phase.delay, total, float64(total)/elapsed.Seconds(), failures, elapsed.Round(time.Millisecond), float64(memory.HeapAlloc)/(1<<20))
			if total < 100 {
				t.Errorf("insufficient load: %d operations", total)
			}
		})
	}
}
