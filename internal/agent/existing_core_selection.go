package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func existingSourceID(spec EngineSpec) string { return coreMigrationSourceDigest(spec) }

// Managed base and shared instances already have an ownership and lifecycle
// mapping. An alias or a renamed service must not turn them into external input.
func isManagedCoreSource(spec EngineSpec) bool {
	if strings.HasPrefix(spec.Service, "qagent-") {
		return true
	}
	for _, path := range []string{spec.ConfigPath, spec.ConfigDirectory} {
		if path == "" {
			continue
		}
		if filepath.Clean(path) == managedCoreConfigurationRoot || pathWithin(path, managedCoreConfigurationRoot) {
			return true
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil && (resolved == managedCoreConfigurationRoot || pathWithin(resolved, managedCoreConfigurationRoot)) {
			return true
		}
	}
	return false
}

func (e *Executor) existingSources(engine core.Engine) []EngineSpec {
	e.specsMu.RLock()
	values := append([]EngineSpec(nil), e.ExistingSources[engine]...)
	if len(values) == 0 {
		if value, ok := e.ExistingSpecs[engine]; ok {
			values = append(values, value)
		}
	}
	e.specsMu.RUnlock()
	var sources []EngineSpec
	for _, value := range values {
		if !isManagedCoreSource(value) {
			sources = append(sources, value)
		}
	}
	return sources
}

func (e *Executor) LoadExistingCoreSources() error {
	if e.ExistingDiscoveryStatePath == "" {
		return nil
	}
	state, err := loadExistingCoreDiscoveryState(e.ExistingDiscoveryStatePath, e.serviceManager())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	e.specsMu.Lock()
	defer e.specsMu.Unlock()
	e.ExistingSources = make(map[core.Engine][]EngineSpec)
	for engine, values := range state.Sources {
		// A manual mapping takes precedence over every automatic candidate.
		if value, ok := e.ExistingSpecs[engine]; ok && value.SourceContext == "" {
			e.ExistingSources[engine] = []EngineSpec{value}
			continue
		}
		for _, value := range values {
			spec := value.engineSpec()
			if err := validateExistingSpecPaths(engine, spec); err != nil {
				return err
			}
			e.ExistingSources[engine] = append(e.ExistingSources[engine], spec)
		}
	}
	return nil
}

func (e *Executor) RefreshDiscoveredSources(ctx context.Context, managed, manual map[core.Engine]EngineSpec) error {
	e.migrationMu.Lock()
	defer e.migrationMu.Unlock()
	specs, issues, err := RefreshExistingCoreDiscovery(ctx, e.ExistingDiscoveryStatePath, e.MigrationMarkerPrefix, managed, manual, e.serviceManager())
	if err != nil {
		return err
	}
	e.specsMu.Lock()
	e.ExistingSpecs = specs
	e.ExistingDiscoveryIssues = issues
	for engine := range e.completedMigrations {
		delete(e.ExistingSpecs, engine)
	}
	e.specsMu.Unlock()
	if err := e.LoadExistingCoreSources(); err != nil {
		return err
	}
	e.specsMu.Lock()
	for engine := range e.completedMigrations {
		delete(e.ExistingSources, engine)
	}
	e.specsMu.Unlock()
	return nil
}
