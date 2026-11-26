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
}

// In a disposable network namespace, use two real UDP processes in different
// cgroups. Neither sets SO_MARK. Only the assigned instance consumes quota.
func TestSharedProcessNetwork(t *testing.T) {
	if os.Getenv("QCH_SHARED_NETWORK") != "1" {
		t.Skip("requires isolated netns and writable cgroup v2")
	}
	if addr := os.Getenv("QCH_PROCESS_LISTENER"); addr != "" {
		conn, err := net.ListenPacket("udp4", addr)
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
		conn, err := net.Dial("udp4", os.Getenv("QCH_PROCESS_PACKET"))
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
	if os.Getenv("QCH_PROCESS_NETNS") == "" {
		child := exec.Command("unshare", "--net", os.Args[0], "-test.run=^TestSharedProcessNetwork$", "-test.v")
		child.Env = append(os.Environ(), "QCH_PROCESS_NETNS=1")
		if out, err := child.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
		return
	}
	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
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
		policies[i].SharedQuota.LimitBytes = 100
	}
	m.processSource = func(context.Context, core.Engine, string) (nativeAccountingSnapshot, error) {
		return nativeAccountingSnapshot{Plan: serverconfig.AccountingPlan{Source: "process"}, GroupInode: 1}, nil
	}
	m.backend = &nftBackend{nftPath: "/usr/sbin/nft", direct: true}
	ctx := context.Background()
	if err = m.SetPolicies(ctx, policies, policies[0].AgentID); err != nil {
		t.Fatal(err)
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
	expected := uint64(2 * (28 + len("qch-process")))
	if got := sharedTrafficUsed(m.records, share); got != expected {
		t.Fatalf("process IP bytes %d, want %d", got, expected)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", policies[0].Port)
	listener := exec.Command("sh", "-ec", `echo $$ > "$1/cgroup.procs"; shift; exec "$@"`, "sh", group, os.Args[0], "-test.run=^TestSharedProcessNetwork$")
	listener.Env = append(os.Environ(), "QCH_PROCESS_LISTENER="+addr)
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
	for _, old := range []processCheckpoint{{}, {SharedProcessUsage: core.SharedProcessUsage{Epoch: strings.Repeat("a", 32), Bytes: 400}}} {
		got, err := sampleProcessCounter(r, old, counters, 1)
		if err != nil || got.Bytes != 150 || got.Epoch != epoch {
			t.Fatalf("checkpoint restore: %+v %v", got, err)
		}
	}
}
