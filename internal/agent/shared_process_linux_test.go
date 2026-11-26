package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestSharedProcessQuota(t *testing.T) {
	m, b, policies, now := sharedTrafficFixture(t)
	for i := range policies {
		policies[i].SharedQuota.ProcessMetering = true
	}
	m.processSource = func(context.Context, core.Engine, string) (nativeAccountingSnapshot, error) {
		return nativeAccountingSnapshot{Plan: serverconfig.AccountingPlan{Source: "process"}, GroupInode: 10}, nil
	}
	ctx := context.Background()
	if err := m.SetPolicies(ctx, policies, policies[0].AgentID); err != nil {
		t.Fatal(err)
	}
	a, other := m.records[policies[0].ID], m.records[policies[2].ID]
	name := processCounterPrefix(a) + a.Process.Epoch
	b.counters[name] = 85
	b.counters[trafficCounterName(a, "in", "tcp")] = 10
	*now = now.Add(time.Second)
	if err := m.collectLocked(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := sharedTrafficUsed(m.records, a.Policy.SharedQuota.ID); got != 95 || a.Blocked {
		t.Fatalf("pending share bytes %d, blocked %v", got, a.Blocked)
	}
	for i := range policies[:2] {
		policies[i].SharedQuota.UsedBytes = 95
		policies[i].SharedQuota.ProcessAck = &core.SharedProcessUsage{Epoch: a.Process.Epoch, Bytes: 85}
	}
	policies[0].SharedQuota.PortUsedBytes = 10
	if err := m.SetPolicies(ctx, policies, policies[0].AgentID); err != nil {
		t.Fatal(err)
	}
	b.counters[name] += 5
	*now = now.Add(time.Second)
	if err := m.collectLocked(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !m.records[policies[0].ID].Blocked || !m.records[policies[1].ID].Blocked || other.Blocked || b.counters["drop:"+name] != 2 {
		t.Fatal("shared limit did not close exactly this instance on both legs")
	}
	if err := m.authorizeSharedDeployment(ctx, policies[0].SharedQuota.ID, []core.PortTrafficEndpoint{{Engine: a.Policy.Engine, Port: a.Policy.Port}}, true); err == nil {
		t.Fatal("exhausted instance authorized")
	}
	if got := m.Snapshot()[0]; got.UsedBytes != 10 || got.ProcessUsage.Bytes != 90 {
		t.Fatalf("mixed listener and process usage: %+v", got)
	}
	for i := range policies[:2] {
		policies[i].SharedQuota.LimitBytes = 110
	}
	if err := m.SetPolicies(ctx, policies, policies[0].AgentID); err != nil {
		t.Fatal(err)
	}
	if err := m.authorizeSharedDeployment(ctx, policies[0].SharedQuota.ID, []core.PortTrafficEndpoint{{Engine: a.Policy.Engine, Port: a.Policy.Port}}, true); err != nil {
		t.Fatal(err)
	}
	good := m.processSource
	m.processSource = func(ctx context.Context, engine core.Engine, share string) (nativeAccountingSnapshot, error) {
		if share == a.Policy.SharedQuota.ID {
			return nativeAccountingSnapshot{}, errors.New("unverified process")
		}
		return good(ctx, engine, share)
	}
	if err := m.collectLocked(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !m.records[policies[0].ID].Blocked || m.Snapshot()[0].EnforcementAvailable || m.records[policies[2].ID].Blocked {
		t.Fatal("loss of attribution did not close this allocation")
	}
	m.processSource = good
	if err := m.collectLocked(ctx, false); err != nil {
		t.Fatal(err)
	}
	if m.records[policies[0].ID].Blocked || !m.Snapshot()[0].EnforcementAvailable {
		t.Fatal("healthy process scope failed to recover its quota")
	}

}

// In a disposable network namespace, use two real UDP processes in different
// cgroups. Neither sets SO_MARK. Only the assigned instance consumes quota.
func TestSharedProcessNetwork(t *testing.T) {
	if os.Getenv("QCH_SHARED_NETWORK") != "1" {
		t.Skip("requires isolated netns and writable cgroup v2")
	}
	if addr := os.Getenv("QCH_PROCESS_LISTENER"); addr != "" {
		conn, err := net.ListenPacket(os.Getenv("QCH_PROCESS_FAMILY"), addr)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		fmt.Println("READY")
		conn.SetDeadline(time.Now().Add(4 * time.Second))
		var b [32]byte
		n, peer, err := conn.ReadFrom(b[:])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = conn.WriteTo(b[:n], peer); err != nil {
			t.Fatal(err)
		}
		return
	}
	if os.Getenv("QCH_PROCESS_PACKET") != "" {
		conn, err := net.Dial(os.Getenv("QCH_PROCESS_FAMILY"), os.Getenv("QCH_PROCESS_PACKET"))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
		_, err = conn.Write([]byte("qch-process"))
		if os.Getenv("QCH_PROCESS_BLOCKED") != "" && errors.Is(err, syscall.EPERM) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		var data [32]byte
		_, err = conn.Read(data[:])
		if (err == nil) != (os.Getenv("QCH_PROCESS_BLOCKED") == "") {
			t.Fatalf("UDP reply: %v", err)
		}
		return
	}
	family := os.Getenv("QCH_PROCESS_FAMILY")
	if family == "" {
		for _, network := range []string{"udp4", "udp6"} {
			t.Run(network, func(t *testing.T) {
				child := exec.Command("unshare", "--net", os.Args[0], "-test.run=^TestSharedProcessNetwork$", "-test.v")
				child.Env = append(os.Environ(), "QCH_PROCESS_FAMILY="+network)
				if out, err := child.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v", out, err)
				}
			})
		}
		return
	}
	host, overhead := "127.0.0.1", 28
	if family == "udp6" {
		host, overhead = "::1", 48
	}
	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	socket, err := net.ListenPacket(family, net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	go func() {
		var b [64]byte
		for {
			n, peer, e := socket.ReadFrom(b[:])
			if e != nil {
				return
			}
			socket.WriteTo(b[:n], peer)
		}
	}()
	m, _, policies, _ := sharedTrafficFixture(t)
	policies = policies[:2]
	suffix, err := randomSuffix(8)
	if err != nil {
		t.Fatal(err)
	}
	share := "shr_" + suffix
	group := filepath.Join("/sys/fs/cgroup", processSliceName(policies[0].Engine, share))
	if err = os.Mkdir(group, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(group)
	for i := range policies {
		policies[i].SharedQuota.ID = share
		policies[i].SharedQuota.ProcessMetering = true
		policies[i].SharedQuota.LimitBytes = uint64(3 * (overhead + len("qch-process")))
	}
	m.processSource = func(context.Context, core.Engine, string) (nativeAccountingSnapshot, error) {
		return nativeAccountingSnapshot{Plan: serverconfig.AccountingPlan{Source: "process"}, GroupInode: 1}, nil
	}
	m.backend = &nftBackend{nftPath: "/usr/sbin/nft", direct: true}
	ctx := context.Background()
	if err = m.SetPolicies(ctx, policies, policies[0].AgentID); err != nil {
		t.Fatal(err)
	}
	// Model a host firewall which restores its routing mark from conntrack.
	// Requests must retain the operator's mark after the accounting hook.
	targetPort := socket.LocalAddr().(*net.UDPAddr).Port
	firewall := exec.Command("nft", "-f", "-")
	firewall.Stdin = strings.NewReader(fmt.Sprintf(`add table inet hostpolicy
add chain inet hostpolicy before { type filter hook output priority -20; policy accept; }
add chain inet hostpolicy after { type filter hook output priority 0; policy accept; }
add rule inet hostpolicy before udp dport %d ct mark set 42
add rule inet hostpolicy after udp dport %d ct mark != 42 drop
`, targetPort, targetPort))
	if out, err := firewall.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	send := func(owned, blocked bool) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestSharedProcessNetwork$")
		if owned {
			cmd = exec.Command("sh", "-ec", `echo $$ > "$1/cgroup.procs"; shift; exec "$@"`, "sh", group, os.Args[0], "-test.run=^TestSharedProcessNetwork$")
		}
		cmd.Env = append(os.Environ(), "QCH_PROCESS_PACKET="+socket.LocalAddr().String())
		if blocked {
			cmd.Env = append(cmd.Env, "QCH_PROCESS_BLOCKED=1")
		}
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("%s: %v", out, e)
		}
		if err = m.collectLocked(ctx, false); err != nil {
			t.Fatal(err)
		}
	}
	send(false, false)
	send(true, false)
	expected := uint64(2 * (overhead + len("qch-process")))
	if got := sharedTrafficUsed(m.records, share); got != expected {
		t.Fatalf("process IP bytes %d, want %d", got, expected)
	}
	addr := net.JoinHostPort(host, fmt.Sprint(policies[0].Port))
	listener := exec.Command("sh", "-ec", `echo $$ > "$1/cgroup.procs"; shift; exec "$@"`, "sh", group, os.Args[0], "-test.run=^TestSharedProcessNetwork$")
	listener.Env = append(os.Environ(), fmt.Sprintf("QCH_PROCESS_LISTENER=:%d", policies[0].Port))
	ready, err := listener.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = listener.Start(); err != nil {
		t.Fatal(err)
	}
	defer listener.Process.Kill()
	if line, _ := bufio.NewReader(ready).ReadString('\n'); line != "READY\n" {
		t.Fatalf("listener startup: %q", line)
	}
	client := exec.Command(os.Args[0], "-test.run=^TestSharedProcessNetwork$")
	client.Env = append(os.Environ(), "QCH_PROCESS_PACKET="+addr)
	if out, err := client.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	if err = listener.Wait(); err != nil {
		t.Fatal(err)
	}
	if err = m.collectLocked(ctx, false); err != nil {
		t.Fatal(err)
	}
	if usage := m.Snapshot()[0]; usage.UsedBytes != expected || usage.ProcessUsage.Bytes != expected {
		t.Fatalf("listener or target counted twice: %+v", usage)
	}
	send(true, true)
	send(false, false)
	if got := sharedTrafficUsed(m.records, share); got != 2*expected {
		t.Fatalf("drop changed bill: %d", got)
	}
}

func TestProcessCounterRecoveryUsesLiveObjectAndServerHighWater(t *testing.T) {
	_, _, p, _ := sharedTrafficFixture(t)
	r := &trafficRecord{Policy: p[0]}
	epoch := strings.Repeat("b", 32)
	ack := core.SharedProcessUsage{Epoch: epoch, Bytes: 123}
	r.Policy.SharedQuota.ProcessAck = &ack
	process, err := sampleProcessCounter(r, processCheckpoint{}, nil, 1)
	if err != nil || process.Bytes != 123 || process.Raw != 123 || process.Epoch != epoch {
		t.Fatalf("%+v %v", process, err)
	}
	name := processCounterPrefix(r) + epoch
	counters := map[string]uint64{name: 150, "handle:" + name: 2}
	process, err = sampleProcessCounter(r, process, counters, 1)
	if err != nil || process.Bytes != 150 {
		t.Fatalf("recreated object overbilled: %+v %v", process, err)
	}
	counters["handle:"+name] = 3
	restored, err := sampleProcessCounter(r, process, counters, 1)
	if err != nil || restored.Bytes != 150 {
		t.Fatalf("restored counter billed again: %+v %v", restored, err)
	}
	counters[name] = 140
	restored, err = sampleProcessCounter(r, restored, counters, 1)
	if err != nil || restored.Bytes != 150 || restored.Raw != 140 {
		t.Fatalf("older nft snapshot billed again: %+v %v", restored, err)
	}
	ack.Bytes = 200
	restored, err = sampleProcessCounter(r, restored, counters, 1)
	if err != nil || restored.Bytes != 200 {
		t.Fatalf("late acknowledgement: %+v %v", restored, err)
	}
	counters[name]++
	restored, err = sampleProcessCounter(r, restored, counters, 1)
	if err != nil || restored.Bytes != 201 {
		t.Fatalf("lost first byte after reconciliation: %+v %v", restored, err)
	}
	counters[name], ack.Bytes = 150, 123
	for _, old := range []processCheckpoint{{}, {SharedProcessUsage: core.SharedProcessUsage{Epoch: strings.Repeat("a", 32), Bytes: 400}}} {
		got, err := sampleProcessCounter(r, old, counters, 1)
		if err != nil || got.Bytes != 150 || got.Epoch != epoch {
			t.Fatalf("checkpoint restore: %+v %v", got, err)
		}
	}
}
