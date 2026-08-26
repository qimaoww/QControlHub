package agent

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	managedCoreConfigurationRoot = "/etc/qagent"
	managedCoreServiceGroup      = "qcontrolhub-core"
)

func ensureDefaultManagedConfigurationAccess(engine core.Engine, spec EngineSpec, manager *ServiceManager) error {
	defaultSpec, ok := DefaultSpecsForServiceManager(selectedServiceManager(manager).Kind())[engine]
	if !ok || spec != defaultSpec {
		return nil
	}
	_, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, spec.ConfigPath)
	return err
}

func atomicDeployManagedConfiguration(engine core.Engine, spec EngineSpec, manager *ServiceManager, content string) (string, error) {
	defaultSpec, ok := DefaultSpecsForServiceManager(selectedServiceManager(manager).Kind())[engine]
	if !ok || spec != defaultSpec {
		return atomicDeploy(spec.ConfigPath, content)
	}
	metadata, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, spec.ConfigPath)
	if err != nil {
		return "", err
	}
	return atomicDeployWithDefaultMetadata(spec.ConfigPath, content, metadata)
}

func prepareManagedConfigurationAccess(rootPath, configPath string) (fileMetadata, error) {
	group, err := user.LookupGroup(managedCoreServiceGroup)
	if err != nil {
		return fileMetadata{}, fmt.Errorf("look up managed core service group: %w", err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil || gid <= 0 {
		return fileMetadata{}, errors.New("managed core service group has an invalid numeric gid")
	}
	return prepareManagedConfigurationAccessWithGID(rootPath, configPath, gid)
}

func prepareManagedConfigurationAccessWithGID(rootPath, configPath string, gid int) (fileMetadata, error) {
	if gid <= 0 {
		return fileMetadata{}, errors.New("managed core service group has an invalid numeric gid")
	}
	rootPath = filepath.Clean(rootPath)
	configPath = filepath.Clean(configPath)
	directory := filepath.Dir(configPath)
	relative, err := filepath.Rel(rootPath, directory)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fileMetadata{}, errors.New("managed configuration path escapes its protected root")
	}
	if err := validateProtectedDirectoryChain(directory); err != nil {
		return fileMetadata{}, fmt.Errorf("managed configuration directory is unsafe: %w", err)
	}

	directories := []string{rootPath}
	if relative != "." {
		current := rootPath
		for _, component := range strings.Split(relative, string(filepath.Separator)) {
			if component == "" || component == "." || component == ".." {
				return fileMetadata{}, errors.New("managed configuration directory has an invalid component")
			}
			current = filepath.Join(current, component)
			directories = append(directories, current)
		}
	}
	for _, path := range directories {
		if err := applyManagedConfigurationMetadata(path, true, gid); err != nil {
			return fileMetadata{}, err
		}
	}
	if err := applyManagedConfigurationMetadata(configPath, false, gid); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fileMetadata{}, err
	}
	return fileMetadata{mode: 0o640, uid: 0, gid: gid, ownershipKnown: true}, nil
}

func applyManagedConfigurationMetadata(path string, directory bool, gid int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		kind := "regular file"
		if directory {
			kind = "directory"
		}
		return fmt.Errorf("managed configuration path %s is not a protected %s", path, kind)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("managed configuration path %s is writable by group or others", path)
	}
	if err := validateOwner(info, "managed configuration path"); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Mode()&os.ModeSymlink != 0 ||
		metadataFromFileInfo(opened) != metadataFromFileInfo(info) ||
		directory && !opened.IsDir() || !directory && !opened.Mode().IsRegular() {
		return errors.New("managed configuration path changed while it was being opened")
	}
	mode := os.FileMode(0o640)
	if directory {
		mode = 0o750
	}
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("set managed configuration mode: %w", err)
	}
	if err := file.Chown(0, gid); err != nil {
		return fmt.Errorf("set managed configuration ownership: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync managed configuration metadata: %w", err)
	}
	return nil
}
