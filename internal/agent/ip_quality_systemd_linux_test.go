//go:build linux

package agent

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The harness runs this test as the shipped Agent unit in a fresh, isolated
// systemd container. It uses actual APT repositories, not the detector network.
func TestIPQualityDependencyHelperSystemdSandbox(t *testing.T) {
	if os.Getenv("QCH_TEST_IPQUALITY_SYSTEMD_SANDBOX") != "1" {
		t.Skip("requires deploy/tests/ip-quality-systemd.sh")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("package installation test requires an isolated Docker node")
	}
	sources, err := os.ReadFile("/etc/apt/sources.list.d/debian.sources")
	if err != nil || !strings.Contains(string(sources), "mirror+file:/etc/apt/mirrors/") {
		t.Fatalf("missing isolated mirror+file fixture: %v", err)
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "CapBnd:") {
			caps, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapBnd:")), 16, 64)
			// The main Agent needs CHOWN, NET_ADMIN and NET_BIND_SERVICE for
			// HTTP-01. Identity capabilities remain exclusive to the fixed helper.
			if err != nil || caps != (1<<0)|(1<<10)|(1<<12) {
				t.Fatalf("main Agent capabilities changed: %s %v", line, err)
			}
		}
	}
	if !strings.Contains(string(status), "NoNewPrivs:\t1") {
		t.Fatal("main Agent must retain NoNewPrivileges")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:80")
	if err != nil {
		t.Fatalf("shipped Agent cannot bind the HTTP-01 port: %v", err)
	}
	_ = listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	p := newIPQualityDependencyPreparer()
	missing, err := p.missing(ctx)
	if err != nil || len(missing) == 0 {
		t.Fatalf("fixture must start without detector dependencies: %v %v", missing, err)
	}
	const executable = "/usr/local/lib/qagent/qagent"
	if os.Getenv("QCH_TEST_IPQUALITY_LEGACY_UID_FAILURE") == "1" {
		args := ipQualityDependencyHelperArguments(executable, "qagent-ip-quality-legacy-fixture.service")
		var legacy []string
		for _, arg := range args {
			if !strings.HasPrefix(arg, "--property=AmbientCapabilities=") {
				legacy = append(legacy, arg)
			}
		}
		output, err := runIPQualityCommand(ctx, "/usr/bin/systemd-run", legacy)
		if err == nil || !strings.Contains(output, "seteuid 42 failed") || !strings.Contains(output, "mirror+file") {
			t.Fatalf("legacy policy did not reproduce APT identity failure: %v\n%s", err, output)
		}
		t.Log("legacy helper reproduced seteuid/setresuid failure before installation")
	}
	for _, phase := range []string{"first", "repeat"} {
		args := ipQualityDependencyHelperArguments(executable, "qagent-ip-quality-"+phase+"-fixture.service")
		output, err := runIPQualityCommand(ctx, "/usr/bin/systemd-run", args)
		if err != nil {
			t.Fatalf("%s preparation failed: %v\n%s", phase, err, output)
		}
		if phase == "first" && !strings.Contains(output, "安装依赖：完成并重新验证可执行性") {
			t.Fatalf("first preparation did not install/verify dependencies: %s", output)
		}
		if phase == "repeat" && (!strings.Contains(output, "全部可执行，复用已有依赖") || strings.Contains(output, "apt-get")) {
			t.Fatalf("repeat preparation did not reuse installed dependencies: %s", output)
		}
		missing, err := p.missing(ctx)
		if err != nil || len(missing) != 0 {
			t.Fatalf("%s preparation left missing commands: %v %v", phase, missing, err)
		}
		t.Log(phase + " preparation passed under all shipped Agent restrictions")
	}
}
