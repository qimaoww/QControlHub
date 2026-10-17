package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// All listeners, packet capture and nft rules live in a disposable namespace.
// Optional exported snapshots can be settled against the real API fixture.
func TestSharedTrafficRealNetwork(t *testing.T) {
	if os.Getenv("QCH_SHARED_NETWORK") != "1" {
		t.Skip("QCH_SHARED_NETWORK=1 requires nft, tcpdump and network namespace privileges")
	}
	if os.Getenv("QCH_SHARED_NETWORK_CHILD") != "1" {
		command := exec.Command("unshare", "--net", os.Args[0], "-test.run=^TestSharedTrafficRealNetwork$", "-test.v")
		command.Env = append(os.Environ(), "QCH_SHARED_NETWORK_CHILD=1")
		output, err := command.CombinedOutput()
		t.Log(string(output))
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Fatalf("loopback %v %s", err, out)
	}
	dir := os.Getenv("QCH_SHARED_NETWORK_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	_, _, policies, _ := sharedTrafficFixture(t)
	if raw, err := os.ReadFile(filepath.Join(dir, "policies.json")); err == nil {
		if err := json.Unmarshal(raw, &policies); err != nil {
			t.Fatal(err)
		}
	}
	if len(policies) != 3 {
		t.Fatal("requires A's two ports and B's port")
	}
	for i := range policies {
		policies[i].SharedQuota.LimitBytes = 64 << 20
	}
	clock := time.Now().UTC()
	manager := &TrafficManager{statePath: filepath.Join(dir, "network-state.json"), backend: &nftBackend{nftPath: "/usr/sbin/nft", direct: true}, records: map[string]*trafficRecord{}, now: func() time.Time { return clock }}
	ctx := context.Background()
	if err := manager.SetPolicies(ctx, policies, policies[0].AgentID); err != nil {
		t.Fatal(err)
	}
	capture := exec.Command("tcpdump", "-B", "8192", "-U", "--immediate-mode", "-i", "lo", "-s", "0", "-w", filepath.Join(dir, "listener.pcap"), "tcp or udp")
	stderr, err := capture.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := capture.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	captureDone := make(chan struct{})
	var captureLog strings.Builder
	go func() {
		defer close(captureDone)
		scanner := bufio.NewScanner(stderr)
		once := sync.Once{}
		for scanner.Scan() {
			captureLog.WriteString(scanner.Text() + "\n")
			if strings.Contains(scanner.Text(), "listening on") {
				once.Do(func() { close(ready) })
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("packet capture did not start")
	}
	defer func() {
		_ = capture.Process.Signal(syscall.SIGINT)
		_ = capture.Wait()
		<-captureDone
		_ = os.WriteFile(filepath.Join(dir, "tcpdump.txt"), []byte(captureLog.String()), 0600)
	}()
	sample := func(name string) []core.PortTrafficUsage {
		t.Helper()
		clock = clock.Add(time.Second)
		if err := manager.collectLocked(ctx, false); err != nil {
			t.Fatal(err)
		}
		snapshot := manager.Snapshot()
		raw, _ := json.MarshalIndent(snapshot, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, name+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		for _, u := range snapshot {
			t.Logf("%s policy=%s upload=%d download=%d share_port=%d epoch=%s", name, u.PolicyID, u.ReceivedBytes, u.SentBytes, u.ShareUsedBytes, u.CounterEpoch)
		}
		return snapshot
	}
	byPort := func(snapshot []core.PortTrafficUsage, port int) core.PortTrafficUsage {
		for _, p := range policies {
			if p.Port == port {
				for _, u := range snapshot {
					if u.PolicyID == p.ID {
						return u
					}
				}
			}
		}
		t.Fatal("missing policy")
		return core.PortTrafficUsage{}
	}
	sample("phase-zero")
	udpTransfer(t, "udp4", policies[0].Port, 256, 1024)
	a := sample("phase-a")
	want := uint64(256 * (1024 + 28))
	if u := byPort(a, policies[0].Port); u.ReceivedBytes != want || u.SentBytes != want {
		t.Fatalf("UDP4 independent IP byte benchmark want=%d actual=%+v", want, u)
	}
	if u := byPort(a, policies[2].Port); u.UsedBytes != 0 {
		t.Fatalf("A charged B: %+v", u)
	}
	udpTransfer(t, "udp4", 24999, 64, 512)
	system := sample("phase-system")
	for i, u := range system {
		if u.UsedBytes != a[i].UsedBytes {
			t.Fatal("system/owner traffic charged recipient")
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); udpTransfer(t, "udp6", policies[0].Port, 128, 1024) }()
	go func() { defer wg.Done(); udpTransfer(t, "udp4", policies[2].Port, 128, 1024) }()
	wg.Wait()
	sample("phase-concurrent")
	tcpTransfer(t, "tcp4", policies[1].Port, 1<<20, 512<<10)
	tcpTransfer(t, "tcp6", policies[2].Port, 256<<10, 1<<20)
	before := sample("phase-tcp")
	// Restart the Agent manager from its actual checkpoint with live kernel counters.
	loaded, err := loadTrafficState(manager.statePath)
	if err != nil {
		t.Fatal(err)
	}
	manager = &TrafficManager{statePath: manager.statePath, backend: manager.backend, records: loaded.Records, now: func() time.Time { return clock }}
	if err := manager.SetPolicies(ctx, policies, policies[0].AgentID); err != nil {
		t.Fatal(err)
	}
	restarted := sample("phase-restart")
	for i, u := range restarted {
		if u.UsedBytes != before[i].UsedBytes {
			t.Fatal("restart rebilled existing bytes")
		}
	}
	if err := manager.collectLocked(ctx, true); err != nil {
		t.Fatal(err)
	}
	udpTransfer(t, "udp6", policies[1].Port, 64, 1024)
	after := sample("phase-rebuilt")
	if byPort(after, policies[1].Port).UsedBytes-byPort(before, policies[1].Port).UsedBytes != 2*64*(1024+48) {
		t.Fatal("rule rebuild lost or repeated UDP6 bytes")
	}
	// A's exhausted allowance blocks A's ports together; B remains usable.
	for i := range policies {
		q := *policies[i].SharedQuota
		if i < 2 {
			q.LimitBytes = 1
		}
		policies[i].SharedQuota = &q
	}
	if err := manager.SetPolicies(ctx, policies, policies[0].AgentID); err != nil {
		t.Fatal(err)
	}
	for _, p := range policies {
		u := byPort(manager.Snapshot(), p.Port)
		if u.Blocked != (p.Port != policies[2].Port) {
			t.Fatal("quota affected wrong allocation")
		}
	}
	udpTransfer(t, "udp4", policies[2].Port, 16, 1024)
	sample("phase-quota")
	for i := range policies {
		q := *policies[i].SharedQuota
		q.LimitBytes = 1 << 30
		policies[i].SharedQuota = &q
	}
	if err := manager.SetPolicies(ctx, policies, policies[0].AgentID); err != nil {
		t.Fatal(err)
	}
	for _, u := range manager.Snapshot() {
		if u.Blocked {
			t.Fatal("top-up did not unblock")
		}
	}
	sample("phase-final")
}

func udpTransfer(t *testing.T, network string, port, count, size int) {
	t.Helper()
	host := "127.0.0.1"
	if network == "udp6" {
		host = "::1"
	}
	listener, err := net.ListenPacket(network, net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		t.Error(err)
		return
	}
	defer listener.Close()
	connection, err := net.Dial(network, listener.LocalAddr().String())
	if err != nil {
		t.Error(err)
		return
	}
	defer connection.Close()
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, size)
		for range count {
			_ = listener.SetDeadline(time.Now().Add(3 * time.Second))
			n, remote, err := listener.ReadFrom(buf)
			if err != nil {
				done <- err
				return
			}
			if _, err = listener.WriteTo(buf[:n], remote); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	packet := make([]byte, size)
	for range count {
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err := connection.Write(packet); err != nil {
			t.Error(err)
			return
		}
		if _, err := io.ReadFull(connection, packet); err != nil {
			t.Error(err)
			return
		}
	}
	if err := <-done; err != nil {
		t.Error(err)
	}
}

func tcpTransfer(t *testing.T, network string, port, upload, download int) {
	t.Helper()
	host := "127.0.0.1"
	if network == "tcp6" {
		host = "::1"
	}
	listener, err := net.Listen(network, net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err = io.CopyN(io.Discard, conn, int64(upload)); err == nil {
			err = writeFixturePayload(conn, download, "d")
		}
		done <- err
	}()
	conn, err := net.Dial(network, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := writeFixturePayload(conn, upload, "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, conn, int64(download)); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // Include the FIN/ACK exchange in both independent counters.
}

// Small paced writes keep the capture reference at the same packet layer as
// netfilter, without changing host interface offload or routing settings.
func writeFixturePayload(conn net.Conn, size int, value string) error {
	block := []byte(strings.Repeat(value, 8192))
	for size > 0 {
		n, err := conn.Write(block[:min(len(block), size)])
		if err != nil {
			return err
		}
		size -= n
		time.Sleep(100 * time.Microsecond)
	}
	return nil
}
