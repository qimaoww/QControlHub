//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func discoverExistingCoreService(ctx context.Context, engine core.Engine, managed EngineSpec, validationDirectory string, managers ...*ServiceManager) (EngineSpec, bool, string) {
	manager := selectedServiceManager(managers...)
	if engine == core.EngineShadowsocksRust && manager.Kind() == ServiceManagerOpenRC {
		return EngineSpec{}, false, ""
	}
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
			return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务和标准 executable，但 executable 未通过 root 所有、非符号链接、不可组/其他写和原生二进制安全校验：%s", engine, core.DiagnosticText(err.Error()))
		}
		if err := validateExistingCoreExecutable(executable); err != nil {
			return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务和标准 executable，但 executable 未通过 root 所有、非符号链接、不可组/其他写和原生二进制安全校验：%s", engine, core.DiagnosticText(err.Error()))
		}
		realBinary = executable
	} else {
		var err error
		realBinary, err = resolveDiscoveredExistingBinary(executable)
		if err != nil {
			return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务和配置，但 executable wrapper 不在安全支持范围；请改为真实二进制、一跳二进制链接或固定 exec 转发器：%s", engine, core.DiagnosticText(err.Error()))
		}
	}
	spec := EngineSpec{
		Binary: realBinary, ConfigPath: configPath, ConfigDirectory: configDirectory,
		WorkingDirectory: workDirectory, ServiceBinary: executable, Service: service,
		ACLPath: existingSSRustACLArg(engine, strings.Fields(argv)),
	}
	return validateDiscoveredExistingSpec(ctx, engine, managed, spec, validationDirectory, manager)
}

func validateDiscoveredExistingSpec(ctx context.Context, engine core.Engine, managed, spec EngineSpec, validationDirectory string, manager *ServiceManager) (EngineSpec, bool, string) {
	managedStatus, err := validateManagedServiceTarget(ctx, engine, managed, manager)
	if err != nil {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但 QAgent 专用服务不是受支持的安全 unit：%s", engine, core.DiagnosticText(err.Error()))
	}
	if err := verifyExistingServiceMapping(ctx, engine, spec, manager); err != nil {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但映射在核验期间发生变化：%s", engine, core.DiagnosticText(err.Error()))
	}
	validationSpec := managed
	validationSpec.ConfigPath = filepath.Join(validationDirectory, "config.json")
	probe := &Executor{Specs: map[core.Engine]EngineSpec{engine: validationSpec}, ExistingSpecs: map[core.Engine]EngineSpec{engine: spec}, Services: manager}
	if _, err := probe.readExistingConfig(ctx, engine, validationSpec, spec); err != nil {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但配置源未通过受保护路径与真实内核校验：%s", engine, core.DiagnosticText(err.Error()))
	}
	if err := verifyExistingServiceMapping(ctx, engine, spec, manager); err != nil {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但映射在配置核验期间发生变化：%s", engine, core.DiagnosticText(err.Error()))
	}
	managedStatusAfter, err := validateManagedServiceTarget(ctx, engine, managed, manager)
	if err != nil {
		return EngineSpec{}, false, fmt.Sprintf("检测到活动的 %s 服务，但 QAgent 专用服务在配置核验期间读取失败：%s", engine, core.DiagnosticText(err.Error()))
	}
	if managedStatusAfter != managedStatus {
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
