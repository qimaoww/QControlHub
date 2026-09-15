//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func managedCoreUnitLines(engine core.Engine, managed EngineSpec) []string {
	addressFamilies := "AF_UNIX AF_INET AF_INET6"
	documentation := "https://github.com/MetaCubeX/mihomo"
	execStart := managed.Binary + " -d /var/lib/qcontrolhub-mihomo -f " + managed.ConfigPath
	extraServiceLines := []string{}
	switch engine {
	case core.EngineXray:
		documentation = "https://github.com/XTLS/Xray-core"
		execStart = managed.Binary + " run -config " + managed.ConfigPath
	case core.EngineSingBox:
		addressFamilies += " AF_NETLINK"
		documentation = "https://github.com/SagerNet/sing-box"
		execStart = managed.Binary + " run -c " + managed.ConfigPath
	case core.EngineShadowsocksRust:
		documentation = "https://github.com/shadowsocks/shadowsocks-rust"
		execStart = managed.Binary + " -c " + managed.ConfigPath + " --acl " + shadowsocksRustACLPath
		extraServiceLines = append(extraServiceLines, "Environment=RUST_LOG="+managedSSRustLogFilter)
	}
	stateDirectory := "/var/lib/qcontrolhub-" + managedCoreAssetName(engine)
	lines := []string{
		"[Unit]", "Description=" + engineDisplayName(engine) + " core managed by QAgent",
		"Documentation=" + documentation, "Wants=network-online.target", "After=network-online.target",
		"ConditionFileIsExecutable=" + managed.Binary, "ConditionPathExists=" + managed.ConfigPath,
		"[Service]", "Type=simple", "User=qcontrolhub-core", "Group=qcontrolhub-core",
		"WorkingDirectory=" + stateDirectory, "StateDirectory=qcontrolhub-" + managedCoreAssetName(engine),
		"StateDirectoryMode=0750", "UMask=0027", "ExecStart=" + execStart,
	}
	if engine == core.EngineShadowsocksRust {
		conditionIndex := 6
		lines = append(lines[:conditionIndex+1], append([]string{"ConditionPathExists=" + shadowsocksRustACLPath}, lines[conditionIndex+1:]...)...)
	}
	lines = append(lines, extraServiceLines...)
	capabilities := "CAP_NET_BIND_SERVICE"
	if engine != core.EngineXray {
		capabilities += " CAP_NET_ADMIN"
	}
	lines = append(lines,
		"LogNamespace=qagent-cores", "StandardOutput=journal", "StandardError=journal",
		"Restart=on-failure", "RestartSec=3s", "TimeoutStopSec=20s", "NoNewPrivileges=true",
		"CapabilityBoundingSet="+capabilities, "AmbientCapabilities="+capabilities,
		"ProtectSystem=strict", "ProtectHome=true", "PrivateTmp=true", "PrivateDevices=true",
		"ProtectKernelTunables=true", "ProtectKernelModules=true", "ProtectKernelLogs=true",
		"ProtectControlGroups=true", "ProtectClock=true", "RestrictSUIDSGID=true",
		"LockPersonality=true", "MemoryDenyWriteExecute=true", "RestrictNamespaces=true",
		"RestrictRealtime=true", "RemoveIPC=true", "ProtectProc=invisible", "ProcSubset=pid",
		"RestrictAddressFamilies="+addressFamilies, "SystemCallArchitectures=native",
		"ReadOnlyPaths="+managed.Binary+" "+filepath.Dir(managed.ConfigPath),
		"ReadWritePaths="+stateDirectory, "[Install]", "WantedBy=multi-user.target",
	)
	return lines
}

func validateManagedUnitFragment(contents []byte, engine core.Engine, managed EngineSpec) error {
	expected := managedCoreUnitLines(engine, managed)
	actual := make([]string, 0, len(expected))
	for _, rawLine := range strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		// Accept the exact historical info-only template for safe upgrades.
		if engine == core.EngineShadowsocksRust && line == "Environment=RUST_LOG=info" {
			line = "Environment=RUST_LOG=" + managedSSRustLogFilter
		}
		if engine != core.EngineXray && (line == "CapabilityBoundingSet=CAP_NET_BIND_SERVICE" || line == "AmbientCapabilities=CAP_NET_BIND_SERVICE") {
			line += " CAP_NET_ADMIN"
		}
		actual = append(actual, line)
	}
	if managedUnitLinesEqual(actual, expected) {
		return nil
	}

	// QControlHub releases before the log namespace and low-port capability
	// hardening wrote this exact unit shape. It remains project-owned and keeps
	// the same executable, user, filesystem, and sandbox contract. Accept only
	// that historical template; arbitrary missing or extra directives still fail.
	legacyOmissions := map[string]struct{}{
		"LogNamespace=qagent-cores":                                {},
		"StandardOutput=journal":                                   {},
		"StandardError=journal":                                    {},
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE":               {},
		"AmbientCapabilities=CAP_NET_BIND_SERVICE":                 {},
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_ADMIN": {},
		"AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN":   {},
	}
	legacy := make([]string, 0, len(expected)-len(legacyOmissions))
	for _, line := range expected {
		if _, omitted := legacyOmissions[line]; !omitted {
			legacy = append(legacy, line)
		}
	}
	if managedUnitLinesEqual(actual, legacy) {
		return nil
	}
	if engine == core.EngineShadowsocksRust {
		oldExecStart := "ExecStart=" + managed.Binary + " -c " + managed.ConfigPath
		newExecStart := oldExecStart + " --acl " + shadowsocksRustACLPath
		old := make([]string, 0, len(expected)-1)
		for _, line := range expected {
			if line == "ConditionPathExists="+shadowsocksRustACLPath {
				continue
			}
			if line == newExecStart {
				line = oldExecStart
			}
			old = append(old, line)
		}
		if managedUnitLinesEqual(actual, old) {
			return nil
		}
		oldWithoutLegacyHardening := make([]string, 0, len(old)-len(legacyOmissions))
		for _, line := range old {
			if _, omitted := legacyOmissions[line]; !omitted {
				oldWithoutLegacyHardening = append(oldWithoutLegacyHardening, line)
			}
		}
		if managedUnitLinesEqual(actual, oldWithoutLegacyHardening) {
			return nil
		}
	}
	return errors.New("managed service unit contains an unknown, missing, duplicate, or unsupported historical directive")
}

func managedUnitLinesEqual(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

func validateManagedServiceExecutionContext(ctx context.Context, engine core.Engine, managed EngineSpec) error {
	workingDirectory := "/var/lib/qcontrolhub-" + managedCoreAssetName(engine)
	expectedEnvironment := ""
	if engine == core.EngineShadowsocksRust {
		expectedEnvironment = "RUST_LOG=" + managedSSRustLogFilter
	}
	for property, expected := range map[string]string{
		"Type": "simple", "WorkingDirectory": workingDirectory, "RootDirectory": "",
		"RootImage": "", "BindPaths": "", "BindReadOnlyPaths": "", "Environment": expectedEnvironment, "EnvironmentFiles": "",
	} {
		value, err := systemdUnitProperty(ctx, managed.Service, property)
		if err == nil && engine == core.EngineShadowsocksRust && property == "Environment" && value == "RUST_LOG=info" {
			continue
		}
		if err != nil || value != expected {
			return fmt.Errorf("managed service effective %s is not %q", property, expected)
		}
	}
	return nil
}

func validateManagedUnitDropIns(ctx context.Context, service string) error {
	dropInValue, err := systemdUnitProperty(ctx, service, "DropInPaths")
	if err != nil {
		return fmt.Errorf("managed service effective DropInPaths cannot be read: %w", err)
	}
	allowed := map[string][][]byte{
		filepath.Join(existingDiscoveryManagedUnitRoot, service+".d", "10-qcontrolhub-bind-low-ports.conf"): managedCapabilityDropInVariants(service),
		filepath.Join(existingDiscoveryManagedUnitRoot, service+".d", "20-qcontrolhub-volatile-logs.conf"): {
			[]byte(managedCoreLogDropIn),
		},
	}
	if fallback, fallbackErr := managedCoreLogFallbackDropIn(service); fallbackErr == nil {
		path := filepath.Join(existingDiscoveryManagedUnitRoot, service+".d", "20-qcontrolhub-volatile-logs.conf")
		allowed[path] = append(allowed[path], fallback)
	}
	if service == "qagent-shadowsocks-rust.service" {
		allowed[filepath.Join(existingDiscoveryManagedUnitRoot, service+".d", "30-qcontrolhub-ss-rust-logs.conf")] = [][]byte{[]byte(managedSSRustLogDropIn)}
	}
	for _, path := range strings.Fields(dropInValue) {
		expected, ok := allowed[path]
		if !ok {
			return fmt.Errorf("managed service has an unknown drop-in %q", path)
		}
		if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
			return err
		}
		if err := validateManagedUnitFile(path); err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil || !matchesAnyManagedDropIn(contents, expected) {
			return fmt.Errorf("managed service drop-in %q is not project-managed", path)
		}
	}
	return nil
}

func matchesAnyManagedDropIn(contents []byte, expected [][]byte) bool {
	for _, candidate := range expected {
		if string(contents) == string(candidate) {
			return true
		}
	}
	return false
}

func supportedManagedExecStart(engine core.Engine, managed EngineSpec, executable, argv string) bool {
	if executable != managed.Binary {
		return false
	}
	switch engine {
	case core.EngineMihomo:
		return argv == managed.Binary+" -d /var/lib/qcontrolhub-mihomo -f "+managed.ConfigPath
	case core.EngineXray:
		return argv == managed.Binary+" run -config "+managed.ConfigPath
	case core.EngineSingBox:
		return argv == managed.Binary+" run -c "+managed.ConfigPath
	case core.EngineShadowsocksRust:
		return argv == managed.Binary+" -c "+managed.ConfigPath ||
			argv == managed.Binary+" -c "+managed.ConfigPath+" --acl "+shadowsocksRustACLPath
	default:
		return false
	}
}

func systemdUnitProperty(ctx context.Context, service, property string) (string, error) {
	output, err := run(ctx, systemctlPath, "show", service, "--property="+property, "--value")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}
