package agent

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedXrayListenerPreflightReportsOccupiedPortBeforeDeployment(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	content := fmt.Sprintf(`{"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"vmess"}]}`, port)
	spec := EngineSpec{ConfigPath: filepath.Join(t.TempDir(), "config.json")}
	if err := checkXrayListenerPorts(spec, content, true); err == nil || !strings.Contains(err.Error(), fmt.Sprint(port)) || !strings.Contains(err.Error(), "占用") {
		t.Fatalf("shared listener conflict lacks a useful reason: %v", err)
	}
	if err := os.WriteFile(spec.ConfigPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkXrayListenerPorts(spec, content, true); err != nil {
		t.Fatalf("preflight rejected the instance's retained listener: %v", err)
	}
}

func TestServiceFailureJournalRetainsCoreErrorBesideRestartChatter(t *testing.T) {
	previous := journalctlPath
	journalctlPath = filepath.Join(t.TempDir(), "journalctl")
	t.Cleanup(func() { journalctlPath = previous })
	script := `#!/bin/sh
printf '%06000d\n' 0
case "$1" in
  --namespace=qagent-cores) printf '%s\n' 'listen tcp 0.0.0.0:24120: bind: address already in use' ;;
  *) printf '%s\n' 'systemd scheduled restart' ;;
esac
`
	if err := os.WriteFile(journalctlPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	output := serviceFailureJournal(context.Background(), "qagent-xray@shr_0123456789abcdef.service")
	if !strings.Contains(output, "24120: bind: address already in use") || !strings.Contains(output, "systemd scheduled restart") || len(output) > 4096 {
		t.Fatalf("core failure was hidden by service-manager output: %q", output)
	}
}
