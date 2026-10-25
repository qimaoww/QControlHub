//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type coreLogConfigurationHistory struct {
	Version    int    `json:"version"`
	ConfigPath string `json:"config_path"`
}

func coreLogConfigurationHistoryPath(executor *Executor) string {
	if executor == nil || executor.MigrationMarkerPrefix == "" {
		return ""
	}
	return executor.MigrationMarkerPrefix + ".log-source-sing-box.json"
}

func readCoreLogConfigurationHistory(path string) (coreLogConfigurationHistory, error) {
	var state coreLogConfigurationHistory
	content, err := readConfigurationFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal([]byte(content), &state); err != nil {
		return state, err
	}
	if state.Version != 1 || !filepath.IsAbs(state.ConfigPath) {
		return state, errors.New("invalid core log configuration history")
	}
	return state, nil
}

func (collector *CoreLogCollector) singBoxConfigurationKnown(executor *Executor, spec EngineSpec) (bool, error) {
	collector.mu.Lock()
	known := collector.configuredPaths[spec.ConfigPath]
	collector.mu.Unlock()
	if known {
		return true, nil
	}
	if path := coreLogConfigurationHistoryPath(executor); path != "" {
		state, err := readCoreLogConfigurationHistory(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		if err == nil && state.ConfigPath == spec.ConfigPath {
			return true, nil
		}
	}
	if executor != nil && executor.MigrationMarkerPrefix != "" {
		record, err := readCoreMigrationRecord(executor.MigrationMarkerPrefix, core.EngineSingBox)
		if err != nil {
			return false, err
		}
		if record.State == coreMigrationComplete {
			return true, nil
		}
	}
	// Backups also identify configurations deployed before history was added.
	backups, err := filepath.Glob(spec.ConfigPath + ".bak-*")
	if err != nil {
		return false, err
	}
	for _, backup := range backups {
		if err := validateProtectedRegularFile(backup); err == nil {
			return true, nil
		}
	}
	// An already running process proves prior configuration even when this is
	// the first Agent version with a history sidecar and no backup exists.
	if collector.serviceState != nil {
		status, err := collector.serviceState(spec.Service)
		return err == nil && status == "active", nil
	}
	if collector.manager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		status, err := serviceStatusWithManager(ctx, collector.manager, spec.Service)
		return err == nil && status == "active", nil
	}
	return false, nil
}

func (collector *CoreLogCollector) rememberSingBoxConfiguration(executor *Executor, spec EngineSpec) error {
	collector.mu.Lock()
	if collector.configuredPaths == nil {
		collector.configuredPaths = make(map[string]bool)
	}
	collector.configuredPaths[spec.ConfigPath] = true
	collector.missingSingBoxConfig = false
	collector.mu.Unlock()
	path := coreLogConfigurationHistoryPath(executor)
	if path == "" {
		return nil
	}
	if state, err := readCoreLogConfigurationHistory(path); err == nil && state.ConfigPath == spec.ConfigPath {
		return nil
	}
	content, err := json.Marshal(coreLogConfigurationHistory{Version: 1, ConfigPath: spec.ConfigPath})
	if err != nil {
		return err
	}
	backup, err := atomicDeploy(path, string(content)+"\n")
	if err == nil && backup != "" {
		_ = os.Remove(backup)
	}
	return err
}

func (collector *CoreLogCollector) clearMissingSingBoxInstallation() {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if !collector.missingSingBoxConfig {
		return
	}
	collector.missingSingBoxConfig = false
	delete(collector.status, core.EngineSingBox)
	delete(collector.statusKind, core.EngineSingBox)
	delete(collector.kindStatus[core.EngineSingBox], core.CoreLogSourceFile)
	collector.preferredKind[core.EngineSingBox] = collector.consoleKind[core.EngineSingBox]
}
