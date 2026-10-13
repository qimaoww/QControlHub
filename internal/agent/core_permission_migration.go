package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Sync installed managed services on every Agent startup. This is independent
// of whether a config needs accounting/log rewrites, and leaves stopped cores
// and boot enablement untouched. One customized service cannot block others.
func (e *Executor) syncManagedCorePermissions(ctx context.Context) error {
	e.migrationMu.Lock()
	defer e.migrationMu.Unlock()
	e.specsMu.RLock()
	specs := make(map[core.Engine]EngineSpec, len(e.Specs))
	for engine, spec := range e.Specs {
		if _, pending := e.ExistingSpecs[engine]; !pending && e.ExistingDiscoveryIssues[engine] == "" {
			specs[engine] = spec
		}
	}
	e.specsMu.RUnlock()
	manager := e.serviceManager()
	var failures []error
	for _, engine := range core.AllEngines() {
		base, configured := specs[engine]
		if !configured || base != DefaultSpecsForServiceManager(manager.Kind())[engine] {
			continue
		}
		if _, err := os.Lstat(base.Binary); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			failures = append(failures, err)
			continue
		}
		instances := []EngineSpec{base}
		entries, err := os.ReadDir(sharedInstanceRoot(engine))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, fmt.Errorf("kernel %s: enumerate shared permission migration: %w", engine, err))
		}
		for _, entry := range entries {
			if !entry.IsDir() || !core.ValidAgentShareID(entry.Name()) {
				continue
			}
			spec, err := sharedInstanceSpec(engine, base, entry.Name(), manager.Kind())
			if err == nil {
				instances = append(instances, spec)
			}
		}
		for _, spec := range instances {
			migration, cancel := context.WithTimeout(ctx, 45*time.Second)
			err := e.syncInstalledCorePermissions(migration, engine, spec)
			cancel()
			if err != nil {
				failures = append(failures, fmt.Errorf("kernel %s service %s permission migration: %w", engine, spec.Service, err))
			}
		}
	}
	return errors.Join(failures...)
}

func (e *Executor) syncInstalledCorePermissions(ctx context.Context, engine core.Engine, spec EngineSpec) error {
	manager := e.serviceManager()
	if isSharedInstanceSpec(engine, spec, manager.Kind()) {
		if err := e.ensureSharedInstanceService(ctx, engine, spec); err != nil {
			return err
		}
	} else {
		status, err := validateManagedServiceForExistingDiscovery(ctx, engine, spec, manager)
		if err != nil || status == "not-found" {
			return err
		}
	}
	status, err := manager.status(ctx, spec.Service)
	if err != nil {
		return fmt.Errorf("inspect service before permission sync (%s): %w", status, err)
	}
	if status != "active" && status != "inactive" && status != "failed" {
		return fmt.Errorf("service %s is %s; wait for its current transition before permission sync", spec.Service, status)
	}
	if err := ensureManagedCoreServiceCapabilities(ctx, engine, spec, manager); err != nil {
		return err
	}
	if err := ensureDefaultManagedConfigurationAccess(engine, spec, manager); err != nil {
		return fmt.Errorf("read config/ACL: %w; check protected /etc/qagent ownership and redeploy the Agent if its parent is inaccessible", err)
	}
	if manager.Kind() == ServiceManagerOpenRC {
		// OpenRC has no StateDirectory automatic ownership repair.
		state := "qcontrolhub-" + managedCoreAssetName(engine)
		if isSharedInstanceSpec(engine, spec, manager.Kind()) {
			state += "-" + filepath.Base(filepath.Dir(spec.ConfigPath))
		}
		if err := repairManagedCoreStateDirectory(filepath.Join("/var/lib", state)); err != nil {
			return fmt.Errorf("write state/cache directory: %w", err)
		}
	}
	if content, err := readConfigurationFile(spec.ConfigPath); err == nil {
		identity, err := managedCoreServiceIdentity()
		if err != nil {
			return err
		}
		if err := e.verifyCoreCertificateAccess(ctx, engine, spec, content, identity); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("read configuration %s after permission sync: %w", spec.ConfigPath, err)
	}
	if status == "active" {
		// Start becomes restart only for stale policy/binary metadata or an
		// actual capability/identity mismatch. Already repaired PIDs survive.
		_, err = serviceCommandAndVerifyWithManager(ctx, manager, spec.Service, core.ActionStart)
		return err
	}
	return nil
}

func repairManagedCoreStateDirectory(path string) error {
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return err
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	meta := metadataFromFileInfo(info)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !meta.ownershipKnown ||
		meta.uid != 0 && meta.uid != int(identity.uid) || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("state path %s is not a recognized protected directory; review ownership manually", path)
	}
	if meta.uid != int(identity.uid) || meta.gid != int(identity.gid) || meta.mode != 0o750 {
		// CAP_CHOWN is sufficient; set the mode while root owns the directory.
		if err := os.Chown(path, 0, 0); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o750); err != nil {
			return err
		}
		if err := os.Chown(path, int(identity.uid), int(identity.gid)); err != nil {
			return err
		}
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || name == "." {
			return walkErr
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		metadata := metadataFromFileInfo(info)
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() || metadata.mode&0o022 != 0 || !metadata.ownershipKnown || metadata.uid != 0 && metadata.uid != int(identity.uid) {
			return fmt.Errorf("state/cache %s: unsafe type, ownership or write permissions; review manually without deleting existing data", filepath.Join(path, name))
		}
		if metadata.uid != 0 {
			return nil
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && info.Mode().IsRegular() && stat.Nlink != 1 {
			return fmt.Errorf("state/cache %s has multiple hard links; repair its ownership manually", filepath.Join(path, name))
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) || metadataFromFileInfo(opened) != metadata {
			return fmt.Errorf("state/cache %s changed during permission sync; retry", name)
		}
		mode := os.FileMode(0o640)
		if info.IsDir() {
			mode = 0o750
		}
		if err := file.Chmod(mode); err != nil {
			return err
		}
		if err := file.Chown(int(identity.uid), int(identity.gid)); err != nil {
			return err
		}
		return file.Sync()
	})
}
