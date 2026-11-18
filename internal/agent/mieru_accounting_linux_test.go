package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// A second nft table selects flows by known ports, independently of Mihomo's
// socket mark and the Agent's conntrack mark. Compare complete IP packets.
func TestMieruMarkedNetworkNamespace(t *testing.T) {
	if os.Getenv("QCH_TEST_NFTABLES") != "1" || os.Getenv("QCH_LIVE_CORE_ROOT") == "" {
		t.Skip("requires isolated nftables and a live Mihomo binary")
	}
	if os.Getenv("QCH_MIERU_METER_CHILD") == "" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "unshare", "--net", os.Args[0], "-test.run=^TestMieruMarkedNetworkNamespace$", "-test.v")
		cmd.Env = append(os.Environ(), "QCH_MIERU_METER_CHILD=1")
		out, err := cmd.CombinedOutput()
		t.Log(string(out))
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	run := func(input, cmd string, args ...string) []byte {
		t.Helper()
		c := exec.Command(cmd, args...)
		c.Stdin = strings.NewReader(input)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %s: %v", cmd, out, err)
		}
		return out
	}
	run("", "ip", "link", "set", "lo", "up")
	run("", "ip", "addr", "add", "192.0.2.2/32", "dev", "lo")
	target, _ := mieruUDPBackend(t, "udp4", "192.0.2.2:0", func(b []byte) []byte { return make([]byte, int(b[0])*100+1) })
	binary := filepath.Join(os.Getenv("QCH_LIVE_CORE_ROOT"), "bin", "mihomo")
	manager, _, now, policy := newAccuracyTrafficManager(t)
	policy.Port, policy.Engine, policy.Protocol = availableTCPPort(t), core.EngineMihomo, core.TrafficProtocolTCP
	manager.backend = &nftBackend{nftPath: "/usr/sbin/nft", direct: true}
	content := fmt.Sprintf("mode: rule\nlisteners:\n  - {name: edge, type: mieru, port: %d, transport: TCP, users: {test: test}}\nrules: ['MATCH,DIRECT']\n", policy.Port)
	e := &Executor{}
	content, warning := e.prepareNativeAccountingContent(context.Background(), policy.Engine, EngineSpec{}, content)
	if warning != "" {
		t.Fatal(warning)
	}
	snapshot, err := compileAccountingConfiguration(policy.Engine, content)
	if err != nil {
		t.Fatal(err)
	}
	server := startMihomoForTrafficTest(t, binary, t.TempDir(), "config.yaml", content, policy.Port)
	defer server.stop(t)
	clientPort := availableTCPPort(t)
	clientConfig, err := yaml.Marshal(map[string]any{"mixed-port": clientPort, "mode": "rule", "proxies": []any{map[string]any{"name": "edge", "type": "mieru", "server": "127.0.0.1", "port": policy.Port, "username": "test", "password": "test", "transport": "TCP", "udp": true}}, "rules": []string{"MATCH,edge"}})
	if err != nil {
		t.Fatal(err)
	}
	client := startMihomoForTrafficTest(t, binary, t.TempDir(), "config.yaml", string(clientConfig), clientPort)
	defer client.stop(t)
	rules := "add table inet witness\nadd chain inet witness observe { type filter hook output priority 0; }\n"
	for _, leg := range []struct {
		name, match string
		port        int
	}{{"client_rx", "tcp dport", policy.Port}, {"client_tx", "tcp sport", policy.Port}, {"target_rx", "udp sport", target.Port}, {"target_tx", "udp dport", target.Port}} {
		rules += fmt.Sprintf("add counter inet witness %s\nadd rule inet witness observe %s %d counter name %s\n", leg.name, leg.match, leg.port, leg.name)
	}
	run(rules, "nft", "-f", "-")
	witness := func() map[string]uint64 {
		var result struct {
			NFTables []struct {
				Counter struct {
					Name  string
					Bytes uint64
				}
			}
		}
		if err := json.Unmarshal(run("", "nft", "-j", "list", "counters", "inet", "witness"), &result); err != nil {
			t.Fatal(err)
		}
		m := map[string]uint64{}
		for _, v := range result.NFTables {
			if v.Counter.Name != "" {
				m[v.Counter.Name] = v.Counter.Bytes
			}
		}
		if len(m) != 4 {
			t.Fatal(m)
		}
		return m
	}
	ctx := context.Background()
	if err := manager.SetPolicies(ctx, []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	association := mieruSOCKSUDP(t, clientPort)
	for i, phase := range []struct {
		name             string
		upload, download int
	}{{"listener", 800, 1}, {"upload", 1100, 1}, {"download", 30, 11}, {"both", 850, 8}} {
		if i == 1 {
			manager.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) { return snapshot, nil }
			manager.collect(ctx, false)
			if got := manager.Snapshot()[0]; got.Accounting == nil || got.Accounting.Source != "nft-dual" {
				t.Fatalf("dual source unavailable: %+v", got)
			}
		}
		time.Sleep(100 * time.Millisecond)
		*now = now.Add(time.Second)
		manager.collect(ctx, false)
		initial := manager.Snapshot()[0]
		before := witness()
		for range 5 {
			payload := bytes.Repeat([]byte{byte(phase.download)}, phase.upload)
			reply, err := association.exchange(target, payload, 3*time.Second)
			if err != nil || len(reply) != phase.download*100+1 {
				t.Fatalf("%s exchange: %d %v", phase.name, len(reply), err)
			}
		}
		time.Sleep(150 * time.Millisecond)
		*now = now.Add(time.Second)
		manager.collect(ctx, false)
		after := witness()
		for k := range after {
			after[k] -= before[k]
		}
		got := manager.Snapshot()[0]
		rx, tx := got.ReceivedBytes-initial.ReceivedBytes, got.SentBytes-initial.SentBytes
		t.Logf("%s client=%d/%d target=%d/%d agent=%d/%d", phase.name, after["client_rx"], after["client_tx"], after["target_rx"], after["target_tx"], rx, tx)
		wantRX, wantTX := after["client_rx"], after["client_tx"]
		if i > 0 {
			wantRX += after["target_rx"]
			wantTX += after["target_tx"]
		}
		if rx != wantRX || tx != wantTX || after["target_tx"] == 0 || after["target_rx"] == 0 {
			t.Fatal("independent byte counts disagree")
		}
	}
}
