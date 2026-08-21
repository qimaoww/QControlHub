package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCoreUnitCapabilitySyncerInstallsProtectedDropIn(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dropInRoot := filepath.Join(root, "systemd")
	if err := os.Mkdir(dropInRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	dropInPath := filepath.Join(dropInRoot, "qagent-xray.service.d", "10-qcontrolhub-bind-low-ports.conf")
	runnerLog := filepath.Join(root, "runner.log")
	systemctl := filepath.Join(root, "systemctl")
	systemdRun := filepath.Join(root, "systemd-run")
	install := filepath.Join(root, "install")
	tee := filepath.Join(root, "tee")
	move := filepath.Join(root, "mv")
	remove := filepath.Join(root, "rm")
	writeExecutable(t, systemctl, "#!/bin/sh\ncase \"$1\" in\nshow)\n  if [ -f '"+dropInPath+"' ]; then\n    printf '%s\\n' 'AmbientCapabilities=cap_net_bind_service' 'CapabilityBoundingSet=cap_net_bind_service'\n  else\n    printf '%s\\n' 'AmbientCapabilities=' 'CapabilityBoundingSet='\n  fi\n  ;;\ndaemon-reload) ;;\n*) exit 64 ;;\nesac\n")
	writeExecutable(t, systemdRun, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+runnerLog+"'\nwhile [ \"$#\" -gt 0 ] && [ \"$1\" != -- ]; do shift; done\n[ \"$#\" -gt 0 ] || exit 65\nshift\nexec \"$@\"\n")
	writeExecutable(t, install, "#!/bin/sh\nexec /usr/bin/install \"$@\"\n")
	writeExecutable(t, tee, "#!/bin/sh\nexec /usr/bin/tee \"$@\"\n")
	writeExecutable(t, move, "#!/bin/sh\nexec /usr/bin/mv \"$@\"\n")
	writeExecutable(t, remove, "#!/bin/sh\nexec /usr/bin/rm \"$@\"\n")

	syncer := coreUnitCapabilitySyncer{
		systemctlPath: systemctl, systemdRunPath: systemdRun,
		installPath: install, teePath: tee, movePath: move, removePath: remove, dropInRoot: dropInRoot,
	}
	if err := syncer.ensure(context.Background(), "qagent-xray.service"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(dropInPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != managedCoreCapabilityDropIn {
		t.Fatalf("drop-in = %q", contents)
	}
	info, err := os.Stat(dropInPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("drop-in mode = %v", info.Mode().Perm())
	}
	logContents, err := os.ReadFile(runnerLog)
	if err != nil {
		t.Fatal(err)
	}
	logValue := string(logContents)
	for _, required := range []string{
		"--property=NoNewPrivileges=yes", "--property=CapabilityBoundingSet=",
		"--property=ProtectSystem=strict", "--property=ReadWritePaths=" + dropInRoot,
		"-- " + install, "-- " + tee, "-- " + move,
	} {
		if !strings.Contains(logValue, required) {
			t.Errorf("runner log is missing %q: %s", required, logValue)
		}
	}
	before := len(logContents)
	if err := syncer.ensure(context.Background(), "qagent-xray.service"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(runnerLog)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != before {
		t.Fatal("already configured service rewrote its drop-in")
	}
	syncer.installPath = filepath.Join(root, "missing-install")
	if err := syncer.ensure(context.Background(), "qagent-xray.service"); err != nil {
		t.Fatalf("configured service unnecessarily required write helpers: %v", err)
	}
}

func TestCoreUnitCapabilitySyncerRejectsUnmanagedService(t *testing.T) {
	t.Parallel()
	syncer := coreUnitCapabilitySyncer{dropInRoot: t.TempDir()}
	if err := syncer.ensure(context.Background(), "ssh.service"); err == nil {
		t.Fatal("capability sync accepted an unmanaged service")
	}
	custom := DefaultSpecs()[core.EngineXray]
	custom.Service = "xray.service"
	if err := ensureManagedCoreServiceCapabilities(context.Background(), core.EngineXray, custom); err != nil {
		t.Fatalf("custom service should remain administrator-managed: %v", err)
	}
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
}
