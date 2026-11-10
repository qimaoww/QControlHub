package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type durableImportResource struct {
	Source  string `json:"source,omitempty"`
	Field   string `json:"field,omitempty"`
	Path    string `json:"path"`
	Digest  string `json:"digest"`
	Created bool   `json:"created"`
	Limit   int64  `json:"limit"`
}
type durableImportResources struct {
	Version      int                     `json:"version"`
	Root         string                  `json:"root"`
	Resources    []durableImportResource `json:"resources"`
	Dependencies []core.ImportDependency `json:"dependencies,omitempty"`
	Pending      *importResourceRefresh  `json:"pending,omitempty"`
}

func importResourceStatePath(prefix string, engine core.Engine) string {
	return prefix + "-" + string(engine) + ".resources"
}

// Record ownership before the first copy, so a crash can remove only files
// created by this import after the old configuration has been restored.
func persistImportResourcePlan(prefix string, engine core.Engine, plan coreImportPlan) error {
	if !plan.active() {
		return nil
	}
	state := durableImportResources{Version: 1, Root: plan.resourceRoot, Dependencies: plan.dependencies}
	for _, resource := range plan.resources {
		_, err := os.Lstat(resource.destination)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		state.Resources = append(state.Resources, durableImportResource{Source: resource.source, Field: resource.field, Path: resource.destination, Digest: resource.digest, Created: errors.Is(err, os.ErrNotExist), Limit: importResourceLimit(resource)})
	}
	return writeImportResourceState(prefix, engine, state)
}

func writeImportResourceState(prefix string, engine core.Engine, state durableImportResources) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return errors.New("import resource manifest exceeds 16 MiB; transaction was not persisted")
	}
	path := importResourceStatePath(prefix, engine)
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
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := root.Rename(name, filepath.Base(path)); err != nil {
		return err
	}
	return syncRootDirectory(root)
}

func cleanupDurableImportResources(prefix string, engine core.Engine, managed EngineSpec) error {
	path := importResourceStatePath(prefix, engine)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("unsafe import resource recovery manifest")
	}
	data, _, err := readImportSource(path, 1<<20)
	if err != nil {
		return err
	}
	var state durableImportResources
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Version != 1 || len(state.Resources) > 256 || !(pathWithin(state.Root, filepath.Dir(managed.ConfigPath)) || engine == core.EngineXray && pathWithin(state.Root, filepath.Join(filepath.Dir(managed.Binary), "imports"))) || state.Root == filepath.Dir(managed.ConfigPath) {
		return errors.New("import resource recovery manifest escapes the managed instance")
	}
	for _, resource := range state.Resources {
		if !resource.Created {
			continue
		}
		if filepath.Dir(resource.Path) != state.Root || resource.Limit <= 0 || resource.Limit > maxReleaseAssetSize {
			return errors.New("invalid import resource recovery target")
		}
		digest, present, err := protectedCoreMigrationFileDigest(resource.Path, resource.Limit)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		if digest != resource.Digest {
			return fmt.Errorf("import resource %s changed outside this transaction; recovery retained it", resource.Path)
		}
		if err := os.Remove(resource.Path); err != nil {
			return err
		}
	}
	// A reused file or an external change keeps its directory in place.
	if err := os.Remove(state.Root); err != nil && !errors.Is(err, os.ErrNotExist) {
		entries, readErr := os.ReadDir(state.Root)
		if readErr != nil || len(entries) == 0 {
			return errors.Join(err, readErr)
		}
	}
	return os.Remove(path)
}
