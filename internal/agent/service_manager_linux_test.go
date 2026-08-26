//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestOpenRCServiceManagerCommandsAndNormalizesStatus(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(directory, "openrc.log")
	helper := filepath.Join(directory, "rc-service")
	script := "#!/bin/sh\n" +
		"/usr/bin/printf '%s %s\\n' \"$1\" \"$2\" >> " + logPath + "\n" +
		"if [ \"$2\" = status ]; then /usr/bin/printf '%s\\n' 'status: stopped'; exit 3; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := &ServiceManager{kind: ServiceManagerOpenRC, executable: helper}
	if _, err := manager.command(context.Background(), "qagent-xray", core.ActionRestart); err != nil {
		t.Fatal(err)
	}
	status, err := manager.status(context.Background(), "qagent-xray")
	if err != nil || status != "inactive" {
		t.Fatalf("OpenRC status = %q, %v; want inactive", status, err)
	}
	commands, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(commands); got != "qagent-xray status\nqagent-xray start\nqagent-xray status\n" {
		t.Fatalf("OpenRC commands = %q", got)
	}
}

func TestOpenRCServiceManagerRestartsActiveService(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(directory, "openrc.log")
	helper := filepath.Join(directory, "rc-service")
	script := "#!/bin/sh\n" +
		"/usr/bin/printf '%s %s\\n' \"$1\" \"$2\" >> " + logPath + "\n" +
		"if [ \"$2\" = status ]; then /usr/bin/printf '%s\\n' 'status: started'; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := &ServiceManager{kind: ServiceManagerOpenRC, executable: helper}
	if _, err := manager.command(context.Background(), "qagent-xray", core.ActionRestart); err != nil {
		t.Fatal(err)
	}
	commands, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(commands); got != "qagent-xray status\nqagent-xray restart\n" {
		t.Fatalf("OpenRC commands = %q", got)
	}
}

func TestOpenRCServiceManagerRejectsUnsafeNames(t *testing.T) {
	manager := &ServiceManager{kind: ServiceManagerOpenRC, executable: "/sbin/rc-service"}
	if _, err := manager.command(context.Background(), "xray;reboot", core.ActionRestart); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe OpenRC service error = %v", err)
	}
}

func TestSystemdStopClearsFailedStateBeforeVerification(t *testing.T) {
	directory := t.TempDir()
	statePath := filepath.Join(directory, "state")
	logPath := filepath.Join(directory, "systemctl.log")
	helper := filepath.Join(directory, "systemctl")
	if err := os.WriteFile(statePath, []byte("failed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"/usr/bin/printf '%s %s\\n' \"$1\" \"$2\" >> " + logPath + "\n" +
		"if [ \"$1\" = stop ]; then exit 0; fi\n" +
		"if [ \"$1\" = reset-failed ]; then /usr/bin/printf 'inactive\\n' > " + statePath + "; exit 0; fi\n" +
		"if [ \"$1\" = is-active ]; then /usr/bin/cat " + statePath + "; exit 3; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := &ServiceManager{kind: ServiceManagerSystemd, executable: helper}
	if _, err := serviceCommandAndVerifyWithManager(context.Background(), manager, "qagent-xray.service", core.ActionStop); err != nil {
		t.Fatal(err)
	}
	commands, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(commands); got != "stop qagent-xray.service\nis-active qagent-xray.service\nreset-failed qagent-xray.service\nis-active qagent-xray.service\n" {
		t.Fatalf("systemd commands = %q", got)
	}
}

func TestOpenRCProcessArgvMatchesExactCoreInvocation(t *testing.T) {
	tests := []struct {
		name   string
		engine core.Engine
		spec   EngineSpec
		argv   []string
		want   bool
	}{
		{
			name: "xray exact", engine: core.EngineXray,
			spec: EngineSpec{Binary: "/usr/bin/xray", ConfigPath: "/etc/xray/config.json"},
			argv: []string{"/usr/bin/xray", "run", "-config", "/etc/xray/config.json"}, want: true,
		},
		{
			name: "sing-box config directory", engine: core.EngineSingBox,
			spec: EngineSpec{Binary: "/usr/bin/sing-box", ConfigPath: "/etc/sing-box/config.json", ConfigDirectory: "/etc/sing-box"},
			argv: []string{"/usr/bin/sing-box", "run", "-c", "/etc/sing-box/config.json", "-C", "/etc/sing-box"}, want: true,
		},
		{
			name: "extra argument rejected", engine: core.EngineXray,
			spec: EngineSpec{Binary: "/usr/bin/xray", ConfigPath: "/etc/xray/config.json"},
			argv: []string{"/usr/bin/xray", "run", "-config", "/etc/xray/config.json", "-test"},
		},
		{
			name: "different config rejected", engine: core.EngineSingBox,
			spec: EngineSpec{Binary: "/usr/bin/sing-box", ConfigPath: "/etc/sing-box/config.json"},
			argv: []string{"/usr/bin/sing-box", "run", "-c", "/tmp/config.json"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := openRCProcessArgvMatches(test.engine, test.spec, test.argv); got != test.want {
				t.Fatalf("openRCProcessArgvMatches() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestOpenRCServiceEnableStateAcceptsOnlyDefaultRunlevel(t *testing.T) {
	runlevels := t.TempDir()
	initDirectory := filepath.Join(t.TempDir(), "init.d")
	if err := os.MkdirAll(filepath.Join(runlevels, "default"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(initDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	previousRunlevels, previousInit := openRCRunlevelsRoot, openRCInitRoot
	openRCRunlevelsRoot, openRCInitRoot = runlevels, initDirectory
	t.Cleanup(func() {
		openRCRunlevelsRoot, openRCInitRoot = previousRunlevels, previousInit
	})

	state, err := openRCServiceEnableState(context.Background(), "xray")
	if err != nil || state != "disabled" {
		t.Fatalf("disabled state = %q, %v", state, err)
	}
	if err := os.Symlink(filepath.Join(initDirectory, "xray"), filepath.Join(runlevels, "default", "xray")); err != nil {
		t.Fatal(err)
	}
	state, err = openRCServiceEnableState(context.Background(), "xray")
	if err != nil || state != "enabled" {
		t.Fatalf("enabled state = %q, %v", state, err)
	}
	if err := os.MkdirAll(filepath.Join(runlevels, "boot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(initDirectory, "xray"), filepath.Join(runlevels, "boot", "xray")); err != nil {
		t.Fatal(err)
	}
	if _, err := openRCServiceEnableState(context.Background(), "xray"); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("unsupported multi-runlevel state error = %v", err)
	}
}
