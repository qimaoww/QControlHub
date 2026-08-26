//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const existingCoreDiscoveryStateVersion = 1

type existingDiscoveryCandidateSet struct {
	services          []string
	executables       []string
	directExecutables []string
	configs           []string
}

// Installer-owned core layouts that ship the real binary inside the
// configuration tree rather than a shared bin directory. Each is only ever
// accepted as a direct, non-symlinked, root-owned executable.
const (
	protectedEtcSingBoxExecutable   = "/etc/sing-box/bin/sing-box"
	protectedEtcXrayExecutable      = "/etc/xray/bin/xray"
	protectedAgentXrayExecutable    = "/etc/v2ray-agent/xray/xray"
	protectedAgentSingBoxExecutable = "/etc/v2ray-agent/sing-box/sing-box"
)

var existingDiscoveryCandidates = map[core.Engine]existingDiscoveryCandidateSet{
	core.EngineXray: {
		services: []string{"xray.service"},
		executables: []string{
			"/usr/local/bin/xray", "/usr/bin/xray",
			protectedEtcXrayExecutable, protectedAgentXrayExecutable,
		},
		directExecutables: []string{protectedEtcXrayExecutable, protectedAgentXrayExecutable},
		configs:           []string{"/usr/local/etc/xray/config.json", "/etc/xray/config.json"},
	},
	core.EngineSingBox: {
		services: []string{"sing-box.service", "singbox.service"},
		executables: []string{
			"/usr/local/bin/sing-box", "/usr/bin/sing-box",
			protectedEtcSingBoxExecutable, protectedAgentSingBoxExecutable,
		},
		directExecutables: []string{protectedEtcSingBoxExecutable, protectedAgentSingBoxExecutable},
		configs: []string{
			"/etc/sing-box/config.json", "/usr/local/etc/sing-box/config.json",
			"/etc/v2ray-agent/sing-box/conf/config.json",
		},
	},
}

var installerOrphanOwnerExecutables = map[string]struct{}{
	protectedEtcSingBoxExecutable:   {},
	protectedEtcXrayExecutable:      {},
	protectedAgentXrayExecutable:    {},
	protectedAgentSingBoxExecutable: {},
}

func installerCoreAllowsOrphanOwner(path string) bool {
	_, ok := installerOrphanOwnerExecutables[filepath.Clean(path)]
	return ok
}

var existingDiscoveryManagedUnitRoot = "/etc/systemd/system"

type existingCoreDiscoveryState struct {
	Version int                                   `json:"version"`
	Specs   map[core.Engine]existingDiscoverySpec `json:"specs,omitempty"`
	Issues  map[core.Engine]string                `json:"issues,omitempty"`
}

type existingDiscoverySpec struct {
	Binary           string `json:"binary"`
	ConfigPath       string `json:"config_path"`
	ConfigDirectory  string `json:"config_directory,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
	ServiceBinary    string `json:"service_binary,omitempty"`
	Service          string `json:"service"`
}

func discoverySpecFromEngineSpec(spec EngineSpec) existingDiscoverySpec {
	return existingDiscoverySpec{
		Binary: spec.Binary, ConfigPath: spec.ConfigPath, ConfigDirectory: spec.ConfigDirectory,
		WorkingDirectory: spec.WorkingDirectory, ServiceBinary: spec.ServiceBinary, Service: spec.Service,
	}
}

func (spec existingDiscoverySpec) engineSpec() EngineSpec {
	return EngineSpec{
		Binary: spec.Binary, ConfigPath: spec.ConfigPath, ConfigDirectory: spec.ConfigDirectory,
		WorkingDirectory: spec.WorkingDirectory, ServiceBinary: spec.ServiceBinary, Service: spec.Service,
	}
}

// RefreshExistingCoreDiscovery performs the same fail-closed, read-only
// mapping used by a fresh installer. It persists only mappings discovered by
// the Agent; explicit QCH_EXISTING_* mappings are never replaced and always
// take precedence. A persisted mapping is reused only to reconcile an
// in-progress migration whose original service may already be inactive.
func RefreshExistingCoreDiscovery(
	ctx context.Context,
	discoveryStatePath string,
	migrationMarkerPrefix string,
	managedSpecs map[core.Engine]EngineSpec,
	manualSpecs map[core.Engine]EngineSpec,
	managers ...*ServiceManager,
) (map[core.Engine]EngineSpec, map[core.Engine]string, error) {
	manager := selectedServiceManager(managers...)
	if err := os.MkdirAll(filepath.Dir(discoveryStatePath), 0o700); err != nil {
		return nil, nil, fmt.Errorf("create existing-core discovery state directory: %w", err)
	}
	if err := validateStateDirectory(filepath.Dir(discoveryStatePath)); err != nil {
		return nil, nil, err
	}
	previous, err := loadExistingCoreDiscoveryState(discoveryStatePath, manager)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("load existing-core discovery state: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		previous = existingCoreDiscoveryState{Version: existingCoreDiscoveryStateVersion}
	}
	validationRoot, err := os.MkdirTemp(filepath.Dir(discoveryStatePath), ".existing-core-discovery-")
	if err != nil {
		return nil, nil, fmt.Errorf("create protected existing-core validation directory: %w", err)
	}
	defer os.RemoveAll(validationRoot)

	automatic := make(map[core.Engine]EngineSpec)
	issues := make(map[core.Engine]string)
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		if _, explicit := manualSpecs[engine]; explicit {
			continue
		}
		managed, enabled := managedSpecs[engine]
		if !enabled {
			continue
		}
		record, recordErr := readCoreMigrationRecord(migrationMarkerPrefix, engine)
		if recordErr != nil {
			return nil, nil, fmt.Errorf("read %s migration state during discovery: %w", engine, recordErr)
		}
		if record.State == coreMigrationInProgress || record.State == coreMigrationComplete {
			previousSpec, ok := previous.Specs[engine]
			if !ok || record.SourceDigest != coreMigrationSourceDigest(previousSpec.engineSpec()) {
				return nil, nil, fmt.Errorf("persisted %s discovery mapping does not match the recorded migration", engine)
			}
			automatic[engine] = previousSpec.engineSpec()
			continue
		}
		spec, found, issue := discoverExistingCoreService(ctx, engine, managed, filepath.Join(validationRoot, string(engine)), manager)
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("discover existing %s service: %w", engine, ctx.Err())
		}
		if found {
			automatic[engine] = spec
			continue
		}
		if issue != "" {
			issues[engine] = limitDiscoveryIssue(issue)
		}
	}

	state := existingCoreDiscoveryState{
		Version: existingCoreDiscoveryStateVersion,
		Specs:   make(map[core.Engine]existingDiscoverySpec, len(automatic)),
		Issues:  issues,
	}
	for engine, spec := range automatic {
		state.Specs[engine] = discoverySpecFromEngineSpec(spec)
	}
	if err := saveExistingCoreDiscoveryState(discoveryStatePath, state); err != nil {
		return nil, nil, fmt.Errorf("persist existing-core discovery state: %w", err)
	}

	result := make(map[core.Engine]EngineSpec, len(manualSpecs)+len(automatic))
	for engine, spec := range automatic {
		result[engine] = spec
	}
	for engine, spec := range manualSpecs {
		result[engine] = spec
		delete(issues, engine)
	}
	return result, issues, nil
}

func discoverExistingCoreService(ctx context.Context, engine core.Engine, managed EngineSpec, validationDirectory string, managers ...*ServiceManager) (EngineSpec, bool, string) {
	manager := selectedServiceManager(managers...)
	candidates := existingDiscoveryCandidates[engine]
	services := candidates.services
	if manager.Kind() == ServiceManagerOpenRC {
		services = make([]string, 0, len(candidates.services))
		for _, service := range candidates.services {
			services = append(services, strings.TrimSuffix(service, ".service"))
		}
	}
	activeServices := make([]string, 0, len(services))
	for _, service := range services {
		status, err := serviceStatusWithManager(ctx, manager, service)
		if err != nil {
			continue
		}
		if status == "active" {
			activeServices = append(activeServices, service)
		}
	}
	if len(activeServices) == 0 {
		return EngineSpec{}, false, ""
	}
	if len(activeServices) != 1 {
		return EngineSpec{}, false, fmt.Sprintf("检测到多个活动的标准 %s 服务，自动迁移已安全禁用", engine)
	}
	service := activeServices[0]
	if manager.Kind() == ServiceManagerOpenRC {
		spec, err := discoverOpenRCExistingSpec(ctx, engine, service, candidates)
		if err != nil {
			return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s OpenRC 服务，但缺少可证明归属该服务的受保护 supervise-daemon 进程身份，或其二进制与参数不受支持", engine)
		}
		return validateDiscoveredExistingSpec(ctx, engine, managed, spec, validationDirectory, manager)
	}
	execStart, err := run(ctx, systemctlPath, "show", service, "--property=ExecStart", "--value")
	if err != nil {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但无法读取其唯一 ExecStart", engine)
	}
	executable, argv, err := parseSingleSystemdExecStart(execStart)
	if err != nil {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但 ExecStart 包含多命令或结构不受支持", engine)
	}
	if !stringInSlice(executable, candidates.executables) {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但 executable 不在受支持的标准路径", engine)
	}
	configPath, configDirectory, workDirectory, ok := parseDiscoveredExistingArgv(engine, executable, argv, candidates.configs)
	if !ok {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但 ExecStart 参数不属于受支持的精确配置形式", engine)
	}
	realBinary := ""
	if stringInSlice(executable, candidates.directExecutables) {
		if err := validateProtectedDirectoryChain(filepath.Dir(executable)); err != nil {
			return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务和标准 executable，但 executable 未通过 root 所有、非符号链接、不可组/其他写和原生二进制安全校验", engine)
		}
		if err := validateExistingCoreExecutable(executable); err != nil {
			return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务和标准 executable，但 executable 未通过 root 所有、非符号链接、不可组/其他写和原生二进制安全校验", engine)
		}
		realBinary = executable
	} else {
		var err error
		realBinary, err = resolveDiscoveredExistingBinary(executable)
		if err != nil {
			return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务和配置，但 executable wrapper 不在安全支持范围；请改为真实二进制、一跳二进制链接或固定 exec 转发器", engine)
		}
	}
	spec := EngineSpec{
		Binary: realBinary, ConfigPath: configPath, ConfigDirectory: configDirectory,
		WorkingDirectory: workDirectory, ServiceBinary: executable, Service: service,
	}
	return validateDiscoveredExistingSpec(ctx, engine, managed, spec, validationDirectory, manager)
}

func validateDiscoveredExistingSpec(ctx context.Context, engine core.Engine, managed, spec EngineSpec, validationDirectory string, manager *ServiceManager) (EngineSpec, bool, string) {
	managedStatus, err := validateManagedServiceForExistingDiscovery(ctx, engine, managed, manager)
	if err != nil {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但 QAgent 专用服务不是受支持的安全 unit", engine)
	}
	if err := verifyExistingServiceMapping(ctx, engine, spec, manager); err != nil {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但映射在核验期间发生变化", engine)
	}
	validationSpec := managed
	validationSpec.ConfigPath = filepath.Join(validationDirectory, "config.json")
	probe := &Executor{Specs: map[core.Engine]EngineSpec{engine: validationSpec}, ExistingSpecs: map[core.Engine]EngineSpec{engine: spec}, Services: manager}
	if _, err := probe.readExistingConfig(ctx, engine, validationSpec, spec); err != nil {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但配置源未通过受保护路径与真实内核校验", engine)
	}
	if err := verifyExistingServiceMapping(ctx, engine, spec, manager); err != nil {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但映射在配置核验期间发生变化", engine)
	}
	managedStatusAfter, err := validateManagedServiceForExistingDiscovery(ctx, engine, managed, manager)
	if err != nil || managedStatusAfter != managedStatus {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但 QAgent 专用服务在配置核验期间发生变化", engine)
	}
	return spec, true, ""
}

func discoverOpenRCExistingSpec(ctx context.Context, engine core.Engine, service string, candidates existingDiscoveryCandidateSet) (EngineSpec, error) {
	identity, err := boundOpenRCServiceProcess(ctx, service)
	if err != nil {
		return EngineSpec{}, err
	}
	serviceBinary, realBinary, ok := matchDiscoveredOpenRCExecutable(identity.Child.Executable, identity.Child.Argv[0], candidates.executables)
	if !ok {
		return EngineSpec{}, errors.New("service-bound OpenRC process executable is not supported")
	}
	configPath, configDirectory, ok := parseDiscoveredOpenRCArgv(engine, identity.Child.Argv, candidates.configs)
	if !ok {
		return EngineSpec{}, errors.New("service-bound OpenRC process arguments are not supported")
	}
	return EngineSpec{
		Binary: realBinary, ConfigPath: configPath, ConfigDirectory: configDirectory,
		ServiceBinary: serviceBinary, Service: service,
	}, nil
}

func matchDiscoveredOpenRCExecutable(processExecutable, argv0 string, candidates []string) (string, string, bool) {
	type resolvedCandidate struct {
		serviceBinary string
		realBinary    string
	}
	resolved := make([]resolvedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		realBinary, err := resolveDiscoveredExistingBinary(candidate)
		if err == nil && realBinary == filepath.Clean(processExecutable) {
			resolved = append(resolved, resolvedCandidate{serviceBinary: candidate, realBinary: realBinary})
		}
	}
	for _, candidate := range resolved {
		if argv0 == candidate.serviceBinary {
			return candidate.serviceBinary, candidate.realBinary, true
		}
	}
	if len(resolved) == 1 && argv0 == resolved[0].realBinary {
		return resolved[0].serviceBinary, resolved[0].realBinary, true
	}
	return "", "", false
}

// parseDiscoveredOpenRCArgv maps a supervise-daemon child's argv onto the same
// layouts the systemd path accepts. The official sing-box working-directory
// form is deliberately excluded: it is a systemd packaging shape, and an OpenRC
// service presenting it has no supervised binding this mapping could prove.
func parseDiscoveredOpenRCArgv(engine core.Engine, argv, configs []string) (string, string, bool) {
	if len(argv) == 0 {
		return "", "", false
	}
	configPath, configDirectory, workDirectory, ok := matchDiscoveredExistingArgv(engine, argv[0], argv, configs)
	if !ok || workDirectory != "" {
		return "", "", false
	}
	return configPath, configDirectory, true
}

func validateManagedServiceForExistingDiscovery(ctx context.Context, engine core.Engine, managed EngineSpec, managers ...*ServiceManager) (string, error) {
	manager := selectedServiceManager(managers...)
	defaultSpec, ok := DefaultSpecsForServiceManager(manager.Kind())[engine]
	if !ok || managed != defaultSpec {
		return "", errors.New("managed service mapping is not the QAgent default")
	}
	if manager.Kind() == ServiceManagerOpenRC {
		if _, err := os.Lstat(filepath.Join(openRCInitRoot, managed.Service)); errors.Is(err, os.ErrNotExist) {
			return "not-found", nil
		} else if err != nil {
			return "", err
		}
		status, err := serviceStatusWithManager(ctx, manager, managed.Service)
		if err != nil || (status != "inactive" && status != "failed") {
			return "", errors.New("managed OpenRC service is not inactive or failed")
		}
		marker := "# QControlHub managed OpenRC service: " + managed.Service
		if err := validateOpenRCServiceScript(managed.Service, marker); err != nil {
			return "", err
		}
		return status, nil
	}
	loadState, err := run(ctx, systemctlPath, "show", managed.Service, "--property=LoadState", "--value")
	if strings.TrimSpace(loadState) == "not-found" {
		return "not-found", nil
	}
	if err != nil || strings.TrimSpace(loadState) != "loaded" {
		return "", errors.New("managed service unit is not loaded")
	}
	status, err := serviceStatusWithManager(ctx, manager, managed.Service)
	if err != nil || (status != "active" && status != "inactive" && status != "failed") {
		return "", errors.New("managed service has an unsupported state")
	}
	fragmentPath, err := run(ctx, systemctlPath, "show", managed.Service, "--property=FragmentPath", "--value")
	expectedFragmentPath := filepath.Join(existingDiscoveryManagedUnitRoot, managed.Service)
	if err != nil || strings.TrimSpace(fragmentPath) != expectedFragmentPath {
		return "", errors.New("managed service fragment path is not the QAgent-owned location")
	}
	fragmentPath = strings.TrimSpace(fragmentPath)
	if err := validateProtectedDirectoryChain(filepath.Dir(fragmentPath)); err != nil {
		return "", err
	}
	info, err := os.Lstat(fragmentPath)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("managed service unit file is unsafe")
	}
	if err := validateOwner(info, "managed service unit file"); err != nil {
		return "", err
	}
	contents, err := os.ReadFile(fragmentPath)
	if err != nil {
		return "", err
	}
	if err := validateManagedUnitFragment(contents, engine, managed); err != nil {
		return "", err
	}
	if err := validateManagedServiceExecutionContext(ctx, engine, managed); err != nil {
		return "", err
	}
	if err := validateManagedUnitDropIns(ctx, managed.Service); err != nil {
		return "", err
	}
	for property, expected := range map[string]string{"Description": engineDisplayName(engine) + " core managed by QAgent", "User": "qcontrolhub-core", "Group": "qcontrolhub-core"} {
		value, err := systemdUnitProperty(ctx, managed.Service, property)
		if err != nil || value != expected {
			return "", fmt.Errorf("managed service effective %s is not %q", property, expected)
		}
	}
	for _, property := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecReload", "ExecStop", "ExecStopPost"} {
		value, err := systemdUnitProperty(ctx, managed.Service, property)
		if err != nil || value != "" {
			return "", fmt.Errorf("managed service has an unsupported effective %s hook", property)
		}
	}
	execStart, err := run(ctx, systemctlPath, "show", managed.Service, "--property=ExecStart", "--value")
	if err != nil {
		return "", errors.New("managed service ExecStart cannot be read")
	}
	executable, argv, err := parseSingleSystemdExecStart(execStart)
	if err != nil || !supportedManagedExecStart(engine, managed, executable, argv) {
		return "", errors.New("managed service ExecStart is not the exact QAgent-owned command")
	}
	return status, nil
}

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
		execStart = managed.Binary + " -c " + managed.ConfigPath
		extraServiceLines = append(extraServiceLines, "Environment=RUST_LOG=info")
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
	lines = append(lines, extraServiceLines...)
	lines = append(lines,
		"LogNamespace=qagent-cores", "StandardOutput=journal", "StandardError=journal",
		"Restart=on-failure", "RestartSec=3s", "TimeoutStopSec=20s", "NoNewPrivileges=true",
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE", "AmbientCapabilities=CAP_NET_BIND_SERVICE",
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
		"LogNamespace=qagent-cores":                  {},
		"StandardOutput=journal":                     {},
		"StandardError=journal":                      {},
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE": {},
		"AmbientCapabilities=CAP_NET_BIND_SERVICE":   {},
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
		expectedEnvironment = "RUST_LOG=info"
	}
	for property, expected := range map[string]string{
		"Type": "simple", "WorkingDirectory": workingDirectory, "RootDirectory": "",
		"RootImage": "", "BindPaths": "", "BindReadOnlyPaths": "", "Environment": expectedEnvironment, "EnvironmentFiles": "",
	} {
		value, err := systemdUnitProperty(ctx, managed.Service, property)
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
	allowed := map[string][]byte{
		filepath.Join(existingDiscoveryManagedUnitRoot, service+".d", "10-qcontrolhub-bind-low-ports.conf"): []byte(managedCoreCapabilityDropIn),
		filepath.Join(existingDiscoveryManagedUnitRoot, service+".d", "20-qcontrolhub-volatile-logs.conf"):  []byte(managedCoreLogDropIn),
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
		if err != nil || string(contents) != string(expected) {
			return fmt.Errorf("managed service drop-in %q is not project-managed", path)
		}
	}
	return nil
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
		return argv == managed.Binary+" -c "+managed.ConfigPath
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

func engineDisplayName(engine core.Engine) string {
	switch engine {
	case core.EngineMihomo:
		return "Mihomo"
	case core.EngineXray:
		return "Xray"
	case core.EngineSingBox:
		return "sing-box"
	case core.EngineShadowsocksRust:
		return "Shadowsocks Rust"
	default:
		return string(engine)
	}
}

func managedCoreAssetName(engine core.Engine) string {
	if engine == core.EngineShadowsocksRust {
		return "shadowsocks-rust"
	}
	return string(engine)
}

func parseDiscoveredExistingArgv(engine core.Engine, executable, argv string, configs []string) (string, string, string, bool) {
	return matchDiscoveredExistingArgv(engine, executable, strings.Fields(argv), configs)
}

// matchDiscoveredExistingArgv maps one already-split command line onto a
// supported existing-core layout. A mapping without a main configuration file
// is directory-authoritative: there is nothing to match against the config
// whitelist, and the confdir alone decides the content. That directory still
// has to clear the protected path chain before anything in it is read.
func matchDiscoveredExistingArgv(engine core.Engine, executable string, fields, configs []string) (string, string, string, bool) {
	configPath, configDirectory, workDirectory, ok := parseExistingArgv(engine, executable, fields)
	if !ok {
		return "", "", "", false
	}
	if configPath == "" {
		if configDirectory == "" {
			return "", "", "", false
		}
		return "", configDirectory, workDirectory, true
	}
	for _, candidate := range configs {
		if configPath == candidate {
			return configPath, configDirectory, workDirectory, true
		}
	}
	return "", "", "", false
}

func parseExistingArgv(engine core.Engine, executable string, fields []string) (string, string, string, bool) {
	if len(fields) < 2 || fields[0] != executable {
		return "", "", "", false
	}
	switch engine {
	case core.EngineXray:
		configPath, configDirectory, ok := parseXrayExistingArgv(fields[1:])
		return configPath, configDirectory, "", ok
	case core.EngineSingBox:
		return parseSingBoxExistingArgv(fields[1:])
	default:
		return "", "", "", false
	}
}

// parseXrayExistingArgv recognizes the exact Xray invocation shapes that can be
// mapped safely: a single configuration file (run -config|-c <file>), a file
// combined with a confdir (run -config|-c <file> -confdir <dir>), and the
// directory-authoritative form used by installers that ship no main file at all
// (run -confdir <dir>), which reports an empty configuration path. Every path
// must be absolute and free of whitespace and the argument list must be exact;
// unknown flags or a repeated flag fail closed.
func parseXrayExistingArgv(args []string) (string, string, bool) {
	if len(args) == 0 || args[0] != "run" {
		return "", "", false
	}
	args = args[1:]
	configFlag := func(flag string) bool { return flag == "-config" || flag == "-c" }
	switch len(args) {
	case 2:
		if args[0] == "-confdir" {
			if !safeExistingAbsolutePath(args[1]) {
				return "", "", false
			}
			return "", args[1], true
		}
		if !configFlag(args[0]) || !safeExistingAbsolutePath(args[1]) {
			return "", "", false
		}
		return args[1], "", true
	case 4:
		if !configFlag(args[0]) || args[2] != "-confdir" {
			return "", "", false
		}
		if !safeExistingAbsolutePath(args[1]) || !safeExistingAbsolutePath(args[3]) {
			return "", "", false
		}
		return args[1], args[3], true
	}
	return "", "", false
}

// parseSingBoxExistingArgv recognizes the exact sing-box invocation shapes
// that can be mapped safely. It supports the legacy run-then-flag forms
// (run -c <file>, run --config <file>, run -c <file> -C <dir> and the
// --config spelling of the same directory form) and the official package form
// (-D <working-directory> -C <config-directory> run). Every path must be
// absolute and free of whitespace, and the argument list must be exact;
// unknown flags, repeated -D/-C, or an ambiguous relative path fail closed.
func parseSingBoxExistingArgv(args []string) (string, string, string, bool) {
	if len(args) == 0 {
		return "", "", "", false
	}
	switch args[0] {
	case "run":
		if len(args) == 3 && (args[1] == "-c" || args[1] == "--config") {
			configPath := args[2]
			if !safeExistingAbsolutePath(configPath) {
				return "", "", "", false
			}
			return configPath, "", "", true
		}
		if len(args) == 5 && (args[1] == "-c" || args[1] == "--config") && args[3] == "-C" {
			configPath := args[2]
			configDirectory := args[4]
			if !safeExistingAbsolutePath(configPath) || !safeExistingAbsolutePath(configDirectory) {
				return "", "", "", false
			}
			return configPath, configDirectory, "", true
		}
	case "-D":
		if len(args) != 5 || args[2] != "-C" || args[4] != "run" {
			return "", "", "", false
		}
		workDirectory := args[1]
		configDirectory := args[3]
		if !safeExistingAbsolutePath(workDirectory) || !safeExistingAbsolutePath(configDirectory) {
			return "", "", "", false
		}
		return filepath.Join(configDirectory, "config.json"), configDirectory, workDirectory, true
	}
	return "", "", "", false
}

func safeExistingAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && path != "" && !strings.ContainsAny(path, " \t\r\n")
}

func resolveDiscoveredExistingBinary(serviceBinary string) (string, error) {
	if !filepath.IsAbs(serviceBinary) || strings.ContainsAny(serviceBinary, " \t\r\n") {
		return "", errors.New("service executable path is unsafe")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(serviceBinary)); err != nil {
		return "", err
	}
	info, err := os.Lstat(serviceBinary)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if err := validateExistingCoreExecutable(serviceBinary); err != nil {
			return "", err
		}
		return serviceBinary, nil
	}
	target, err := os.Readlink(serviceBinary)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(serviceBinary), target)
	}
	target = filepath.Clean(target)
	targetInfo, err := os.Lstat(target)
	if err != nil {
		return "", err
	}
	if targetInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("service executable uses more than one symlink")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(target)); err != nil {
		return "", err
	}
	if err := validateExistingCoreExecutable(target); err == nil {
		return target, nil
	}
	contents, err := os.ReadFile(target)
	if err != nil || len(contents) > 1024 {
		return "", errors.New("service executable wrapper is unsupported")
	}
	lines := strings.Split(string(contents), "\n")
	if len(lines) != 3 || lines[0] != "#!/bin/sh" || lines[2] != "" {
		return "", errors.New("service executable wrapper is not the fixed two-line form")
	}
	const prefix = "exec "
	const suffix = " \"$@\""
	if !strings.HasPrefix(lines[1], prefix) || !strings.HasSuffix(lines[1], suffix) {
		return "", errors.New("service executable wrapper is not an unconditional exec forwarder")
	}
	realBinary := strings.TrimSuffix(strings.TrimPrefix(lines[1], prefix), suffix)
	if !filepath.IsAbs(realBinary) || strings.ContainsAny(realBinary, " \t\r\n") {
		return "", errors.New("forwarded core path is unsafe")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(realBinary)); err != nil {
		return "", err
	}
	if err := validateExistingCoreExecutable(realBinary); err != nil {
		return "", err
	}
	return realBinary, nil
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func limitDiscoveryIssue(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "�"))
	if len(value) <= 512 {
		return value
	}
	return strings.ToValidUTF8(value[:512], "�")
}

func loadExistingCoreDiscoveryState(path string, managers ...*ServiceManager) (existingCoreDiscoveryState, error) {
	manager := selectedServiceManager(managers...)
	if err := validateStateDirectory(filepath.Dir(path)); err != nil {
		return existingCoreDiscoveryState{}, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return existingCoreDiscoveryState{}, err
	}
	defer root.Close()
	info, err := root.Lstat(filepath.Base(path))
	if err != nil {
		return existingCoreDiscoveryState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return existingCoreDiscoveryState{}, errors.New("existing-core discovery state must be a protected regular file")
	}
	if err := validateOwner(info, "existing-core discovery state"); err != nil {
		return existingCoreDiscoveryState{}, err
	}
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return existingCoreDiscoveryState{}, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, (32<<10)+1))
	if err != nil {
		return existingCoreDiscoveryState{}, err
	}
	if len(contents) > 32<<10 {
		return existingCoreDiscoveryState{}, errors.New("existing-core discovery state is too large")
	}
	var state existingCoreDiscoveryState
	if err := json.Unmarshal(contents, &state); err != nil {
		return existingCoreDiscoveryState{}, err
	}
	if state.Version != existingCoreDiscoveryStateVersion {
		return existingCoreDiscoveryState{}, errors.New("existing-core discovery state version is unsupported")
	}
	for engine, issue := range state.Issues {
		if (engine != core.EngineXray && engine != core.EngineSingBox) || issue == "" || len(issue) > 512 || !utf8.ValidString(issue) {
			return existingCoreDiscoveryState{}, errors.New("existing-core discovery issue is invalid")
		}
	}
	for engine, stored := range state.Specs {
		spec := stored.engineSpec()
		// A directory-authoritative mapping carries no main configuration file;
		// its confdir must then be present and absolute so the reader still has
		// exactly one protected source of truth.
		configPathMapped := filepath.IsAbs(spec.ConfigPath) || (spec.ConfigPath == "" && spec.ConfigDirectory != "")
		if !supportedExistingServiceForManager(manager, engine, spec.Service) || !filepath.IsAbs(spec.Binary) || !configPathMapped ||
			(spec.ConfigDirectory != "" && !filepath.IsAbs(spec.ConfigDirectory)) || !filepath.IsAbs(existingServiceBinary(spec)) {
			return existingCoreDiscoveryState{}, errors.New("existing-core discovery mapping is invalid")
		}
	}
	return state, nil
}

func saveExistingCoreDiscoveryState(path string, state existingCoreDiscoveryState) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := validateStateDirectory(directory); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if info, statErr := root.Lstat(filepath.Base(path)); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return errors.New("existing-core discovery state destination is unsafe")
		}
		if err := validateOwner(info, "existing-core discovery state destination"); err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	suffix, err := randomSuffix(10)
	if err != nil {
		return err
	}
	tempName := ".existing-cores-" + suffix + ".tmp"
	temp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(tempName)
	if err := json.NewEncoder(temp).Encode(state); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := root.Rename(tempName, filepath.Base(path)); err != nil {
		return err
	}
	return syncRootDirectory(root)
}
