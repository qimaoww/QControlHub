//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSSRustConsoleLogLevels(t *testing.T) {
	for _, test := range []struct{ message, level string }{
		{"2026-09-05T13:00:00.123456Z DEBUG established tcp tunnel 127.0.0.1:1000 <-> example.invalid:443", "debug"},
		{"2026-09-05T21:00:00.123+08:00 DEBUG created udp association for 127.0.0.1:1000", "debug"},
		{"2026-09-05 13:00:00.123 ERROR tcp tunnel connect failed", "error"},
		{"\x1b[33mWARN\x1b[0m access denied by ACL rules", "warning"},
		{"INFO  shadowsocks tcp server listening on 127.0.0.1:20001", "info"},
		{"TRACE tcp tunnel closed", "debug"},
		{"INFO peer sent ERROR in application message", "info"},
		{"ordinary message with DEBUG words", "info"},
	} {
		t.Run(test.message, func(t *testing.T) {
			for _, unit := range []string{"qagent-shadowsocks-rust.service", "shadowsocks-rust.service"} {
				value, _ := json.Marshal(map[string]string{"MESSAGE": test.message, "_SYSTEMD_UNIT": unit, "PRIORITY": "6", "__CURSOR": "cursor-1"})
				entry, cursor, ok := decodeJournalCoreLog(value, map[string]core.Engine{unit: core.EngineShadowsocksRust})
				if !ok || entry.Level != test.level || entry.Engine != core.EngineShadowsocksRust || cursor != "cursor-1" {
					t.Fatalf("journal entry: %+v cursor=%q ok=%v", entry, cursor, ok)
				}
			}
			collector := NewCoreLogCollector()
			collector.appendFileEntry(coreLogFileSource{engine: core.EngineShadowsocksRust}, []byte(test.message))
			if len(collector.queued) != 1 || collector.queued[0].Level != test.level {
				t.Fatalf("OpenRC entry: %+v", collector.queued)
			}
		})
	}
	if level := ssRustLogLevel("systemd stopped the service", "warning"); level != "warning" {
		t.Fatalf("unstructured journal priority lost: %s", level)
	}
}

func TestSSRustManagedLoggingTemplatesAndLegacyUnits(t *testing.T) {
	systemd, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/systemd/qagent-shadowsocks-rust.service")
	if err != nil {
		t.Fatal(err)
	}
	spec := DefaultSpecs()[core.EngineShadowsocksRust]
	for _, contents := range []string{string(systemd), strings.ReplaceAll(string(systemd), managedSSRustLogFilter, "info")} {
		if err := validateManagedUnitFragment([]byte(contents), core.EngineShadowsocksRust, spec); err != nil {
			t.Fatal(err)
		}
		oldACL := strings.ReplaceAll(contents, " --acl "+shadowsocksRustACLPath, "")
		oldACL = strings.ReplaceAll(oldACL, "ConditionPathExists="+shadowsocksRustACLPath+"\n", "")
		if err := validateManagedUnitFragment([]byte(oldACL), core.EngineShadowsocksRust, spec); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []string{"debug", "trace", managedSSRustLogFilter + ",shadowsocks=trace"} {
		if err := validateManagedUnitFragment([]byte(strings.ReplaceAll(string(systemd), managedSSRustLogFilter, bad)), core.EngineShadowsocksRust, spec); err == nil {
			t.Fatalf("accepted unrecognized filter %q", bad)
		}
	}
}

func TestSSRustLoggingDropInIsExactAndEngineScoped(t *testing.T) {
	oldSystemctl, oldRoot := systemctlPath, existingDiscoveryManagedUnitRoot
	existingDiscoveryManagedUnitRoot = t.TempDir()
	systemctlPath = filepath.Join(t.TempDir(), "systemctl")
	t.Cleanup(func() { systemctlPath, existingDiscoveryManagedUnitRoot = oldSystemctl, oldRoot })
	const service = "qagent-shadowsocks-rust.service"
	directory := filepath.Join(existingDiscoveryManagedUnitRoot, service+".d")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "30-qcontrolhub-ss-rust-logs.conf")
	writeExecutable(t, systemctlPath, "#!/bin/sh\nprintf '%s\\n' '"+path+"'\n")
	if err := os.WriteFile(path, []byte(managedSSRustLogDropIn), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedUnitDropIns(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedUnitDropIns(context.Background(), "qagent-xray.service"); err == nil {
		t.Fatal("SS Rust drop-in accepted on another engine")
	}
	if err := os.WriteFile(path, []byte(managedSSRustLogDropIn+"ExecStart=/tmp/custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedUnitDropIns(context.Background(), service); err == nil {
		t.Fatal("modified log drop-in accepted")
	}
}

func TestSSRustOpenRCLoggingUpgradeIsExactAndIdempotent(t *testing.T) {
	oldRoot := openRCInitRoot
	openRCInitRoot = t.TempDir()
	t.Cleanup(func() { openRCInitRoot = oldRoot })
	const service = "qagent-shadowsocks-rust"
	path := filepath.Join(openRCInitRoot, service)
	current, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/openrc/"+service)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(current), `export RUST_LOG="`+managedSSRustLogFilter+`"`) {
		t.Fatal("OpenRC filter differs from managed journal policy")
	}
	legacy := strings.ReplaceAll(string(current), managedSSRustLogFilter, "info")
	if err := os.WriteFile(path, []byte(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		if err := ensureOpenRCSSRustLogging(service); err != nil {
			t.Fatal(err)
		}
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != string(current) {
			t.Fatalf("upgraded script: %s / %v", contents, err)
		}
	}
	custom := legacy + "\necho custom hook\n"
	if err := os.WriteFile(path, []byte(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureOpenRCSSRustLogging(service); err == nil {
		t.Fatal("custom script was overwritten")
	}
	contents, _ := os.ReadFile(path)
	if string(contents) != custom {
		t.Fatal("refused custom script was modified")
	}
}
