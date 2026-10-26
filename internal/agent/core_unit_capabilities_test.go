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

func TestXrayOutboundMarkCapabilityRecognition(t *testing.T) {
	variants := managedCapabilityDropInVariants("qagent-xray.service")
	if len(variants) != 2 || !strings.Contains(string(variants[1]), "CAP_NET_ADMIN") {
		t.Fatal("marked Xray drop-in must be recognized during managed-service migration")
	}
	for _, marked := range []bool{false, true} {
		capabilities := "cap_net_bind_service"
		if marked {
			capabilities += " cap_net_admin"
		}
		systemctl := filepath.Join(t.TempDir(), "systemctl")
		writeExecutable(t, systemctl, "#!/bin/sh\nprintf '%s\\n' 'AmbientCapabilities="+capabilities+"' 'CapabilityBoundingSet="+capabilities+"'\n")
		syncer := coreUnitCapabilitySyncer{systemctlPath: systemctl, outboundMarks: true}
		configured, err := syncer.configured(context.Background(), "qagent-xray.service")
		if err != nil || configured != marked {
			t.Fatalf("socket mark capability checked incorrectly: configured=%v err=%v", configured, err)
		}
	}
}

func TestMihomoNetworkFamilyRecognition(t *testing.T) {
	for _, tc := range []struct {
		families string
		allowed  bool
	}{
		{"AF_UNIX AF_INET AF_INET6", false},
		{"AF_UNIX AF_INET AF_INET6 AF_NETLINK", true},
		{"", true},
		{"~AF_PACKET", true},
		{"~AF_NETLINK AF_PACKET", false},
	} {
		t.Run(tc.families, func(t *testing.T) {
			systemctl := filepath.Join(t.TempDir(), "systemctl")
			writeExecutable(t, systemctl, "#!/bin/sh\nprintf '%s\\n' 'AmbientCapabilities=cap_net_bind_service cap_net_admin' 'CapabilityBoundingSet=cap_net_bind_service cap_net_admin' 'RestrictAddressFamilies="+tc.families+"'\n")
			syncer := coreUnitCapabilitySyncer{systemctlPath: systemctl, outboundMarks: true}
			configured, err := syncer.configured(context.Background(), "qagent-mihomo.service")
			if err != nil || configured != tc.allowed {
				t.Fatalf("Mihomo netlink permission: configured=%v err=%v", configured, err)
			}
		})
	}
}

func TestMihomoRepairsNetworkFamilyWithExistingCapabilities(t *testing.T) {
	root := t.TempDir()
	dropInPath := filepath.Join(root, "qagent-mihomo.service.d", "10-qcontrolhub-bind-low-ports.conf")
	systemctl, runner := filepath.Join(root, "systemctl"), filepath.Join(root, "systemd-run")
	writeExecutable(t, systemctl, "#!/bin/sh\ncase \"$1\" in\nshow)\n  printf '%s\\n' 'AmbientCapabilities=cap_net_bind_service cap_net_admin' 'CapabilityBoundingSet=cap_net_bind_service cap_net_admin'\n  families='AF_UNIX AF_INET AF_INET6'\n  if [ -f '"+dropInPath+"' ] && /usr/bin/grep -q '^RestrictAddressFamilies=AF_NETLINK$' '"+dropInPath+"'; then families=\"$families AF_NETLINK\"; fi\n  printf 'RestrictAddressFamilies=%s\\n' \"$families\"\n  ;;\ndaemon-reload) ;;\n*) exit 64 ;;\nesac\n")
	writeExecutable(t, runner, "#!/bin/sh\nwhile [ \"$#\" -gt 0 ] && [ \"$1\" != -- ]; do shift; done\nshift\nexec \"$@\"\n")
	for _, name := range []string{"install", "tee", "mv", "rm"} {
		writeExecutable(t, filepath.Join(root, name), "#!/bin/sh\nexec /usr/bin/"+name+" \"$@\"\n")
	}
	syncer := coreUnitCapabilitySyncer{
		outboundMarks: true, systemctlPath: systemctl, systemdRunPath: runner,
		installPath: filepath.Join(root, "install"), teePath: filepath.Join(root, "tee"), movePath: filepath.Join(root, "mv"), removePath: filepath.Join(root, "rm"), dropInRoot: root,
	}
	if err := syncer.ensure(context.Background(), "qagent-mihomo.service"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(dropInPath)
	if err != nil || string(contents) != string(managedCapabilityDropInVariants("qagent-mihomo.service")[2]) {
		t.Fatalf("network repair drop-in: %q %v", contents, err)
	}
	// The next restart must recognize the repair rather than rewrite it.
	syncer.installPath = filepath.Join(root, "missing-helper")
	if err := syncer.ensure(context.Background(), "qagent-mihomo.service"); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := writeFixtureExecutable(path, []byte(contents)); err != nil {
		t.Fatal(err)
	}
}

func TestManagedUnitHelperProtectedMulticallLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "coreutils")
	writeExecutable(t, target, "#!/bin/sh\nexit 0\n")
	link := filepath.Join(root, "install")
	if err := os.Symlink("coreutils", link); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedUnitHelper(link); err != nil {
		t.Fatalf("protected distribution multicall link: %v", err)
	}
	if err := validatePrivilegedExecutable(link); err == nil {
		t.Fatal("generic executable validation must still reject symlinks")
	}
	chain := filepath.Join(root, "chain")
	if err := os.Symlink(link, chain); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedUnitHelper(chain); err == nil {
		t.Fatal("accepted an indirect link")
	}
	if err := os.Chmod(target, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedUnitHelper(link); err == nil {
		t.Fatal("accepted a writable executable target")
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Lchown(link, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		if err := validateManagedUnitHelper(link); err == nil {
			t.Fatal("accepted a foreign-owned helper link")
		}
		if err := os.Lchown(link, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedUnitHelper(link); err == nil {
		t.Fatal("accepted an unprotected helper directory")
	}
}
