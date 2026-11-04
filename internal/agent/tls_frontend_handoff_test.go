package agent

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTLSHandoffRequiresEveryTCPListenerToBelongToTarget(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := processOwnsTCPPort(os.Getpid(), port); err != nil {
		t.Fatal(err)
	}
	// An unrelated process has no authority over this test's listener.
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	if err := processOwnsTCPPort(child.Process.Pid, port); err == nil {
		t.Fatal("foreign listener was attributed to target process")
	}
	// The proxy's wildcard bind must also account for a second address.
	other, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.2", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := processOwnsTCPPort(os.Getpid(), port); err != nil {
		t.Fatal(err)
	}
	listener.Close()
	other.Close()
	if err := processOwnsTCPPort(os.Getpid(), port); err == nil {
		t.Fatal("stale ownership survived closed sockets")
	}
}

func TestTLSHandoffChecksAllAddressFamilies(t *testing.T) {
	previousRoot := openRCProcRoot
	openRCProcRoot = t.TempDir()
	t.Cleanup(func() { openRCProcRoot = previousRoot })
	for _, dir := range []string{"net", "123/fd"} {
		if err := os.MkdirAll(filepath.Join(openRCProcRoot, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("socket:[111]", filepath.Join(openRCProcRoot, "123/fd/1")); err != nil {
		t.Fatal(err)
	}
	write := func(family, inode string) {
		t.Helper()
		row := fmt.Sprintf("0: 00000000:01BB 00000000:0000 0A 0:0 0:0 0 0 0 %s\n", inode)
		if err := os.WriteFile(filepath.Join(openRCProcRoot, "net", family), []byte(row), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("tcp", "111")
	write("tcp6", "222")
	if err := processOwnsTCPPort(123, 443); err == nil {
		t.Fatal("foreign IPv6 listener was ignored")
	}
	write("tcp6", "111")
	if err := processOwnsTCPPort(123, 443); err != nil {
		t.Fatal(err)
	}
	if err := processOwnsTCPPort(123, 8443); err == nil {
		t.Fatal("unowned port accepted")
	}
}

func TestTLSFrontendHandoffOnlyDefersStagingBind(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	m := tlsFrontendManager{}
	for _, mode := range []string{"caddy", "nginx"} {
		f := core.TLSFrontend{Mode: mode, PublicPort: port}
		// The fixture isolates the public listener check from this host's
		// unrelated HTTP-01 service; production still probes a new port 80.
		previous := []core.TLSFrontend{{PublicPort: 80}}
		if err := m.checkPorts(previous, []core.TLSFrontend{f}); err == nil || !strings.Contains(err.Error(), "不可用") {
			t.Fatalf("baseline conflict missing: %v", err)
		}
		if err := m.checkPorts(previous, []core.TLSFrontend{f}, func(p int) error { return processOwnsTCPPort(os.Getpid(), p) }); err != nil {
			t.Fatal(err)
		}
		// Apply never receives the staging exception, and therefore refuses a
		// port that the core failed to release (or an external process took).
		if err := m.checkPorts(previous, []core.TLSFrontend{f}); err == nil {
			t.Fatal("apply skipped the bind check")
		}
	}
}
