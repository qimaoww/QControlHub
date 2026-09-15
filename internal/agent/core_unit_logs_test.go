package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallManagedLogFileIsProtectedAndIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	systemdRoot := filepath.Join(root, "systemd")
	if err := os.Mkdir(systemdRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	systemdRun := filepath.Join(root, "systemd-run")
	install := filepath.Join(root, "install")
	tee := filepath.Join(root, "tee")
	move := filepath.Join(root, "mv")
	remove := filepath.Join(root, "rm")
	writeExecutable(t, systemdRun, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ] && [ \"$1\" != -- ]; do shift; done\nshift\nexec \"$@\"\n")
	writeExecutable(t, install, "#!/bin/sh\nexec /usr/bin/install \"$@\"\n")
	writeExecutable(t, tee, "#!/bin/sh\nexec /usr/bin/tee \"$@\"\n")
	writeExecutable(t, move, "#!/bin/sh\nexec /usr/bin/mv \"$@\"\n")
	writeExecutable(t, remove, "#!/bin/sh\nexec /usr/bin/rm \"$@\"\n")
	syncer := coreUnitCapabilitySyncer{
		systemdRunPath: systemdRun, installPath: install, teePath: tee,
		movePath: move, removePath: remove, dropInRoot: systemdRoot,
	}
	destination := filepath.Join(systemdRoot, "system", "qagent-xray.service.d", "20-qcontrolhub-volatile-logs.conf")
	changed, err := installManagedLogFile(context.Background(), syncer, destination, []byte(managedCoreLogDropIn))
	if err != nil || !changed {
		t.Fatalf("first install = changed %v, %v", changed, err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != managedCoreLogDropIn {
		t.Fatalf("installed contents = %q, %v", contents, err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("installed mode = %v, %v", info.Mode(), err)
	}
	changed, err = installManagedLogFile(context.Background(), syncer, destination, []byte(managedCoreLogDropIn))
	if err != nil || changed {
		t.Fatalf("second install = changed %v, %v", changed, err)
	}
	if _, err := installManagedLogFile(context.Background(), syncer, filepath.Join(root, "escape.conf"), []byte("unsafe")); err == nil {
		t.Fatal("managed log install accepted a path outside /etc/systemd")
	}
}

func TestStartManagedCoreJournalUsesFixedNamespaceUnit(t *testing.T) {
	t.Parallel()
	systemctl := filepath.Join(t.TempDir(), "systemctl")
	writeExecutable(t, systemctl, `#!/bin/sh
test "$2" = systemd-journald@qagent-cores.service
case "$1" in
start) ;;
is-active) printf 'active\n' ;;
*) exit 64 ;;
esac
`)
	if err := startManagedCoreJournal(context.Background(), systemctl); err != nil {
		t.Fatal(err)
	}
}

func TestManagedCoreLogFallbackIsProjectManaged(t *testing.T) {
	fallback, err := managedCoreLogFallbackDropIn("qagent-xray.service")
	if err != nil {
		t.Fatal(err)
	}
	for _, contents := range [][]byte{[]byte(managedCoreLogDropIn), fallback} {
		if !matchesAnyManagedDropIn(contents, [][]byte{[]byte(managedCoreLogDropIn), fallback}) {
			t.Fatalf("managed log drop-in was not accepted: %q", contents)
		}
	}
	if matchesAnyManagedDropIn([]byte("[Service]\nEnvironment=UNSAFE=1\n"), [][]byte{[]byte(managedCoreLogDropIn), fallback}) {
		t.Fatal("unknown managed log drop-in was accepted")
	}
	if strings.Contains(string(fallback), "StandardOutput=journal") || strings.Contains(string(fallback), "StandardOutput=null") ||
		!strings.Contains(string(fallback), "StandardOutput=append:/run/qagent-core-logs/qagent-core-log-qagent-xray.service.log") {
		t.Fatalf("unsupported namespace fallback is not a bounded /run transport: %s", fallback)
	}
	if _, err := managedCoreLogFallbackDropIn("operator.service"); err == nil {
		t.Fatal("fallback accepted a non-project service")
	}
}
