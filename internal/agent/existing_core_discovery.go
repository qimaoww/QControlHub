//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	core.EngineShadowsocksRust: {
		services:    []string{"shadowsocks-rust.service"},
		executables: []string{"/usr/local/bin/ssserver"},
		configs:     []string{"/etc/shadowsocks-rust/config.json"},
	},
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
	ACLPath          string `json:"acl_path,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
	ServiceBinary    string `json:"service_binary,omitempty"`
	Service          string `json:"service"`
}

func discoverySpecFromEngineSpec(spec EngineSpec) existingDiscoverySpec {
	return existingDiscoverySpec{
		Binary: spec.Binary, ConfigPath: spec.ConfigPath, ConfigDirectory: spec.ConfigDirectory,
		ACLPath:          spec.ACLPath,
		WorkingDirectory: spec.WorkingDirectory, ServiceBinary: spec.ServiceBinary, Service: spec.Service,
	}
}

func (spec existingDiscoverySpec) engineSpec() EngineSpec {
	return EngineSpec{
		Binary: spec.Binary, ConfigPath: spec.ConfigPath, ConfigDirectory: spec.ConfigDirectory,
		ACLPath:          spec.ACLPath,
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
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
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
