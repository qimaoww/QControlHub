package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type importResourceReplacement struct {
	Path     string `json:"path"`
	Before   string `json:"before"`
	After    string `json:"after"`
	Previous []byte `json:"previous"`
}
type importResourceRefresh struct {
	Active bool                        `json:"active"`
	Files  []importResourceReplacement `json:"files"`
}

func readImportResourceState(prefix string, engine core.Engine, managed EngineSpec) (durableImportResources, error) {
	data, _, err := readImportSource(importResourceStatePath(prefix, engine), 16<<20)
	if err != nil {
		return durableImportResources{}, err
	}
	var state durableImportResources
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	allowed := pathWithin(state.Root, filepath.Join(filepath.Dir(managed.ConfigPath), "imports")) || engine == core.EngineXray && pathWithin(state.Root, filepath.Join(filepath.Dir(managed.Binary), "imports")) || engine == core.EngineShadowsocksRust && pathWithin(state.Root, filepath.Join(filepath.Dir(managed.ConfigPath), "install-ss-rust"))
	if state.Version != 1 || !allowed || len(state.Resources) > 256 || len(state.Dependencies) > 256 {
		return state, errors.New("invalid instance resource manifest")
	}
	for _, resource := range state.Resources {
		if filepath.Dir(resource.Path) != state.Root || resource.Limit <= 0 || resource.Limit > maxReleaseAssetSize {
			return state, errors.New("resource refresh escapes managed instance")
		}
	}
	if state.Pending != nil {
		if len(state.Pending.Files) > 256 {
			return state, errors.New("resource recovery limit exceeded")
		}
		for _, file := range state.Pending.Files {
			if filepath.Dir(file.Path) != state.Root || len(file.Previous) > 4<<20 || coreMigrationConfigDigest(string(file.Previous)) != file.Before {
				return state, errors.New("invalid resource refresh recovery")
			}
		}
	}
	return state, nil
}

func replaceImportedResource(path string, content []byte, identity commandIdentity) error {
	if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	name, err := randomCoreTempName(root)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	metadataErr := applyFileMetadata(file, fileMetadata{mode: 0o640, uid: 0, gid: int(identity.gid), ownershipKnown: true})
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, metadataErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := root.Rename(name, filepath.Base(path)); err != nil {
		return err
	}
	return syncRootDirectory(root)
}

func (e *Executor) restoreImportResourceRefresh(ctx context.Context, engine core.Engine, spec EngineSpec, state *durableImportResources, identity commandIdentity) error {
	pending := state.Pending
	if pending == nil {
		return nil
	}
	if _, err := serviceCommandAndVerifyWithManager(ctx, e.serviceManager(), spec.Service, core.ActionStop); err != nil {
		return err
	}
	for _, file := range pending.Files {
		data, _, err := readImportSource(file.Path, 4<<20)
		if err != nil {
			return err
		}
		digest := coreMigrationConfigDigest(string(data))
		if digest != file.Before && digest != file.After {
			return errors.New("resource changed outside refresh transaction; recovery retained it")
		}
		if digest != file.Before {
			if err := replaceImportedResource(file.Path, file.Previous, identity); err != nil {
				return err
			}
		}
		for i := range state.Resources {
			if state.Resources[i].Path == file.Path {
				state.Resources[i].Digest = file.Before
			}
		}
	}
	if pending.Active {
		if _, err := serviceCommandAndVerifyWithManager(ctx, e.serviceManager(), spec.Service, core.ActionStart); err != nil {
			return err
		}
	}
	state.Pending = nil
	return writeImportResourceState(e.MigrationMarkerPrefix, engine, *state)
}

// Re-read the authorized lexical source, including certificate live/archive
// links, and validate the entire replacement before changing a running core.
// The durable before-images let a restart restore one consistent generation.
func (e *Executor) refreshImportedResources(ctx context.Context, engine core.Engine, spec EngineSpec) error {
	state, err := readImportResourceState(e.MigrationMarkerPrefix, engine, spec)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return err
	}
	if state.Pending != nil {
		return e.restoreImportResourceRefresh(ctx, engine, spec, &state, identity)
	}
	if len(state.Dependencies) == 0 {
		return nil
	}
	content, err := readConfigurationFile(spec.ConfigPath)
	if err != nil {
		return err
	}
	next := make(map[string][]byte)
	refs := []importFileReference{}
	material := map[string][]byte{}
	limits := map[string]int64{}
	for _, resource := range state.Resources {
		limits[resource.Path] = resource.Limit
	}
	total := 0
	for _, dependency := range state.Dependencies {
		// Configuration edits can retire an imported resource. Never rewrite it
		// after the instance no longer references it.
		if !strings.Contains(content, dependency.Destination) {
			continue
		}
		limit, ok := limits[dependency.Destination]
		if !ok {
			return fmt.Errorf("%s: resource ownership mapping is missing", dependency.Field)
		}
		data, _, err := readExternalImportSource(dependency.Source, limit)
		if err != nil {
			return fmt.Errorf("refresh %s: %w", dependency.Field, err)
		}
		total += len(data)
		if total > 256<<20 {
			return errors.New("import resource refresh exceeds the combined 256 MiB limit")
		}
		material[dependency.Source] = data
		refs = append(refs, importFileReference{path: dependency.Source, field: dependency.Field})
		next[dependency.Destination] = data
	}
	if err := validateImportCertificateMaterials(engine, refs, material); err != nil {
		return err
	}
	replacements := []importResourceReplacement{}
	for _, resource := range state.Resources {
		data, ok := next[resource.Path]
		if !ok {
			continue
		}
		previous, _, err := readImportSource(resource.Path, resource.Limit)
		if err != nil {
			return err
		}
		if coreMigrationConfigDigest(string(previous)) != resource.Digest {
			return errors.New("managed resource was changed outside import update mapping")
		}
		digest := coreMigrationConfigDigest(string(data))
		if digest == resource.Digest {
			continue
		}
		if len(previous) > 4<<20 || len(data) > 4<<20 {
			return fmt.Errorf("%s: automatic refresh exceeds 4 MiB transaction limit", resource.Field)
		}
		replacements = append(replacements, importResourceReplacement{resource.Path, resource.Digest, digest, previous})
	}
	if len(replacements) == 0 {
		return nil
	}
	// Validate against disposable copies before stopping the managed service.
	preview := coreImportPlan{managedContent: content, stateRoot: filepath.Dir(spec.ConfigPath), resourceRoot: state.Root}
	for _, resource := range state.Resources {
		if _, referenced := next[resource.Path]; !referenced {
			continue
		}
		source := resource.Path
		for _, dependency := range state.Dependencies {
			if dependency.Destination == resource.Path {
				source = dependency.Source
				break
			}
		}
		data, resolved, err := readExternalImportSource(source, resource.Limit)
		if err != nil {
			return err
		}
		preview.resources = append(preview.resources, coreImportResource{source: resolved, destination: resource.Path, digest: coreMigrationConfigDigest(string(data)), limit: resource.Limit})
	}
	if err := e.validateImportPlanSnapshot(ctx, engine, spec, preview); err != nil {
		return err
	}
	for _, dependency := range state.Dependencies {
		expected, ok := next[dependency.Destination]
		if !ok {
			continue
		}
		actual, _, err := readExternalImportSource(dependency.Source, limits[dependency.Destination])
		if err != nil {
			return err
		}
		if coreMigrationConfigDigest(string(actual)) != coreMigrationConfigDigest(string(expected)) {
			return errors.New("import resource changed during update preparation")
		}
	}
	status, err := serviceStatusWithManager(ctx, e.serviceManager(), spec.Service)
	if err != nil {
		return err
	}
	if status != "active" && status != "inactive" {
		return fmt.Errorf("resource refresh waits for a stable service state (%s)", status)
	}
	state.Pending = &importResourceRefresh{Active: status == "active", Files: replacements}
	recoveryBytes := 0
	for _, file := range replacements {
		recoveryBytes += len(file.Previous)
	}
	if recoveryBytes > 8<<20 {
		return errors.New("resource update recovery exceeds combined 8 MiB limit")
	}
	if err := writeImportResourceState(e.MigrationMarkerPrefix, engine, state); err != nil {
		return err
	}
	rollback := func(cause error) error {
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return errors.Join(cause, e.restoreImportResourceRefresh(recovery, engine, spec, &state, identity))
	}
	if status == "active" {
		if _, err := serviceCommandAndVerifyWithManager(ctx, e.serviceManager(), spec.Service, core.ActionStop); err != nil {
			return rollback(err)
		}
	}
	for _, file := range replacements {
		if err := replaceImportedResource(file.Path, next[file.Path], identity); err != nil {
			return rollback(err)
		}
	}
	if _, err := e.validateImportedSnapshot(ctx, engine, spec, content); err != nil {
		return rollback(err)
	}
	if status == "active" {
		if _, err := serviceCommandAndVerifyWithManager(ctx, e.serviceManager(), spec.Service, core.ActionStart); err != nil {
			return rollback(err)
		}
	}
	for i := range state.Resources {
		for _, file := range replacements {
			if state.Resources[i].Path == file.Path {
				state.Resources[i].Digest = file.After
			}
		}
	}
	completed := state.Pending
	state.Pending = nil
	if err := writeImportResourceState(e.MigrationMarkerPrefix, engine, state); err != nil {
		state.Pending = completed
		return rollback(err)
	}
	return nil
}

func (e *Executor) RefreshImportedResources(ctx context.Context) error {
	if !e.mainOperationMu.TryLock() {
		return nil
	}
	defer e.mainOperationMu.Unlock()
	e.migrationMu.Lock()
	defer e.migrationMu.Unlock()
	var failures []error
	for engine, spec := range e.Specs {
		record, err := readCoreMigrationRecord(e.MigrationMarkerPrefix, engine)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if record.State != coreMigrationComplete {
			continue
		}

		err = e.refreshImportedResources(ctx, engine, spec)
		// An interrupted replacement must be recovered before any ordinary
		// operation can start the core with mixed certificate generations.
		state, stateErr := readImportResourceState(e.MigrationMarkerPrefix, engine, spec)
		e.specsMu.Lock()
		if e.importedResourceIssues == nil {
			e.importedResourceIssues = make(map[core.Engine]string)
		}
		if err != nil {
			e.importedResourceIssues[engine] = "导入资源更新失败：" + core.DiagnosticText(err.Error())
		} else {
			delete(e.importedResourceIssues, engine)
		}
		const recoveryIssue = "导入资源更新尚未恢复："
		if stateErr == nil && state.Pending != nil {
			if e.ManagedSafetyIssues == nil {
				e.ManagedSafetyIssues = make(map[core.Engine]string)
			}
			e.ManagedSafetyIssues[engine] = recoveryIssue + core.DiagnosticText(fmt.Sprint(err))
		} else if strings.HasPrefix(e.ManagedSafetyIssues[engine], recoveryIssue) {
			delete(e.ManagedSafetyIssues, engine)
		}
		e.specsMu.Unlock()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s import resource update: %w", engine, err))
		}
	}
	return errors.Join(failures...)
}
