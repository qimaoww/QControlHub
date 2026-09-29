package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// uninstallCore removes this Agent's managed core binary and its version
// backups. The configuration and panel revisions remain available for a later
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
	files, err := managedCoreRemovalFiles(root, engine, spec, manager)
	if err != nil {
		return "", err
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

func managedCoreRemovalFiles(root *os.Root, engine core.Engine, spec EngineSpec, manager *ServiceManager) ([]string, error) {
	binaryName := filepath.Base(spec.Binary)
	managedFiles := []string{binaryName}
	if engine == core.EngineXray && spec == DefaultSpecsForServiceManager(manager.Kind())[engine] {
		managedFiles = append(managedFiles, xrayMigrationAssetNames[:]...)
	}
	var files []string
	for _, name := range managedFiles {
		backups, err := managedCoreFileBackups(root, name)
		if err != nil {
			return nil, err
		}
		files = append(files, backups...)
		if name != binaryName {
			files = append(files, name)
		}
	}
	// Leave the installed executable until last. A failed earlier removal can
	// then be retried from the panel while the core still appears installed.
	files = append(files, binaryName)
	return files, nil
}

func managedCoreFileBackups(root *os.Root, fileName string) ([]string, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var backups []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, fileName+".bak-") {
			continue
		}
		if !validManagedCoreBackupName(fileName, name) {
			return nil, fmt.Errorf("unrecognized managed core backup %s", name)
		}
		backups = append(backups, name)
	}
	return backups, nil
}

func validManagedCoreBackupName(fileName, name string) bool {
	const timestampLayout = "20060102T150405Z"
	suffix := strings.TrimPrefix(name, fileName+".bak-")
	if len(suffix) != len(timestampLayout)+1+12 || suffix[len(timestampLayout)] != '-' {
		return false
	}
	timestamp := suffix[:len(timestampLayout)]
	parsed, err := time.Parse(timestampLayout, timestamp)
	if err != nil || parsed.UTC().Format(timestampLayout) != timestamp {
		return false
	}
	for _, character := range suffix[len(timestampLayout)+1:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (e *Executor) checkSharedCoreInstancesInactive(ctx context.Context, engine core.Engine, base EngineSpec) error {
	return e.checkSharedCoreInstancesInactiveAtRoot(ctx, engine, base, sharedInstanceRoot(engine))
}

func (e *Executor) checkSharedCoreInstancesInactiveAtRoot(ctx context.Context, engine core.Engine, base EngineSpec, directory string) error {
	entries, err := os.ReadDir(directory)
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
		// A stopped instance may still be enabled at boot. The shared unit
		// uses this base binary, so removing it would leave an enabled unit
		// attempting to start a missing executable after reboot.
		enablement, err := serviceEnableState(ctx, instance.Service, e.serviceManager())
		if err != nil {
			return fmt.Errorf("inspect shared %s core instance %s enablement: %w", engine, entry.Name(), err)
		}
		if enablement != "disabled" {
			return fmt.Errorf("shared %s core instance %s must be disabled before uninstallation: state %q", engine, entry.Name(), enablement)
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
