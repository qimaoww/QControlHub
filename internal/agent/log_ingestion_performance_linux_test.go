//go:build linux

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func BenchmarkCoreLogIngestion(b *testing.B) {
	for _, size := range []int{160, 4096} {
		for _, color := range []bool{false, true} {
			b.Run(fmt.Sprintf("bytes%d/color=%t", size, color), func(b *testing.B) {
				message := strings.Repeat("ordinary diagnostic line ", size/25+1)[:size]
				if color {
					message = "\x1b[32m" + message + "\x1b[0m"
				}
				b.ReportAllocs()
				for b.Loop() {
					if len(sanitizeCoreLogMessage(message)) == 0 {
						b.Fatal("lost message")
					}
				}
			})
		}
	}
	for _, prefix := range []string{"Authorization: Bearer test-secret ", "private_key=test-secret ", "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY----- "} {
		b.Run("redacted/"+strings.Fields(prefix)[0], func(b *testing.B) {
			message := prefix + strings.Repeat("ordinary diagnostic line ", 160)
			b.ReportAllocs()
			for b.Loop() {
				if len(sanitizeCoreLogMessage(message)) == 0 {
					b.Fatal("lost message")
				}
			}
		})
	}
	for _, sources := range []int{1, 32, 256} {
		b.Run(fmt.Sprintf("queue/sources%d", sources), func(b *testing.B) {
			collector := NewCoreLogCollector()
			collector.executor = &Executor{}
			entries := make([]core.CoreLogEntry, sources)
			for i := range entries {
				entries[i] = core.CoreLogEntry{Engine: core.EngineMihomo, InstanceID: fmt.Sprintf("shr_%016x", i), Message: "ordinary line", Level: "info"}
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				collector.append(entries[i%len(entries)])
			}
		})
	}
}

// This opt-in process probe exercises the real parser, queue, serialization and
// ACK path with synthetic records. It does not emulate a running service manager
// or include file discovery, journald subprocesses, network or host metrics.
func TestCoreLogProcessMeasurements(t *testing.T) {
	if os.Getenv("QCH_LOG_PROCESS_MEASURE") != "1" {
		t.Skip("opt-in process measurement")
	}
	mode := os.Getenv("QCH_LOG_PROCESS_CHILD")
	if mode == "" {
		for _, transport := range []string{"journal", "openrc"} {
			for _, scenario := range []string{"idle", "typical", "pressure"} {
				for repeat := range 3 {
					command := exec.Command(os.Args[0], "-test.run=^TestCoreLogProcessMeasurements$", "-test.v")
					command.Env = append(os.Environ(), "QCH_LOG_PROCESS_CHILD="+transport+"/"+scenario)
					output, err := command.CombinedOutput()
					t.Logf("repeat=%d %s", repeat, output)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		return
	}
	seconds := 3
	if setting := os.Getenv("QCH_LOG_PROCESS_SECONDS"); setting != "" {
		parsed, err := strconv.Atoi(setting)
		if err != nil || parsed < 1 || parsed > 120 {
			t.Fatal("QCH_LOG_PROCESS_SECONDS must be 1..120")
		}
		seconds = parsed
	}
	parts := strings.Split(mode, "/")
	size, rate, sources := 160, 0, 1
	if parts[1] == "typical" {
		rate = 100
	}
	if parts[1] == "pressure" {
		size, rate, sources = 4096, 400, 32
	}
	collector := NewCoreLogCollector()
	collector.executor = &Executor{}
	children := make([]*CoreLogCollector, sources)
	for i := range children {
		children[i] = NewCoreLogCollector()
		children[i].parent = collector
		if sources > 1 {
			children[i].instanceID = fmt.Sprintf("shr_%016x", i+1)
			children[i].instanceService = core.CoreLogService(core.EngineMihomo, children[i].instanceID, parts[0])
		}
	}
	message := strings.Repeat("ordinary diagnostic line ", size/25+1)[:size]
	journal, _ := json.Marshal(map[string]string{"MESSAGE": message, "_SYSTEMD_UNIT": "qagent-mihomo.service", "PRIORITY": "6"})
	units := map[string]core.Engine{"qagent-mihomo.service": core.EngineMihomo}
	source := coreLogFileSource{engine: core.EngineMihomo, kind: "openrc", path: "/var/log/qagent/qagent-mihomo.log"}
	snapshot := func() (runtime.MemStats, int, int, uint64) {
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		descriptors, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		status, err := os.ReadFile("/proc/self/status")
		if err != nil {
			t.Fatal(err)
		}
		var rss uint64
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				rss, _ = strconv.ParseUint(strings.Fields(line)[1], 10, 64)
			}
		}
		return memory, runtime.NumGoroutine(), len(descriptors), rss
	}
	runtime.GC()
	before, goroutines, descriptors, rss := snapshot()
	var startCPU, endCPU syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &startCPU); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	sent, maxQueue, wireBytes := 0, 0, 0
	for tick := range seconds * 10 {
		for offset := 0; offset < rate/10; offset++ {
			index := tick*(rate/10) + offset
			child := children[index%sources]
			if parts[0] == "journal" {
				entry, _, ok := decodeJournalCoreLog(journal, units)
				if !ok {
					t.Fatal("decode")
				}
				child.appendJournal(entry, strconv.Itoa(index))
			} else {
				child.appendFileEntry(source, []byte(message))
			}
		}
		maxQueue = max(maxQueue, len(collector.queued))
		for batch := collector.NextBatch(); batch != nil; batch = collector.NextBatch() {
			retry := collector.NextBatch()
			if retry.ID != batch.ID || len(retry.Entries) != len(batch.Entries) {
				t.Fatal("retry changed pending batch")
			}
			payload, err := json.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			wireBytes += len(payload)
			sent += len(batch.Entries)
			if !collector.Acknowledge(batch.ID) {
				t.Fatal("ACK rejected")
			}
		}
		time.Sleep(max(time.Duration(0), time.Until(start.Add(time.Duration(tick+1)*100*time.Millisecond))))
	}
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &endCPU); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	after, endGoroutines, endDescriptors, endRSS := snapshot()
	runtime.GC()
	var recovered runtime.MemStats
	runtime.ReadMemStats(&recovered)
	if sent != rate*seconds || len(collector.queued) != 0 || collector.pending != nil || endGoroutines != goroutines || endDescriptors != descriptors {
		t.Fatalf("resource or record mismatch: sent=%d expected=%d", sent, rate*seconds)
	}
	cpu := time.Duration(endCPU.Utime.Nano() + endCPU.Stime.Nano() - startCPU.Utime.Nano() - startCPU.Stime.Nano())
	t.Logf("mode=%s records=%d seconds=%.3f process_cpu_ms=%.3f process_cpu_percent=%.3f allocated_bytes=%d allocs=%d gc=%d heap_after_gc=%d rss_kib=%d->%d goroutines=%d->%d fds=%d->%d queue_max=%d queue_end=%d json_bytes=%d", mode, sent, elapsed.Seconds(), float64(cpu)/float64(time.Millisecond), 100*cpu.Seconds()/elapsed.Seconds(), after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, after.NumGC-before.NumGC, recovered.HeapAlloc, rss, endRSS, goroutines, endGoroutines, descriptors, endDescriptors, maxQueue, len(collector.queued), wireBytes)
}
