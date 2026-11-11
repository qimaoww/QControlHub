//go:build linux

package networkdiag

import (
	"context"
	"encoding/json"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestBoundedServerAndDurableReplay(t *testing.T) {
	if _, err := exec.LookPath("iperf3"); err != nil {
		t.Skip("iperf3 unavailable")
	}
	cap := Capabilities(context.Background())
	if cap.Tools["iperf3"].Error != "" || len(cap.Addresses) == 0 {
		t.Skip("compatible iperf3 and node interface required")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(cap.Addresses[0], "0"))
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	p := core.DiagnosticParameters{Tool: "iperf3", Protocol: "tcp", Provider: "disable-geoip", Count: 2, IntervalMS: 200, TimeoutMS: 200, MaxHops: 3, Size: 64, Port: port, Duration: 1, Streams: 1, Bandwidth: 1000000}
	c := core.DiagnosticCommand{ID: "ndg_local-server", SessionID: "ndg_local", Role: "server", Source: core.DiagnosticEndpoint{Address: cap.Addresses[0]}, Target: core.DiagnosticEndpoint{Address: cap.Addresses[0]}, Parameters: p, Deadline: time.Now().Add(30 * time.Second)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "journal")
	life := &sync.Mutex{}
	m, err := New(ctx, path, life)
	if err != nil {
		t.Fatal(err)
	}
	m.Apply([]core.DiagnosticCommand{c})
	wait := func(terminal bool) core.DiagnosticResult {
		t.Helper()
		until := time.Now().Add(15 * time.Second)
		for time.Now().Before(until) {
			r := m.Apply([]core.DiagnosticCommand{c})[0]
			if r.Terminal() == terminal && (terminal || r.State == "ready") {
				return r
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("server did not advance")
		return core.DiagnosticResult{}
	}
	r := wait(false)
	if !life.TryLock() {
		t.Fatal("ready server monopolized lifecycle mutex")
	}
	life.Unlock()
	// A second Agent cannot bypass admission against the same persisted identity.
	if other, e := New(ctx, path, life); e == nil {
		other.lock.Close()
		t.Fatal("duplicate Agent acquired journal")
	}
	c.Role = "stop"
	m.Apply([]core.DiagnosticCommand{c})
	r = wait(true)
	if !r.Cleaned || r.State != "canceled" {
		t.Fatalf("stop did not reap child: %+v", r)
	}
	c.Role = "server"
	if replay := m.Apply([]core.DiagnosticCommand{c})[0]; replay.State != r.State {
		t.Fatal("duplicate dispatch restarted server")
	}
	l, e := net.Listen("tcp", net.JoinHostPort(c.Source.Address, strings.Split(listener.Addr().String(), ":")[len(strings.Split(listener.Addr().String(), ":"))-1]))
	if e != nil {
		t.Fatal("server port not released", e)
	}
	l.Close()
	b, e := os.ReadFile(path)
	if e != nil || !json.Valid(b) {
		t.Fatal("terminal result was not durable", e)
	}
}

func TestCommandValidation(t *testing.T) {
	p := core.DiagnosticParameters{Tool: "mtr", Protocol: "tcp", Provider: "disable-geoip", Count: 2, IntervalMS: 200, TimeoutMS: 200, MaxHops: 3, Size: 64, Port: 443, Duration: 1, Streams: 1, Bandwidth: 1000}
	c := core.DiagnosticCommand{Role: "probe", Source: core.DiagnosticEndpoint{Address: "2001:db8::1"}, Target: core.DiagnosticEndpoint{Address: "2001:db8::2"}, Parameters: p}
	_, args, e := Args(c)
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(args, " ")
	for _, expected := range []string{"--mtr --report", "--tcp", "--port 443", "--psize 64", "--data-provider disable-geoip"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("missing %s: %s", expected, joined)
		}
	}
	for _, target := range []string{"--help", "192.0.2.1\n--tcp", "127.0.0.1", "fe80::1%eth0"} {
		c.Target.Address = target
		if _, _, err := Args(c); err == nil {
			t.Errorf("accepted target %q", target)
		}
	}
	c.Target.Address = "2001:db8::2"
	c.Parameters.Size = 63
	if _, _, err := Args(c); err == nil {
		t.Fatal("accepted undersized IPv6 TCP packet")
	}
}

func TestRestartReclaimsOnlyJournaledProcess(t *testing.T) {
	if _, err := exec.LookPath("iperf3"); err != nil {
		t.Skip("iperf3 unavailable")
	}
	start := func() *exec.Cmd {
		t.Helper()
		cmd := exec.Command("iperf3", "--server", "--port", "0")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if e := cmd.Start(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd
	}
	owned, other := start(), start()
	c := core.DiagnosticCommand{ID: "ndg_recovery-server", SessionID: "ndg_recovery", Role: "server"}
	path := filepath.Join(t.TempDir(), "journal")
	entries := map[string]*entry{c.ID: {PID: owned.Process.Pid, ProcessStart: processStart(owned.Process.Pid), BootID: bootID(), Command: c, Result: core.DiagnosticResult{ID: c.ID, State: "ready"}}}
	b, _ := json.Marshal(entries)
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, e := New(ctx, path, &sync.Mutex{})
	if e != nil {
		t.Fatal(e)
	}
	r := m.Apply([]core.DiagnosticCommand{c})[0]
	if !r.Cleaned || r.State != "failed" {
		t.Fatalf("restart did not confirm reclaim: %+v", r)
	}
	if e := other.Process.Signal(syscall.Signal(0)); e != nil {
		t.Fatal("unrelated server was killed", e)
	}
}

func TestJournalRejectsForeignOrUnsafeState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journal")
	target := filepath.Join(dir, "other")
	if e := os.WriteFile(target, []byte("{}"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(target, path); e != nil {
		t.Fatal(e)
	}
	if _, e := New(context.Background(), path, &sync.Mutex{}); e == nil {
		t.Fatal("accepted symlinked process journal")
	}
}

func TestIdleServerExpiresAndKeepsUnrelatedListener(t *testing.T) {
	if _, e := exec.LookPath("iperf3"); e != nil {
		t.Skip("iperf3 unavailable")
	}
	caps := Capabilities(context.Background())
	if caps.Tools["iperf3"].Error != "" || len(caps.Addresses) == 0 {
		t.Skip("compatible iperf3/interface required")
	}
	listener, e := net.Listen("tcp", net.JoinHostPort(caps.Addresses[0], "0"))
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	unrelated, e := net.Listen("tcp", net.JoinHostPort(caps.Addresses[0], "0"))
	if e != nil {
		t.Fatal(e)
	}
	defer unrelated.Close()
	p := core.DiagnosticParameters{Tool: "iperf3", Protocol: "tcp", Provider: "disable-geoip", Count: 1, IntervalMS: 200, TimeoutMS: 200, MaxHops: 1, Size: 64, Port: port, Duration: 1, Streams: 1, Bandwidth: 1000000}
	c := core.DiagnosticCommand{ID: "ndg_idle-server", Role: "server", Source: core.DiagnosticEndpoint{Address: caps.Addresses[0]}, Target: core.DiagnosticEndpoint{Address: caps.Addresses[0]}, Parameters: p}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ready := false
	r := runProcess(ctx, c, func() { ready = true }, func(int) error { return nil })
	if !ready || !r.Cleaned || !r.Terminal() {
		t.Fatalf("idle server: %+v ready=%v", r, ready)
	}
	released, e := net.Listen("tcp", net.JoinHostPort(c.Source.Address, strconv.Itoa(port)))
	if e != nil {
		t.Fatal("idle server leaked listener", e)
	}
	released.Close()
	if conn, e := net.DialTimeout("tcp", unrelated.Addr().String(), time.Second); e != nil {
		t.Fatal("unrelated listener closed", e)
	} else {
		conn.Close()
	}
}

func TestProviderReadinessAndSecretRedaction(t *testing.T) {
	t.Setenv("IPDBONE_API_ID", "")
	t.Setenv("IPDBONE_API_KEY", "")
	t.Setenv("NEXTTRACE_IPINFOLOCALPATH", filepath.Join(t.TempDir(), "missing.mmdb"))
	t.Setenv("NEXTTRACE_CHUNZHENURL", "http://127.0.0.1:1")
	c := Capabilities(context.Background())
	if c.Providers["IPInfo"] != "" || c.Providers["IPInsight"] != "" || c.Providers["ipdb.one"] == "" || c.Providers["IPInfoLocal"] == "" || c.Providers["chunzhen"] == "" {
		t.Fatalf("incorrect preparation: %+v", c.Providers)
	}
	t.Setenv("IPDBONE_API_ID", "task-provider-id")
	t.Setenv("IPDBONE_API_KEY", "task-provider-secret")
	if got := redactProviderSecrets(map[string]any{"message": "error task-provider-secret task-provider-id"}).(map[string]any)["message"]; got != "error [redacted] [redacted]" {
		t.Fatalf("credential escaped: %s", got)
	}
	p := core.DiagnosticParameters{Tool: "traceroute", Protocol: "icmp", Provider: "DN42", Count: 1, IntervalMS: 200, TimeoutMS: 200, MaxHops: 2, Size: 64, Port: 443, Duration: 1, Streams: 1, Bandwidth: 1000}
	_, args, e := Args(core.DiagnosticCommand{Role: "probe", Source: core.DiagnosticEndpoint{Address: "192.0.2.1"}, Target: core.DiagnosticEndpoint{Address: "192.0.2.2"}, Parameters: p})
	if e != nil || !strings.Contains(strings.Join(args, " "), "--data-provider DN42") || slices.Contains(args, "disable-geoip") {
		t.Fatalf("DN42 silently replaced: %v %v", args, e)
	}
}

func TestUnconfirmedCleanupBlocksNewWork(t *testing.T) {
	caps := Capabilities(context.Background())
	if len(caps.Addresses) == 0 {
		t.Skip("node interface required")
	}
	old := core.DiagnosticCommand{ID: "ndg_uncertain-server", SessionID: "ndg_uncertain", Role: "server"}
	path := filepath.Join(t.TempDir(), "journal")
	data, _ := json.Marshal(map[string]*entry{old.ID: {Command: old, PID: os.Getpid(), ProcessStart: processStart(os.Getpid()), Result: core.DiagnosticResult{ID: old.ID, State: "failed", Cleaned: false}}})
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, e := New(ctx, path, &sync.Mutex{})
	if e != nil {
		t.Fatal(e)
	}
	p := core.DiagnosticParameters{Tool: "ping", Protocol: "icmp", Provider: "disable-geoip", Count: 1, IntervalMS: 200, TimeoutMS: 200, MaxHops: 1, Size: 64, Port: 443, Duration: 1, Streams: 1, Bandwidth: 1000}
	c := core.DiagnosticCommand{ID: "ndg_next-forward", SessionID: "ndg_next", Role: "probe", Source: core.DiagnosticEndpoint{Address: caps.Addresses[0]}, Target: core.DiagnosticEndpoint{Address: "198.51.100.2"}, Parameters: p, Deadline: time.Now().Add(time.Minute)}
	r := m.Apply([]core.DiagnosticCommand{c})[0]
	if r.State != "failed" || !strings.Contains(r.Error, "其他网络诊断") || !m.Active() {
		t.Fatalf("uncertain process ownership did not block new work: %+v", r)
	}
}
