package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Opt-in sandbox: these are two actual Agent processes in an internal Docker
// network, not WSS simulators or command-construction assertions.
func TestNetworkDiagnosticsRealAgents(t *testing.T) {
	if os.Getenv("QCH_NETWORK_DIAGNOSTICS_REAL") != "1" {
		t.Skip("requires built qagent/NextTrace sandbox image and Docker network administration")
	}
	db, ctx, admin, alice, bob := newConfigScopeAPIFixtureWithin(t, 15*time.Minute)
	run := func(args ...string) string {
		t.Helper()
		b, e := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if e != nil {
			t.Fatalf("docker %s: %v: %s", args[0], e, b)
		}
		return strings.TrimSpace(string(b))
	}
	network := "qch-network-tools-20261112-net"
	run("network", "create", "--internal", "--ipv6", "--subnet", "172.29.78.0/24", "--subnet", "fd42:7163:7800::/64", network)
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", network).Run() })
	artifact := os.Getenv("QCH_NETDIAG_ARTIFACT_DIR")
	if artifact == "" {
		artifact = t.TempDir()
	}
	var geoCalls atomic.Int32
	var geoFail atomic.Bool
	mux := http.NewServeMux()
	// Controlled provider fixture supplies only metadata. Every packet and
	// measurement below still travels through the real isolated Agent path.
	mux.HandleFunc("/test-geolocation", func(w http.ResponseWriter, r *http.Request) {
		ip := r.URL.Query().Get("ip")
		if ip != "1.1.1.1" {
			geoCalls.Add(1)
		}
		if geoFail.Load() {
			http.Error(w, "provider rate limit", http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{ip: map[string]string{"country": "TestCountry", "area": "TestCity", "asn": "64512"}})
	})
	mux.Handle("/api/", admin.handler)
	mux.Handle("/agent/", admin.handler)
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("../../frontend"))))
	mux.Handle("/", http.FileServer(http.Dir("../../frontend")))
	server := httptest.NewUnstartedServer(mux)
	listener, e := net.Listen("tcp", "0.0.0.0:0")
	if e != nil {
		t.Fatal(e)
	}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	port := listener.Addr().(*net.TCPAddr).Port
	base := fmt.Sprintf("http://172.29.78.1:%d", port)
	local := fmt.Sprintf("http://127.0.0.1:%d", port)
	image := os.Getenv("QCH_NETDIAG_IMAGE")
	if image == "" {
		image = "qch-network-tools:20261112"
	}
	names := []string{"qch-network-tools-20261112-a", "qch-network-tools-20261112-b"}
	// A pre-existing unrelated iperf server must survive all task cleanup.
	for i, name := range names {
		var credential core.EnrollmentTokenCreated
		admin.call("POST", "/enrollment-tokens", map[string]string{"name": name}, 201, &credential)
		state := filepath.Join(artifact, name)
		if e = os.MkdirAll(state, 0700); e != nil {
			t.Fatal(e)
		}
		run("run", "-d", "--name", name, "--label", "qch.task=network-tools", "--network", network, "--ip", fmt.Sprintf("172.29.78.%d", i+2), "--ip6", fmt.Sprintf("fd42:7163:7800::%d", i+2), "--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW", "--memory", "256m", "-v", state+":/state", "-e", "QCH_AGENT_STATE=/state/agent.json", "-e", "QCH_AGENT_NAME="+name, "-e", "QCH_SERVER_URL="+base, "-e", "QCH_ENROLLMENT_TOKEN="+credential.Token, "-e", "QCH_ALLOW_HTTP=true", "-e", "QCH_ALLOW_INSECURE_LIVE=true", "-e", "QCH_HEARTBEAT_INTERVAL=1s", "-e", "QCH_AGENT_ENGINES=mihomo", "-e", "NEXTTRACE_CHUNZHENURL="+base+"/test-geolocation", image)
		t.Cleanup(func() {
			logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
			_ = os.WriteFile(filepath.Join(artifact, name+".log"), logs, 0600)
			_ = exec.Command("docker", "rm", "-f", name).Run()
		})
	}
	var nodes []core.DiagnosticNode
	until := time.Now().Add(45 * time.Second)
	for time.Now().Before(until) {
		admin.call("GET", "/network-diagnostics/nodes", nil, 200, &nodes)
		if len(nodes) == 2 && nodes[0].Online && nodes[1].Online {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if len(nodes) != 2 {
		for _, name := range names {
			t.Log(run("logs", name))
		}
		t.Fatalf("two agents did not connect: %+v", nodes)
	}
	byName := map[string]core.DiagnosticNode{}
	for _, n := range nodes {
		byName[n.Name] = n
	}
	node1, node2 := byName[names[0]], byName[names[1]]
	if node1.AgentID == "" || node2.AgentID == "" {
		t.Fatalf("unexpected nodes: %+v", nodes)
	}
	for _, n := range nodes {
		agent := core.Agent{ID: n.AgentID, Capabilities: []core.Engine{core.EngineMihomo}}
		grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 5201)
		grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 5202)
	}
	access := acceptConfigScopeAPIInvitations(alice)
	for _, sh := range access.Shares {
		admin.call("PUT", "/agents/"+sh.AgentID+"/diagnostic-grants/"+sh.ID, map[string]bool{"enabled": true}, 204, nil)
	}
	alice.call("GET", "/network-diagnostics/nodes", nil, 200, &nodes)
	byID := map[string]core.DiagnosticNode{}
	for _, n := range nodes {
		byID[n.AgentID] = n
	}
	makeRequest := func(tool, protocol string, family int) core.DiagnosticRequest {
		a, b := byID[node1.AgentID], byID[node2.AgentID]
		address := func(n core.DiagnosticNode) string {
			for _, s := range n.Capabilities.Addresses {
				if strings.Contains(s, ":") == (family == 6) {
					return s
				}
			}
			t.Fatalf("missing IPv%d on %s", family, n.Name)
			return ""
		}
		p := diagnosticTestParameters()
		p.Tool = tool
		p.Protocol = protocol
		p.TimeoutMS = 500
		p.MaxHops = 3
		p.Duration = 2
		p.Size = 64
		if tool == "iperf3" && protocol == "udp" {
			p.Size = 1200
		}
		return core.DiagnosticRequest{RequestID: fmt.Sprintf("%s-%s-%d-%d", tool, protocol, family, time.Now().UnixNano()), Node1: core.DiagnosticEndpoint{AgentID: a.AgentID, InstanceID: a.InstanceID, Address: address(a)}, Node2: core.DiagnosticEndpoint{AgentID: b.AgentID, InstanceID: b.InstanceID, Address: address(b)}, Parameters: p, Bidirectional: tool == "mtr" || tool == "traceroute"}
	}
	wait := func(t *testing.T, id string) core.DiagnosticSession {
		t.Helper()
		deadline := time.Now().Add(40 * time.Second)
		var d core.DiagnosticSession
		for time.Now().Before(deadline) {
			alice.call("GET", "/network-diagnostics/"+id, nil, 200, &d)
			if d.State == "succeeded" || d.State == "failed" || d.State == "canceled" {
				return d
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("measurement stuck: %+v", d)
		return d
	}
	waitClient := func(t *testing.T, id string) {
		t.Helper()
		until := time.Now().Add(20 * time.Second)
		for time.Now().Before(until) {
			var d core.DiagnosticSession
			alice.call("GET", "/network-diagnostics/"+id, nil, 200, &d)
			if d.Results[id+"-client"].State == "running" {
				time.Sleep(time.Second)
				return
			}
			if d.State == "failed" {
				t.Fatalf("client could not start: %+v", d)
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("client did not start")
	}
	run("exec", "-d", names[1], "iperf3", "-s", "-p", "5300")
	for _, family := range []int{4, 6} {
		cases := [][2]string{{"ping", "icmp"}, {"traceroute", "icmp"}, {"traceroute", "tcp"}, {"traceroute", "udp"}, {"mtr", "icmp"}, {"mtr", "tcp"}, {"mtr", "udp"}, {"iperf3", "tcp"}, {"iperf3", "udp"}}
		for _, c := range cases {
			t.Run(fmt.Sprintf("IPv%d/%s/%s", family, c[0], c[1]), func(t *testing.T) {
				localAlice := alice
				localAlice.t = t
				localBob := bob
				localBob.t = t
				request := makeRequest(c[0], c[1], family)
				var d core.DiagnosticSession
				localAlice.call("POST", "/network-diagnostics", request, 201, &d)
				localAlice.call("POST", "/network-diagnostics", request, 201, nil) // duplicate request must not rerun
				localBob.call("GET", "/network-diagnostics/"+d.ID, nil, 404, nil)
				d = wait(t, d.ID)
				encoded, _ := json.MarshalIndent(d, "", "  ")
				_ = os.WriteFile(filepath.Join(artifact, fmt.Sprintf("%s-%s-ipv%d.json", c[0], c[1], family)), encoded, 0600)
				if d.State != "succeeded" {
					t.Fatalf("real measurement failed: %s", encoded)
				}
				for _, r := range d.Results {
					if !r.Cleaned {
						t.Fatal("terminal result did not confirm cleanup")
					}
					if len(r.Output) == 0 {
						t.Fatalf("missing real output: %+v", r)
					}
				}
				if request.Bidirectional && len(d.Results) != 2 {
					t.Fatal("missing real reverse measurement")
				}
				if c[0] == "iperf3" {
					client := d.Results[d.ID+"-client"]
					var result struct {
						Start struct {
							Connected []struct {
								LocalHost  string `json:"local_host"`
								RemoteHost string `json:"remote_host"`
							} `json:"connected"`
						} `json:"start"`
						End struct {
							Sent struct {
								BPS float64 `json:"bits_per_second"`
							} `json:"sum_sent"`
						} `json:"end"`
					}
					if e = json.Unmarshal(client.Output, &result); e != nil {
						t.Fatal(e)
					}
					if len(result.Start.Connected) == 0 || result.Start.Connected[0].LocalHost != request.Node1.Address || result.Start.Connected[0].RemoteHost != request.Node2.Address {
						t.Fatalf("iperf actual direction: %s", client.Output)
					}
					if strings.Contains(run("exec", names[1], "ss", "-lntup"), ":5201") {
						t.Fatal("task listener leaked")
					}
				}
			})
		}
	}
	// Exercise actual tool limits, including IPv6/protocol header sizes. API
	// rejection alone cannot prove that the installed binary accepts a boundary.
	for _, family := range []int{4, 6} {
		for _, probe := range [][2]string{{"ping", "icmp"}, {"traceroute", "icmp"}, {"traceroute", "tcp"}, {"traceroute", "udp"}, {"mtr", "icmp"}, {"mtr", "tcp"}, {"mtr", "udp"}, {"iperf3", "tcp"}, {"iperf3", "udp"}} {
			minimum, maximum := 0, 1400
			switch probe[0] {
			case "traceroute", "mtr":
				minimum, maximum = 28, 1500
				if probe[1] == "tcp" {
					minimum = 44
				}
				if family == 6 {
					minimum += 20
				}
			case "iperf3":
				minimum = 16
				if probe[1] == "tcp" {
					minimum, maximum = 1, 1048576
				}
			}
			for _, size := range []int{minimum, maximum} {
				t.Run(fmt.Sprintf("size-IPv%d/%s/%s/%d", family, probe[0], probe[1], size), func(t *testing.T) {
					r := makeRequest(probe[0], probe[1], family)
					r.Bidirectional = false
					r.Parameters.Count = 1
					r.Parameters.Size = size
					local := alice
					local.t = t
					for _, invalid := range []int{minimum - 1, maximum + 1} {
						bad := r
						bad.RequestID += fmt.Sprintf("-invalid-%d", invalid)
						bad.Parameters.Size = invalid
						local.call("POST", "/network-diagnostics", bad, 400, nil)
					}
					var d core.DiagnosticSession
					local.call("POST", "/network-diagnostics", r, 201, &d)
					d = wait(t, d.ID)
					if d.State != "succeeded" {
						t.Fatalf("tool rejected valid size %d: %+v", size, d)
					}
					for _, result := range d.Results {
						if !result.Cleaned || len(result.Output) == 0 {
							t.Fatalf("size result incomplete: %+v", result)
						}
					}
				})
			}
		}
	}
	// Custom IP mode targets the existing service without acquiring receiver
	// execution rights, starting a server or stopping the external listener.
	for _, family := range []int{4, 6} {
		for _, probe := range [][2]string{{"ping", "icmp"}, {"traceroute", "icmp"}, {"traceroute", "tcp"}, {"traceroute", "udp"}, {"mtr", "icmp"}, {"mtr", "tcp"}, {"mtr", "udp"}, {"iperf3", "tcp"}, {"iperf3", "udp"}} {
			t.Run(fmt.Sprintf("custom-IPv%d/%s/%s", family, probe[0], probe[1]), func(t *testing.T) {
				r := makeRequest(probe[0], probe[1], family)
				r.TargetMode = "ip"
				r.Node2.AgentID = ""
				r.Node2.InstanceID = ""
				r.Node2.Name = ""
				r.Bidirectional = false
				if probe[0] == "iperf3" {
					r.Parameters.Port = 5300
				}
				var d core.DiagnosticSession
				alice.call("POST", "/network-diagnostics", r, 201, &d)
				d = wait(t, d.ID)
				encoded, _ := json.MarshalIndent(d, "", "  ")
				_ = os.WriteFile(filepath.Join(artifact, fmt.Sprintf("custom-%s-%s-ipv%d.json", probe[0], probe[1], family)), encoded, 0600)
				if d.State != "succeeded" || len(d.Results) != 1 {
					t.Fatalf("custom real target: %s", encoded)
				}
				if !strings.Contains(run("exec", names[1], "ss", "-lntup"), ":5300") {
					t.Fatal("external service was stopped")
				}
			})
		}
	}
	// A public-format address is DNATed inside the source test container to
	// the real receiver; no packet to it can leave the internal network.
	// It makes NextTrace query the selected metadata provider instead of using
	// its built-in private-address label.
	run("exec", names[0], "ip", "route", "add", "8.8.4.4/32", "via", "172.29.78.3")
	run("exec", names[0], "nft", "add", "table", "ip", "qch_geo_test")
	run("exec", names[0], "nft", "add", "chain", "ip", "qch_geo_test", "output", "{ type nat hook output priority -100; policy accept; }")
	run("exec", names[0], "nft", "add", "rule", "ip", "qch_geo_test", "output", "ip", "daddr", "8.8.4.4", "dnat", "to", "172.29.78.3")
	for _, tool := range []string{"traceroute", "mtr"} {
		for _, provider := range []string{"disable-geoip", "chunzhen"} {
			t.Run("provider/"+tool+"/"+provider, func(t *testing.T) {
				before := geoCalls.Load()
				r := makeRequest(tool, "icmp", 4)
				r.TargetMode = "ip"
				r.Bidirectional = false
				r.Node2 = core.DiagnosticEndpoint{Address: "8.8.4.4"}
				r.Parameters.Provider = provider
				var d core.DiagnosticSession
				alice.call("POST", "/network-diagnostics", r, 201, &d)
				d = wait(t, d.ID)
				encoded, _ := json.Marshal(d)
				if d.State != "succeeded" {
					t.Fatalf("provider measurement: %s", encoded)
				}
				if provider == "chunzhen" && (geoCalls.Load() <= before || !strings.Contains(string(encoded), "TestCity")) {
					t.Fatalf("selected provider was not queried: %s", encoded)
				}
				if provider == "disable-geoip" && geoCalls.Load() != before {
					t.Fatal("disabled lookup queried provider")
				}
			})
		}
	}
	geoFail.Store(true)
	t.Run("provider/rate-limit-preserves-probe", func(t *testing.T) {
		r := makeRequest("mtr", "icmp", 4)
		r.TargetMode = "ip"
		r.Bidirectional = false
		r.Node2 = core.DiagnosticEndpoint{Address: "8.8.4.4"}
		r.Parameters.Provider = "chunzhen"
		var d core.DiagnosticSession
		alice.call("POST", "/network-diagnostics", r, 201, &d)
		d = wait(t, d.ID)
		encoded, _ := json.Marshal(d)
		if d.State != "succeeded" || !strings.Contains(string(encoded), "destination_reached") || strings.Contains(string(encoded), "TestCity") {
			t.Fatalf("metadata failure erased network evidence: %s", encoded)
		}
	})
	geoFail.Store(false)
	if !strings.Contains(run("exec", names[1], "ss", "-lntup"), ":5300") {
		t.Fatal("unrelated iperf server was killed")
	}
	for _, tool := range []string{"ping", "traceroute", "mtr", "iperf3"} {
		t.Run("custom-stop/"+tool, func(t *testing.T) {
			r := makeRequest(tool, "icmp", 4)
			r.TargetMode = "ip"
			r.Bidirectional = false
			r.Node2.AgentID = ""
			r.Node2.InstanceID = ""
			r.Node2.Name = ""
			r.Parameters.Count = 100
			if tool == "iperf3" {
				r.Parameters.Protocol = "tcp"
				r.Parameters.Duration = 30
				r.Parameters.Port = 5300
			}
			var d core.DiagnosticSession
			alice.call("POST", "/network-diagnostics", r, 201, &d)
			time.Sleep(3 * time.Second)
			alice.call("POST", "/network-diagnostics/"+d.ID+"/stop", nil, 204, nil)
			d = wait(t, d.ID)
			if d.State != "canceled" {
				t.Fatalf("custom stop did not complete: %+v", d)
			}
			for _, result := range d.Results {
				if !result.Cleaned {
					t.Fatal("stop lacked process cleanup confirmation")
				}
			}
			if !strings.Contains(run("exec", names[1], "ss", "-lntup"), ":5300") {
				t.Fatal("custom stop closed external server")
			}
		})
	}
	// Stop while the receiver is listening, then confirm actual sockets/processes.
	request := makeRequest("iperf3", "tcp", 4)
	request.Parameters.Duration = 30
	var d core.DiagnosticSession
	alice.call("POST", "/network-diagnostics", request, 201, &d)
	waitClient(t, d.ID)
	alice.call("POST", "/network-diagnostics/"+d.ID+"/stop", nil, 204, nil)
	d = wait(t, d.ID)
	if d.State != "canceled" {
		t.Fatalf("running stop: %+v", d)
	}
	if strings.Contains(run("exec", names[1], "ss", "-lntup"), ":5201") {
		t.Fatal("listener survived stop")
	}
	// Real port conflict is rejected without touching either existing server.
	run("exec", "-d", names[1], "iperf3", "-s", "-p", "5201", "--pidfile", "/tmp/qch-conflict.pid")
	request = makeRequest("iperf3", "tcp", 4)
	alice.call("POST", "/network-diagnostics", request, 201, &d)
	d = wait(t, d.ID)
	if d.State != "failed" {
		t.Fatalf("port conflict not rejected: %+v", d)
	}
	if !strings.Contains(run("exec", names[1], "ss", "-lntup"), ":5201") {
		t.Fatal("pre-existing listener was killed")
	}
	// Remove only the deliberately created conflict service by its own PID.
	run("exec", names[1], "sh", "-c", "kill $(cat /tmp/qch-conflict.pid)")
	for _, failure := range []string{"receiver-restart", "control-disconnect"} {
		t.Run(failure, func(t *testing.T) {
			request := makeRequest("iperf3", "tcp", 4)
			request.Parameters.Duration = 30
			var d core.DiagnosticSession
			alice.call("POST", "/network-diagnostics", request, 201, &d)
			waitClient(t, d.ID)
			if failure == "receiver-restart" {
				run("restart", names[1])
			} else {
				run("network", "disconnect", network, names[1])
				time.Sleep(12 * time.Second)
				if strings.Contains(run("exec", names[1], "ss", "-lntup"), ":5201") {
					t.Fatal("disconnected Agent leaked listener")
				}
				run("network", "connect", "--ip", "172.29.78.3", "--ip6", "fd42:7163:7800::3", network, names[1])
			}
			d = wait(t, d.ID)
			if d.State == "succeeded" {
				t.Fatal("interrupted measurement reported success")
			}
			if strings.Contains(run("exec", names[1], "ss", "-lntup"), ":5201") {
				t.Fatal("interrupted listener leaked")
			}
		})
	}
	if !t.Failed() {
		t.Logf("REAL_AGENTS_VERIFIED preview=%s artifacts=%s", local, artifact)
	}
	if os.Getenv("QCH_NETDIAG_PREVIEW") == "1" {
		_ = os.WriteFile(filepath.Join(artifact, "preview.url"), []byte(local), 0600)
		for {
			if _, err := os.Stat(filepath.Join(artifact, "preview.stop")); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("preview deadline")
			case <-time.After(time.Second):
			}
		}
	}
}
