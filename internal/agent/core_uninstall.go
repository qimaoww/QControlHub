package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// uninstallCore removes only the binary owned by this Agent's managed core
// mapping. The configuration and panel revisions remain available for a later
// install. A QAgent service may stay on disk, but it is disabled before the
// executable is removed so boot cannot repeatedly launch a missing binary.
func (e *Executor) uninstallCore(ctx context.Context, engine core.Engine, spec EngineSpec) (string, error) {
	if err := validateProtectedDirectoryChain(filepath.Dir(spec.Binary)); err != nil {
		return "", fmt.Errorf("unsafe core binary directory: %w", err)
	}
	if err := validateCoreInstallDestination(spec.Binary); err != nil {
		return "", fmt.Errorf("unsafe core binary: %w", err)
	}
	manager := e.serviceManager()
	defaultSpec := DefaultSpecsForServiceManager(manager.Kind())[engine]
	serviceState := ""
	var err error
	if spec == defaultSpec {
		serviceState, err = validateManagedServiceForExistingDiscovery(ctx, engine, spec, manager)
	} else {
		serviceState, err = serviceStatusWithManager(ctx, manager, spec.Service)
	}
	if err != nil {
		return "", fmt.Errorf("inspect managed core service: %w", err)
	}
	if err := e.checkSharedCoreInstancesInactive(ctx, engine, spec); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(filepath.Dir(spec.Binary))
	if err != nil {
		return "", err
	}
	defer root.Close()
	files := []string{filepath.Base(spec.Binary)}
	if engine == core.EngineXray && spec == defaultSpec {
		files = append(files, xrayMigrationAssetNames[:]...)
	}
	for _, name := range files {
		if err := validateRemovableCoreFile(root, name); err != nil {
			return "", err
		}
	}
	output := ""
	if serviceState != "not-found" {
		output, err = serviceCommandAndVerifyWithManager(ctx, manager, spec.Service, core.ActionStop)
		if err != nil {
			return output, err
		}
		if err := disableServiceCompletely(ctx, spec.Service, manager); err != nil {
			return output, fmt.Errorf("core stopped but could not disable its service: %w", err)
		}
	}
	for _, name := range files {
		if err := removeCoreFile(root, name); err != nil {
			return output, fmt.Errorf("remove managed core file %s: %w", name, err)
		}
	}
	if err := syncRootDirectory(root); err != nil {
		return output, fmt.Errorf("sync core binary directory: %w", err)
	}
	return fmt.Sprintf("uninstalled %s core; managed configuration retained\n%s", engine, output), nil
}

func (e *Executor) checkSharedCoreInstancesInactive(ctx context.Context, engine core.Engine, base EngineSpec) error {
	entries, err := os.ReadDir(sharedInstanceRoot(engine))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !core.ValidAgentShareID(entry.Name()) {
			return errors.New("unrecognized shared core instance prevents uninstallation")
		}
		instance, err := sharedInstanceSpec(engine, base, entry.Name(), e.serviceManager().Kind())
		if err != nil {
			return err
		}
		status, err := serviceStatusWithManager(ctx, e.serviceManager(), instance.Service)
		if err != nil || (status != "inactive" && status != "failed") {
			return fmt.Errorf("shared %s core instance %s must be stopped before uninstallation", engine, entry.Name())
		}
	}
	return nil
}

func validateRemovableCoreFile(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("unsafe managed core file %s", name)
	}
	return validateOwner(info, "managed core file")
}

func removeCoreFile(root *os.Root, name string) error {
	if err := validateRemovableCoreFile(root, name); err != nil {
		return err
	}
	err := root.Remove(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
