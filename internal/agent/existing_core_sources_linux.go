//go:build linux

package agent

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type existingServiceContext struct {
	Argv             []string `json:"argv"`
	Directory        string   `json:"directory"`
	User             string   `json:"user,omitempty"`
	Group            string   `json:"group,omitempty"`
	Environment      string   `json:"environment,omitempty"`
	EnvironmentFiles string   `json:"environment_files,omitempty"`
	UnitDigest       string   `json:"unit_digest"`
}

func installedServiceNames(ctx context.Context, manager *ServiceManager) ([]string, error) {
	var names []string
	if manager.Kind() == ServiceManagerOpenRC {
		entries, err := os.ReadDir(openRCInitRoot)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if safeServiceName(entry.Name()) {
				names = append(names, entry.Name())
			}
		}
	} else {
		output, err := run(ctx, systemctlPath, "list-unit-files", "--type=service", "--no-legend", "--no-pager")
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 0 && strings.HasSuffix(fields[0], ".service") && safeServiceName(fields[0]) {
				names = append(names, fields[0])
			}
		}
	}
	if len(names) > 4096 {
		return nil, errors.New("service discovery exceeds 4096 units")
	}
	sort.Strings(names)
	return names, nil
}

func existingBinaryEngine(ctx context.Context, path string) core.Engine {
	if info, err := buildinfo.ReadFile(path); err == nil {
		module := strings.ToLower(info.Main.Path)
		switch {
		case strings.Contains(module, "metacubex/mihomo"):
			return core.EngineMihomo
		case strings.Contains(module, "xtls/xray-core"):
			return core.EngineXray
		case strings.Contains(module, "sagernet/sing-box"):
			return core.EngineSingBox
		}
	}
	// Rust does not carry Go build information. Only interrogate a protected
	// candidate executable with the documented, non-running version switch.
	data, _, readErr := readImportSource(path, maxReleaseAssetSize)
	if readErr == nil && bytes.HasPrefix(data, []byte("\x7fELF")) && bytes.Contains(data, []byte("shadowsocks-rust")) && bytes.Contains(data, []byte("ssserver ")) {
		version := binaryVersion(ctx, core.EngineShadowsocksRust, path)
		if strings.Contains(strings.ToLower(version), "shadowsocks") {
			return core.EngineShadowsocksRust
		}
	}
	return ""
}

// systemd's human-readable ExecStart property does not preserve argv boundaries
// on all supported releases. Parse its effective unit fragments as data; never
// execute a shell or an EnvironmentFile to recover the command.
func effectiveUnitCommand(content string) ([]string, error) {
	section := ""
	command := ""
	count := 0
	content = strings.ReplaceAll(content, "\\\n", " ")
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[Service]" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "ExecStart" {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			command = ""
			count = 0
			continue
		}
		command = value
		count++
	}
	if count != 1 {
		return nil, errors.New("effective unit must contain one ExecStart")
	}

	words, err := splitServiceWords(command)
	if err != nil || len(words) == 0 {
		return nil, errors.Join(err, errors.New("effective ExecStart is empty or invalid"))
	}
	if !filepath.IsAbs(words[0]) {
		return nil, errors.New("effective ExecStart executable is not absolute")
	}
	return words, nil
}

func inspectSystemdCoreSource(ctx context.Context, service string) (core.Engine, EngineSpec, error) {
	property := func(name string) (string, error) {
		v, e := run(ctx, systemctlPath, "show", service, "--property="+name, "--value")
		return strings.TrimSpace(v), e
	}
	// The unit ID coalesces aliases before a source is counted.
	id, err := property("Id")
	if err != nil {
		return "", EngineSpec{}, err
	}
	if id != "" && safeServiceName(id) {
		service = id
	}
	if isManagedCoreSource(EngineSpec{Service: service}) {
		return "", EngineSpec{}, nil
	}
	unit, err := run(ctx, systemctlPath, "cat", service, "--no-pager")
	if err != nil {
		return "", EngineSpec{}, err
	}
	argv, err := effectiveUnitCommand(unit)
	if err != nil {
		return "", EngineSpec{}, err
	}
	real, err := resolveDiscoveredExistingBinary(argv[0])
	if err != nil {
		return "", EngineSpec{}, err
	}
	engine := existingBinaryEngine(ctx, real)
	if !engine.Valid() {
		return "", EngineSpec{}, nil
	}
	context := existingServiceContext{Argv: argv, UnitDigest: coreMigrationConfigDigest(unit)}
	context.Directory, err = property("WorkingDirectory")
	if err != nil {
		return engine, EngineSpec{}, err
	}
	if context.Directory == "" {
		context.Directory = "/"
	}
	context.User, err = property("User")
	if err != nil {
		return engine, EngineSpec{}, err
	}
	context.Group, err = property("Group")
	if err != nil {
		return engine, EngineSpec{}, err
	}
	context.Environment, err = property("Environment")
	if err != nil {
		return engine, EngineSpec{}, err
	}
	context.EnvironmentFiles, err = property("EnvironmentFiles")
	if err != nil {
		return engine, EngineSpec{}, err
	}
	environment := map[string]string{}
	assignments, err := splitServiceWords(context.Environment)
	if err != nil {
		return engine, EngineSpec{}, err
	}
	for _, assignment := range assignments {
		key, value, ok := strings.Cut(assignment, "=")
		if !ok || !serviceVariableName.MatchString(key) {
			return engine, EngineSpec{}, errors.New("malformed service environment")
		}
		environment[key] = value
	}
	files, err := splitServiceWords(context.EnvironmentFiles)
	if err != nil {
		return engine, EngineSpec{}, err
	}
	for index := 0; index < len(files); index += 2 {
		if index+1 >= len(files) || !strings.HasPrefix(files[index+1], "(ignore_errors=") {
			return engine, EngineSpec{}, errors.New("ambiguous effective EnvironmentFiles")
		}
		path := files[index]
		optional := files[index+1] == "(ignore_errors=yes)"
		data, resolved, err := readExternalImportSource(path, 256<<10)
		if errors.Is(err, os.ErrNotExist) && optional {
			continue
		}
		if err != nil {
			return engine, EngineSpec{}, fmt.Errorf("read environment file %s: %w", path, err)
		}
		values, err := parseSystemdEnvironmentFile(string(data))
		if err != nil {
			return engine, EngineSpec{}, err
		}
		for key, value := range values {
			environment[key] = value
		}
		context.UnitDigest = coreMigrationConfigDigest(context.UnitDigest + "\x00" + resolved + "\x00" + string(data))
	}
	for key, value := range environment {
		if value != "" && (strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "XRAY_") || strings.HasPrefix(key, "V2RAY_") || strings.HasPrefix(key, "SING_") || strings.HasPrefix(key, "ENABLE_") || strings.HasPrefix(key, "MIHOMO_") || key == "SAFE_PATHS") {
			return engine, EngineSpec{}, fmt.Errorf("environment variable %s requires a verified managed runtime mapping", key)
		}
	}
	for index, arg := range argv {
		// systemd splits a standalone $VAR after expansion, whereas ${VAR}
		// always forms one argument. Do not confuse those two forms.
		if strings.HasPrefix(arg, "$") && !strings.HasPrefix(arg, "${") && serviceVariableName.MatchString(arg[1:]) {
			return engine, EngineSpec{}, errors.New("standalone environment argv expansion requires an ordered argument mapping")
		}
		argv[index], err = expandServiceArgument(arg, environment)
		if err != nil {
			return engine, EngineSpec{}, err
		}
	}
	context.Argv = argv

	for _, name := range []string{"RootDirectory", "RootImage", "BindPaths", "BindReadOnlyPaths", "ExecCondition", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost"} {
		value, err := property(name)
		if err != nil {
			return engine, EngineSpec{}, err
		}
		if value != "" {
			return engine, EngineSpec{}, fmt.Errorf("service %s requires a managed mapping", name)
		}
	}
	spec, err := parseCoreServiceCommand(engine, argv[0], argv, context.Directory)
	if err != nil {
		return engine, EngineSpec{}, err
	}
	spec.Binary = real
	spec.Service = service
	if isManagedCoreSource(spec) {
		return "", EngineSpec{}, nil
	}
	pidText, err := property("MainPID")
	if err != nil {
		return engine, EngineSpec{}, err
	}
	pid, _ := strconv.Atoi(pidText)
	if pid > 0 {
		directory := context.Directory
		if engine == core.EngineSingBox {
			directory = spec.WorkingDirectory
		}
		if err := verifyExistingProcessContext(pid, context, directory); err != nil {
			return engine, EngineSpec{}, err
		}
		proc := filepath.Join(openRCProcRoot, strconv.Itoa(pid))
		executable, err := os.Readlink(filepath.Join(proc, "exe"))
		if err != nil || executable != real {
			return engine, EngineSpec{}, errors.New("service MainPID executable does not match its configured core")
		}
		data, err := os.ReadFile(filepath.Join(proc, "cmdline"))
		if err != nil {
			return engine, EngineSpec{}, err
		}
		actual := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
		if len(actual) != len(argv) {
			return engine, EngineSpec{}, errors.New("service process arguments differ from its unit")
		}
		for i := range argv {
			if i == 0 && (actual[i] == real || actual[i] == argv[i]) {
				continue
			}
			if actual[i] != argv[i] {
				return engine, EngineSpec{}, errors.New("service process arguments differ from its unit")
			}
		}
	}
	if _, err := existingServiceIdentity(context); err != nil {
		return engine, EngineSpec{}, err
	}
	encoded, _ := json.Marshal(context)
	spec.SourceContext = string(encoded)
	return engine, spec, nil
}

func discoverInstalledCoreSources(ctx context.Context, engine core.Engine, manager *ServiceManager) ([]EngineSpec, []string, error) {
	specs, issues, err := discoverAllInstalledCoreSources(ctx, manager)
	return specs[engine], issues[engine], err
}

func discoverAllInstalledCoreSources(ctx context.Context, manager *ServiceManager) (map[core.Engine][]EngineSpec, map[core.Engine][]string, error) {
	names, err := installedServiceNames(ctx, manager)
	if err != nil {
		return nil, nil, err
	}
	specs := map[core.Engine][]EngineSpec{}
	issues := map[core.Engine][]string{}
	seen := map[string]bool{}
	for _, name := range names {
		if strings.HasPrefix(name, "qagent-") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		var found core.Engine
		var spec EngineSpec
		var err error
		if manager.Kind() == ServiceManagerOpenRC {
			found, spec, err = inspectOpenRCCoreSource(ctx, name, manager)
		} else {
			found, spec, err = inspectSystemdCoreSource(ctx, name)
		}
		if !found.Valid() {
			continue
		}
		if err != nil {
			issues[found] = append(issues[found], fmt.Sprintf("%s: %s", name, core.DiagnosticText(err.Error())))
			continue
		}
		if !seen[spec.Service] {
			seen[spec.Service] = true
			if len(specs[found]) >= 64 {
				return nil, nil, errors.New("discovery exceeds 64 sources for one core")
			}
			specs[found] = append(specs[found], spec)
		}
	}
	return specs, issues, nil
}
