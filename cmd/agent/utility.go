//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/agent"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func existingSpec(prefix string) (agent.EngineSpec, bool) {
	binary := strings.TrimSpace(os.Getenv("QCH_EXISTING_" + prefix + "_BINARY"))
	configPath := strings.TrimSpace(os.Getenv("QCH_EXISTING_" + prefix + "_CONFIG"))
	configDirectory := strings.TrimSpace(os.Getenv("QCH_EXISTING_" + prefix + "_CONFIG_DIRECTORY"))
	workingDirectory := strings.TrimSpace(os.Getenv("QCH_EXISTING_" + prefix + "_WORK_DIRECTORY"))
	serviceBinary := strings.TrimSpace(os.Getenv("QCH_EXISTING_" + prefix + "_SERVICE_BINARY"))
	service := strings.TrimSpace(os.Getenv("QCH_EXISTING_" + prefix + "_SERVICE"))
	aclPath := strings.TrimSpace(os.Getenv("QCH_EXISTING_" + prefix + "_ACL"))
	configured := binary != "" || configPath != "" || configDirectory != "" || workingDirectory != "" || serviceBinary != "" || service != "" || aclPath != ""
	if !configured {
		return agent.EngineSpec{}, false
	}
	// A directory-authoritative mapping (installers that run the core with only
	// a confdir) has no main configuration file, so either source satisfies it.
	if binary == "" || service == "" || (configPath == "" && configDirectory == "") {
		slog.Error("existing core mapping requires binary, service, and a config file or config directory", "engine", prefix)
		os.Exit(1)
	}
	return agent.EngineSpec{
		Binary: binary, ConfigPath: configPath, ConfigDirectory: configDirectory,
		WorkingDirectory: workingDirectory, ServiceBinary: serviceBinary, Service: service,
		ACLPath: aclPath,
	}, true
}

func runUtilityCommand(specs map[core.Engine]agent.EngineSpec, arguments []string, serviceManagers ...*agent.ServiceManager) error {
	serviceManager, err := agent.NewServiceManager(agent.ServiceManagerSystemd)
	if err != nil {
		return err
	}
	if len(serviceManagers) > 0 && serviceManagers[0] != nil {
		serviceManager = serviceManagers[0]
	}
	if len(arguments) != 2 || arguments[0] != "inspect-existing" {
		return errors.New("usage: qagent inspect-existing <xray|sing-box|ss-rust>")
	}
	engine, err := core.ParseEngine(arguments[1])
	if err != nil || (engine != core.EngineXray && engine != core.EngineSingBox && engine != core.EngineShadowsocksRust) {
		return errors.New("inspect-existing supports only xray, sing-box and ss-rust")
	}
	existing := specs[engine]
	temporaryDirectory, err := os.MkdirTemp("", "qagent-inspect-existing-")
	if err != nil {
		return fmt.Errorf("create protected validation directory: %w", err)
	}
	defer os.RemoveAll(temporaryDirectory)
	managed := existing
	managed.ConfigPath = temporaryDirectory + "/config.json"
	executor := &agent.Executor{
		Specs:         map[core.Engine]agent.EngineSpec{engine: managed},
		ExistingSpecs: map[core.Engine]agent.EngineSpec{engine: existing},
		Services:      serviceManager,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := executor.Execute(ctx, core.Task{Action: core.ActionReadConfig, Engine: engine}); err != nil {
		return fmt.Errorf("existing %s configuration cannot be inspected safely: %w", engine, err)
	}
	return nil
}

func overrideSpec(spec agent.EngineSpec, prefix string) agent.EngineSpec {
	spec.Binary = env("QCH_"+prefix+"_BINARY", spec.Binary)
	spec.ConfigDirectory = strings.TrimSpace(os.Getenv("QCH_" + prefix + "_CONFIG_DIRECTORY"))
	// A directory-authoritative mapping is expressed as an explicitly empty
	// configuration path alongside a configuration directory. env() treats an
	// empty value as unset, so falling through to it here would silently
	// inspect the managed default file instead of the mapped confdir.
	rawConfig, configPresent := os.LookupEnv("QCH_" + prefix + "_CONFIG")
	if configPresent && strings.TrimSpace(rawConfig) == "" && spec.ConfigDirectory != "" {
		spec.ConfigPath = ""
	} else {
		spec.ConfigPath = env("QCH_"+prefix+"_CONFIG", spec.ConfigPath)
	}
	spec.WorkingDirectory = strings.TrimSpace(os.Getenv("QCH_" + prefix + "_WORK_DIRECTORY"))
	spec.ServiceBinary = strings.TrimSpace(os.Getenv("QCH_" + prefix + "_SERVICE_BINARY"))
	spec.Service = env("QCH_"+prefix+"_SERVICE", spec.Service)
	return spec
}
