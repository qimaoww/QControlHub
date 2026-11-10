//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func inspectOpenRCCoreSource(ctx context.Context, service string, manager *ServiceManager) (core.Engine, EngineSpec, error) {
	path := filepath.Join(openRCInitRoot, service)
	data, _, err := readImportSource(path, 256<<10)
	if err != nil {
		return "", EngineSpec{}, err
	}
	values := map[string]string{"RC_SVCNAME": service, "SVCNAME": service}
	assignments := []string{}
	hasHooks := false
	inFunction := false
	dependencyFunction := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "()") || strings.HasSuffix(line, "{") {
			inFunction = true
			dependencyFunction = strings.HasPrefix(line, "depend()")
			if !dependencyFunction {
				hasHooks = true
			}
			continue
		}
		if line == "}" {
			inFunction = false
			continue
		}
		if inFunction {
			if dependencyFunction && line != "" && line != "need net" && line != "after net" && !strings.HasPrefix(line, "#") {
				hasHooks = true
			}
			continue
		}
		key, _, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if ok && serviceVariableName.MatchString(key) {
			assignments = append(assignments, line)
		}
	}
	values, err = parseServiceAssignments(strings.Join(assignments, "\n"), values)
	if err != nil {
		return "", EngineSpec{}, err
	}
	// The OpenRC service configuration is data; sourcing it would execute
	// arbitrary host code during discovery.
	conf := filepath.Join(filepath.Dir(openRCInitRoot), "conf.d", service)
	var confData []byte
	if _, err := os.Lstat(conf); err == nil {
		confData, _, err = readImportSource(conf, 256<<10)
		if err != nil {
			return "", EngineSpec{}, err
		}
		values, err = parseServiceAssignments(string(confData), values)
		if err != nil {
			return "", EngineSpec{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", EngineSpec{}, err
	}
	command, err := expandServiceArgument(values["command"], values)
	if err != nil || !filepath.IsAbs(command) {
		return "", EngineSpec{}, err
	}
	real, err := resolveDiscoveredExistingBinary(command)
	if err != nil {
		return "", EngineSpec{}, err
	}
	engine := existingBinaryEngine(ctx, real)
	if !engine.Valid() {
		return "", EngineSpec{}, nil
	}
	if hasHooks {
		return engine, EngineSpec{}, errors.New("OpenRC service functions require a verified auxiliary startup mapping")
	}
	argv, err := splitServiceWords(values["command_args"])
	if err != nil {
		return engine, EngineSpec{}, err
	}
	for i := range argv {
		argv[i], err = expandServiceArgument(argv[i], values)
		if err != nil {
			return engine, EngineSpec{}, err
		}
	}
	argv = append([]string{command}, argv...)
	cwd := values["directory"]
	if cwd == "" {
		cwd = "/"
	}
	cwd, err = expandServiceArgument(cwd, values)
	if err != nil {
		return engine, EngineSpec{}, err
	}
	spec, err := parseCoreServiceCommand(engine, command, argv, cwd)
	if err != nil {
		return engine, EngineSpec{}, err
	}
	spec.Binary = real
	spec.Service = service
	user, group, _ := strings.Cut(values["command_user"], ":")
	context := existingServiceContext{Argv: argv, Directory: cwd, User: user, Group: group, UnitDigest: coreMigrationConfigDigest(string(data) + "\x00" + string(confData))}
	encoded, _ := json.Marshal(context)
	spec.SourceContext = string(encoded)
	status, err := serviceStatusWithManager(ctx, manager, service)
	if err != nil {
		return engine, EngineSpec{}, err
	}
	if status == "active" {
		identity, err := verifyOpenRCExistingServiceProcess(ctx, engine, spec)
		if err != nil {
			return engine, EngineSpec{}, fmt.Errorf("service-bound process: %w", err)
		}
		if identity.Child.Executable != real {
			return engine, EngineSpec{}, errors.New("OpenRC child executable changed")
		}
		directory := cwd
		if engine == core.EngineSingBox {
			directory = spec.WorkingDirectory
		}
		if err := verifyExistingProcessContext(identity.Child.PID, context, directory); err != nil {
			return engine, EngineSpec{}, err
		}
	} else if status != "inactive" {
		return engine, EngineSpec{}, fmt.Errorf("OpenRC service status is %s", status)
	}
	if _, err := existingServiceIdentity(context); err != nil {
		return engine, EngineSpec{}, err
	}
	return engine, spec, nil
}
