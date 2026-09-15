package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestProductionAgentUnitAllowsOnlyRequiredCapabilities(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("../../deploy/systemd/qagent.service")
	if err != nil {
		t.Fatal(err)
	}
	unit := string(contents)
	requiredCapabilities := "CAP_CHOWN CAP_NET_ADMIN"
	if !strings.Contains(unit, "CapabilityBoundingSet="+requiredCapabilities) || !strings.Contains(unit, "AmbientCapabilities="+requiredCapabilities) {
		t.Fatal("production Agent unit does not retain the metadata and traffic-accounting capabilities")
	}
	for _, forbidden := range []string{"CAP_SYS_ADMIN", "CAP_SYS_PTRACE", "CAP_DAC_OVERRIDE", "CAP_NET_RAW", "CAP_SETUID", "CAP_SETGID"} {
		if strings.Contains(unit, forbidden) {
			t.Fatalf("production Agent unit grants unnecessary capability %s", forbidden)
		}
	}
	if !strings.Contains(unit, "ReadWritePaths=-/usr/local/lib/qagent/cores") {
		t.Fatal("production Agent unit does not allow updates in the private core directory")
	}
	if strings.Contains(unit, "ReadWritePaths=-/usr/local/bin\n") {
		t.Fatal("production Agent unit can modify administrator-managed /usr/local/bin programs")
	}
	if !strings.Contains(unit, "ProtectProc=invisible") {
		t.Fatal("production Agent unit must hide process details")
	}
	if strings.Contains(unit, "ProcSubset=pid") {
		t.Fatal("production Agent unit hides /proc/stat, /proc/meminfo, and /proc/net metrics")
	}
	if !strings.Contains(unit, "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK") {
		t.Fatal("production Agent unit does not allow the nftables netlink transport")
	}
}

func TestDefaultSpecsUsePrivateQAgentNamespace(t *testing.T) {
	t.Parallel()
	want := map[core.Engine]EngineSpec{
		core.EngineMihomo:          {Binary: "/usr/local/lib/qagent/cores/mihomo", ConfigPath: "/etc/qagent/mihomo/config.yaml", Service: "qagent-mihomo.service"},
		core.EngineXray:            {Binary: "/usr/local/lib/qagent/cores/xray", ConfigPath: "/etc/qagent/xray/config.json", Service: "qagent-xray.service"},
		core.EngineSingBox:         {Binary: "/usr/local/lib/qagent/cores/sing-box", ConfigPath: "/etc/qagent/sing-box/config.json", Service: "qagent-sing-box.service"},
		core.EngineShadowsocksRust: {Binary: "/usr/local/lib/qagent/cores/ssserver", ConfigPath: "/etc/qagent/shadowsocks-rust/config.json", Service: "qagent-shadowsocks-rust.service"},
	}
	for engine, expected := range want {
		actual := DefaultSpecs()[engine]
		if actual != expected {
			t.Errorf("DefaultSpecs()[%s] = %+v, want %+v", engine, actual, expected)
		}
		unitPath := filepath.Join("../../deploy/systemd", expected.Service)
		contents, err := os.ReadFile(unitPath)
		if err != nil {
			t.Errorf("read %s: %v", unitPath, err)
			continue
		}
		unit := string(contents)
		if err := validateManagedUnitFragment(contents, engine, expected); err != nil {
			t.Errorf("%s does not match the runtime-owned unit contract: %v", expected.Service, err)
		}
		for _, required := range []string{
			"ConditionFileIsExecutable=" + expected.Binary,
			"ConditionPathExists=" + expected.ConfigPath,
			"ExecStart=" + expected.Binary,
			"CapabilityBoundingSet=CAP_NET_BIND_SERVICE",
			"AmbientCapabilities=CAP_NET_BIND_SERVICE",
			"LogNamespace=qagent-cores",
			"StandardOutput=journal",
			"StandardError=journal",
		} {
			if !strings.Contains(unit, required) {
				t.Errorf("%s is missing %q", expected.Service, required)
			}
		}
		for _, forbidden := range []string{"CAP_NET_ADMIN", "CAP_NET_RAW", "CAP_SYS_ADMIN", "CAP_DAC_OVERRIDE"} {
			if forbidden == "CAP_NET_ADMIN" && engine != core.EngineXray {
				continue
			}
			if strings.Contains(unit, forbidden) {
				t.Errorf("%s grants unnecessary capability %s", expected.Service, forbidden)
			}
		}
	}
	journalConfig, err := os.ReadFile("../../deploy/systemd/qagent-core-journal.conf")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"# qcontrolhub-node-budget-v2", "Storage=volatile", "RuntimeMaxUse=524288", "RuntimeMaxFileSize=524288", "MaxRetentionSec=15min"} {
		if !strings.Contains(string(journalConfig), required) {
			t.Errorf("core journal configuration is missing %q", required)
		}
	}
	agentUnit, err := os.ReadFile("../../deploy/systemd/qagent.service")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"RuntimeDirectory=qagent-core-logs", "RuntimeDirectoryMode=0700", "RuntimeDirectoryPreserve=yes"} {
		if !strings.Contains(string(agentUnit), required) {
			t.Errorf("qagent service is missing %q", required)
		}
	}
}

func TestOpenRCSpecsAndServicesUsePrivateQAgentNamespace(t *testing.T) {
	t.Parallel()
	for engine, spec := range DefaultSpecsForServiceManager(ServiceManagerOpenRC) {
		if strings.HasSuffix(spec.Service, ".service") {
			t.Errorf("OpenRC service for %s retains systemd suffix: %s", engine, spec.Service)
		}
		contents, err := os.ReadFile(filepath.Join("../../deploy/openrc", spec.Service))
		if err != nil {
			t.Errorf("read OpenRC service %s: %v", spec.Service, err)
			continue
		}
		script := string(contents)
		capabilities := "capabilities=\"^cap_net_bind_service,^cap_net_admin\""
		for _, required := range []string{"#!/sbin/openrc-run", "# QControlHub managed OpenRC service:", "supervisor=\"supervise-daemon\"", capabilities, spec.Binary} {
			if !strings.Contains(script, required) {
				t.Errorf("OpenRC service %s is missing %q", spec.Service, required)
			}
		}
	}
	agentService, err := os.ReadFile("../../deploy/openrc/qagent")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"command=\"/usr/local/lib/qagent/qagent\"", "command_user=\"root:root\"", "respawn_delay=5", "capabilities=\"^cap_chown,^cap_setgid,^cap_setuid\"", "qagent_capability_is_bound 12", "capabilities=\"${capabilities},^cap_net_admin\"", "no_new_privs=true"} {
		if !strings.Contains(string(agentService), required) {
			t.Errorf("OpenRC Agent service is missing %q", required)
		}
	}
}

func TestCoreBootstrapDoesNotTouchLegacyInstallations(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("../../deploy/bootstrap-core-services.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	for _, required := range []string{"/usr/local/lib/qagent/cores", "/etc/systemd/system/$managed_service", "install_managed_unit", "preserved non-QAgent unit", "install_managed_openrc_service", "rc-update add"} {
		if !strings.Contains(script, required) {
			t.Errorf("core bootstrap is missing private namespace %q", required)
		}
	}
	for _, forbidden := range []string{"/usr/local/bin/mihomo", "/usr/local/bin/xray", "managed by QControlHub", "legacy_service"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("core bootstrap unexpectedly touches legacy installation %q", forbidden)
		}
	}
}

func TestOneClickInstallerMapsOnlyValidatedExistingCorePaths(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("../../deploy/remote/install-agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := os.ReadFile("../../deploy/existing-core-mapping.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents) + string(mapping)
	for _, required := range []string{
		"discover_existing_xray", "discover_existing_singbox", "systemctl is-active --quiet",
		"existing-core-mapping.sh", "QCH_EXISTING_XRAY_CONFIG", "QCH_EXISTING_SING_BOX_CONFIG",
		"rc-service \"$service\" status", "openrc_supervised_child_pid",
		"QCH_SERVICE_MANAGER", "apk add --no-cache", "deploy/openrc",
		"install_nftables", "apt-get install -y --no-install-recommends nftables",
		"dnf install -y nftables", "zypper --non-interactive install nftables",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("one-click installer is missing inherited-core guard %q", required)
		}
	}
	for _, forbidden := range []string{"systemctl stop xray.service", "systemctl stop sing-box.service", "QCH_INHERIT_CONFIGS", "validate-inherited", "/proc/[0-9]*"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("one-click installer stops an existing service via %q", forbidden)
		}
	}
}

func TestOneClickInstallerDefersManagedCoreServicesUntilPanelInstall(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("../../deploy/remote/install-agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	for _, required := range []string{
		`bootstrap-core-services.sh" --prepare-agent`,
		`core_asset_root=${QCH_CORE_ASSET_ROOT:-/usr/local/share/qcontrolhub/core-install}`,
		`stage_core_asset "$repository_dir/deploy/$service_manager/$service_asset"`,
		`qagent_bin_dir=${QCH_AGENT_BIN_DIR:-/usr/local/lib/qagent}`,
		`"$qagent_bin_dir/cores"`,
		`existing $label service was left unchanged because it could not be mapped safely`,
		`only this core's remote tasks will remain disabled`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("one-click installer is missing deferred-core contract %q", required)
		}
	}
	if strings.Contains(script, `QCH_SKIP_CORE_SERVICES="$mapped_engines" sh "$repository_dir/deploy/bootstrap-core-services.sh"`) {
		t.Fatal("one-click installer still installs all managed core services during Agent deployment")
	}
	if strings.Contains(script, `unsafe $label service state; installation stopped`) {
		t.Fatal("one-click installer still blocks Agent enrollment for an untouched, unmappable existing core")
	}

	bootstrap, err := os.ReadFile("../../deploy/bootstrap-core-services.sh")
	if err != nil {
		t.Fatal(err)
	}
	bootstrapScript := string(bootstrap)
	for _, required := range []string{
		`--prepare-agent) selected_engines=""`,
		`mihomo|xray|sing-box|shadowsocks-rust) selected_engines=$requested_engine`,
		`for engine in $selected_engines`,
	} {
		if !strings.Contains(bootstrapScript, required) {
			t.Errorf("core bootstrap is missing selective-install contract %q", required)
		}
	}
}

func TestManagedCoreBootstrapCapabilities(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy.sh")
	if err := os.WriteFile(legacy, []byte("#!/bin/sh\n"+legacyCoreStateDirectoryInstall+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	capabilities, err := managedCoreBootstrapCapabilities(legacy)
	if err != nil || capabilities != "CAP_CHOWN CAP_FOWNER" {
		t.Fatalf("legacy bootstrap capabilities = %q, %v", capabilities, err)
	}
	current := filepath.Join(root, "current.sh")
	if err := os.WriteFile(current, []byte("#!/bin/sh\nensure_service_state_directory \"$state_directory\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	capabilities, err = managedCoreBootstrapCapabilities(current)
	if err != nil || capabilities != "CAP_CHOWN" {
		t.Fatalf("current bootstrap capabilities = %q, %v", capabilities, err)
	}
}
