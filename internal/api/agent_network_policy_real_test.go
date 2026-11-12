//go:build linux

package api

import (
	"bytes"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Reuse a disposable node from the existing service-manager sandbox harness.
// It must start with the pre-diagnostics Agent and its exact shipped unit.
func TestAgentRawPolicyUpgradeSandbox(t *testing.T) {
	container := os.Getenv("QCH_RAW_POLICY_CONTAINER")
	binary := os.Getenv("QCH_RAW_POLICY_BINARY")
	openrc := os.Getenv("QCH_RAW_POLICY_MANAGER") == "openrc"
	older := os.Getenv("QCH_RAW_POLICY_OLDER") == "1"
	if older && openrc {
		t.Fatal("older fixture is systemd only")
	}
	policyPath, assetPath := "/etc/systemd/system/qagent.service", "../../deploy/systemd/qagent.service"
	if openrc {
		policyPath, assetPath = "/etc/init.d/qagent", "../../deploy/openrc/qagent"
	}
	if container == "" || binary == "" {
		t.Skip("requires a disposable legacy systemd Agent node and the candidate binary")
	}
	db, ctx, admin, _, _ := newConfigScopeAPIFixtureWithin(t, 3*time.Minute)
	candidate, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	config := configScopeFixtureConfig()
	config.AgentBinary, config.AgentVersion = candidate, "raw-permission-test"
	admin.handler = New(db, config).Handler()
	server := httptest.NewUnstartedServer(admin.handler)
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	run := func(args ...string) string {
		t.Helper()
		b, err := exec.CommandContext(ctx, "docker", append([]string{"exec", container}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("sandbox command %s failed: %v: %s", args[0], err, b)
		}
		return strings.TrimSpace(string(b))
	}
	if run("test", "-f", "/.dockerenv") != "" {
		t.Fatal("not an isolated node")
	}
	var enrollment core.EnrollmentTokenCreated
	admin.call("POST", "/enrollment-tokens", map[string]string{"name": "raw-policy-fixture"}, 201, &enrollment)
	// Pass credentials only through stdin; never include them in test output.
	env := fmt.Sprintf("QCH_SERVER_URL=http://host.docker.internal:%d\nQCH_AGENT_STATE=/var/lib/qcontrolhub/raw-policy.json\nQCH_ALLOW_HTTP=true\nQCH_ALLOW_INSECURE_LIVE=true\nQCH_HEARTBEAT_INTERVAL=1s\nQCH_AGENT_NAME=raw-policy-fixture\nQCH_ENROLLMENT_TOKEN=%s\n", listener.Addr().(*net.TCPAddr).Port, enrollment.Token)
	envPath := "/etc/qcontrolhub/agent.env"
	if openrc {
		env += "QCH_SERVICE_MANAGER=openrc\n"
		var exports []string
		for _, line := range strings.Split(strings.TrimSpace(env), "\n") {
			key, value, _ := strings.Cut(line, "=")
			exports = append(exports, "export "+key+"='"+strings.ReplaceAll(value, "'", "'\\''")+"'")
		}
		env = strings.Join(exports, "\n") + "\n"
		envPath = "/etc/conf.d/qagent"
	}
	write := exec.CommandContext(ctx, "docker", "exec", "-i", container, "sh", "-c", "umask 077; cat > "+envPath)
	write.Stdin = strings.NewReader(env)
	if err := write.Run(); err != nil {
		t.Fatal(err)
	}
	pid := func() string {
		if openrc {
			return run("cat", "/run/openrc/options/qagent/child_pid")
		}
		return run("systemctl", "show", "qagent.service", "--property=MainPID", "--value")
	}
	if openrc {
		run("/sbin/rc-service", "qagent", "start")
	} else {
		run("systemctl", "start", "qagent.service")
	}
	t.Cleanup(func() {
		args := []string{"systemctl", "stop", "qagent.service"}
		if openrc {
			args = []string{"/sbin/rc-service", "qagent", "stop"}
		}
		_ = exec.Command("docker", append([]string{"exec", container}, args...)...).Run()
	})
	want, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatal(err)
	}
	legacyMask := "0000000000001401"
	if older {
		original, err := os.ReadFile("../agent/testdata/agent-systemd-pre-certificates.service")
		if err != nil {
			t.Fatal(err)
		}
		if run("cat", policyPath) != strings.TrimSpace(string(original)) {
			t.Fatal("older service fixture mismatch")
		}
		want = bytes.ReplaceAll(original, []byte("CAP_CHOWN CAP_NET_ADMIN\n"), []byte("CAP_CHOWN CAP_NET_ADMIN CAP_NET_RAW\n"))
		legacyMask = "0000000000001001"
	}
	before := ""
	wait := func(ready, upgraded bool) core.DiagnosticNode {
		t.Helper()
		until := time.Now().Add(65 * time.Second)
		var nodes []core.DiagnosticNode
		for time.Now().Before(until) {
			admin.call("GET", "/network-diagnostics/nodes", nil, 200, &nodes)
			if upgraded && (pid() == before || run("cat", policyPath) != strings.TrimSpace(string(want))) {
				time.Sleep(250 * time.Millisecond)
				continue
			}
			versions := map[string]string{}
			if upgraded {
				var agents []core.Agent
				admin.call("GET", "/agents", nil, 200, &agents)
				for _, a := range agents {
					versions[a.ID] = a.Version
				}
			}
			for _, n := range nodes {
				if upgraded && versions[n.AgentID] != "raw-permission-test" {
					continue
				}
				if n.Name == "raw-policy-fixture" && n.Online && (n.Capabilities.Tools["nexttrace"].Error == "") == ready {
					return n
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("capability did not converge (ready=%v)", ready)
		return core.DiagnosticNode{}
	}
	denied := wait(openrc, false)
	if !openrc && (denied.Capabilities.Tools["nexttrace"].AutoInstall || !strings.Contains(denied.Capabilities.Tools["nexttrace"].Error, "operation not permitted")) {
		t.Fatalf("legacy failure: %+v", denied.Capabilities.Tools["nexttrace"])
	}
	before = pid()
	status := run("cat", "/proc/"+before+"/status")
	if !openrc && !strings.Contains(status, "CapBnd:\t"+legacyMask) {
		t.Fatalf("legacy RAW denial was not real: %s", status)
	}
	var task core.Task
	admin.call("POST", "/tasks", core.TaskRequest{AgentID: denied.AgentID, Action: core.ActionUpgradeAgent}, 201, &task)
	recovered := wait(true, true)
	if recovered.AgentID != denied.AgentID {
		t.Fatal("upgrade changed node identity")
	}
	after := pid()
	if before == after {
		t.Fatal("service policy never started a new process")
	}
	status = run("cat", "/proc/"+after+"/status")
	expectedAmbient := "0000000000003401"
	if older {
		expectedAmbient = "0000000000003001"
	}
	if openrc {
		expectedAmbient = "00000000000034c1"
	}
	if (!openrc && !strings.Contains(status, "CapBnd:\t"+expectedAmbient)) || !strings.Contains(status, "CapAmb:\t"+expectedAmbient) || !strings.Contains(status, "NoNewPrivs:\t1") {
		t.Fatal("new process did not receive the shipped narrow capability policy")
	}
	got := run("cat", policyPath)
	if !bytes.Equal(bytes.TrimSpace(want), []byte(got)) {
		t.Fatal("migration changed the bundled policy")
	}
	var finished core.Task
	admin.call("GET", "/tasks/"+task.ID, nil, 200, &finished)
	if finished.Status != core.TaskSucceeded {
		t.Fatalf("upgrade task: %s", finished.Status)
	}
	time.Sleep(2 * time.Second)
	if pid() != after {
		t.Fatal("policy recovery restarted repeatedly")
	}
}
