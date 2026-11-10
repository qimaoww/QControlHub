package agent

import (
	"context"
	"errors"
	"os"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func existingSourceID(spec EngineSpec) string { return coreMigrationSourceDigest(spec) }

func (e *Executor) existingSources(engine core.Engine) []EngineSpec {
	e.specsMu.RLock()
	defer e.specsMu.RUnlock()
	if values := e.ExistingSources[engine]; len(values) > 0 {
		return append([]EngineSpec(nil), values...)
	}
	if value, ok := e.ExistingSpecs[engine]; ok {
		return []EngineSpec{value}
	}
	return nil
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
