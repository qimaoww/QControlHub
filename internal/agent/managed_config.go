package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	managedCoreConfigurationRoot = "/etc/qagent"
	managedCoreServiceGroup      = "qcontrolhub-core"
)

type commandIdentity struct {
	uid    uint32
	gid    uint32
	groups []uint32
}

func managedCoreServiceIdentity() (commandIdentity, error) {
	return lookupCommandIdentity(managedCoreServiceGroup, managedCoreServiceGroup)
}

func lookupCommandIdentity(userName, groupName string) (commandIdentity, error) {
	account, err := user.Lookup(userName)
	if err != nil {
		return commandIdentity{}, fmt.Errorf("look up managed core service user: %w", err)
	}
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return commandIdentity{}, fmt.Errorf("look up managed core service group: %w", err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 {
		return commandIdentity{}, errors.New("managed core service user has an invalid numeric uid")
	}
	gid, err := strconv.ParseUint(group.Gid, 10, 32)
	if err != nil || gid == 0 {
		return commandIdentity{}, errors.New("managed core service group has an invalid numeric gid")
	}
	groupIDs, err := account.GroupIds()
	if err != nil {
		return commandIdentity{}, fmt.Errorf("look up managed core supplementary groups: %w", err)
	}
	groups := make([]uint32, 0, len(groupIDs)+1)
	seen := map[uint32]struct{}{uint32(gid): {}}
	groups = append(groups, uint32(gid))
	for _, raw := range groupIDs {
		value, parseErr := strconv.ParseUint(raw, 10, 32)
		if parseErr != nil || value == 0 {
			return commandIdentity{}, errors.New("managed core service has an invalid supplementary gid")
		}
		current := uint32(value)
		if _, exists := seen[current]; exists {
			continue
		}
		seen[current] = struct{}{}
		groups = append(groups, current)
	}
	sort.Slice(groups, func(left, right int) bool { return groups[left] < groups[right] })
	return commandIdentity{uid: uint32(uid), gid: uint32(gid), groups: groups}, nil
}

func ensureDefaultManagedConfigurationAccess(engine core.Engine, spec EngineSpec, manager *ServiceManager) error {
	defaultSpec, ok := DefaultSpecsForServiceManager(selectedServiceManager(manager).Kind())[engine]
	if !ok || spec != defaultSpec && !isSharedInstanceSpec(engine, spec, selectedServiceManager(manager).Kind()) {
		return nil
	}
	if err := ensureManagedCoreBinaryAccess(engine, spec, manager); err != nil {
		return err
	}
	if _, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, spec.ConfigPath); err != nil {
		return err
	}
	if engine == core.EngineShadowsocksRust {
		path := shadowsocksRustACLPath
		if spec.ACLPath != "" {
			path = spec.ACLPath
		}
		_, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, path)
		return err
	}
	return nil
}

func ensureManagedConfigurationRootAccess(gid int) error {
	info, err := os.Lstat(managedCoreConfigurationRoot)
	if err != nil {
		return err
	}
	meta := metadataFromFileInfo(info)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !meta.ownershipKnown || meta.uid != 0 || meta.mode&0o022 != 0 {
		return fmt.Errorf("configuration root %s is not a protected root-owned directory", managedCoreConfigurationRoot)
	}
	if meta.gid == gid && meta.mode == 0o750 {
		return nil
	}
	if err := applyManagedConfigurationMetadata(managedCoreConfigurationRoot, true, gid); err == nil {
		return nil
	}
	// ProtectSystem on older Agents can make just this parent read-only.
	// The helper grants CHOWN only and writes to this fixed protected root.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	syncer := defaultCoreUnitCapabilitySyncer()
	syncer.dropInRoot = managedCoreConfigurationRoot
	syncer.metadataWrites = true
	for _, executable := range []string{syncer.systemdRunPath, syncer.installPath} {
		if err := validatePrivilegedExecutable(executable); err != nil {
			return err
		}
	}
	if err := syncer.runHelper(ctx, nil, syncer.installPath, "-d", "-o", "root", "-g", strconv.Itoa(gid), "-m", "0750", managedCoreConfigurationRoot); err != nil {
		return fmt.Errorf("repair configuration parent traversal for non-root cores: %w; redeploy the current Agent to restore root:qcontrolhub-core 0750 on /etc/qagent", err)
	}
	return nil
}

func ensureManagedCoreBinaryAccess(engine core.Engine, spec EngineSpec, manager *ServiceManager) error {
	base := DefaultSpecsForServiceManager(selectedServiceManager(manager).Kind())[engine]
	if spec != base && !isSharedInstanceSpec(engine, spec, selectedServiceManager(manager).Kind()) {
		return nil
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return err
	}
	for _, path := range []string{filepath.Dir(filepath.Dir(spec.Binary)), filepath.Dir(spec.Binary)} {
		if err := applyManagedConfigurationMetadata(path, true, int(identity.gid)); err != nil {
			return fmt.Errorf("kernel %s: traverse binary directory %s: %w", engine, path, err)
		}
	}
	if err := validatePrivilegedExecutable(spec.Binary); errors.Is(err, os.ErrNotExist) {
		return nil // First install has not staged the executable yet.
	} else if err != nil {
		return fmt.Errorf("kernel %s: execute %s: %w", engine, spec.Binary, err)
	}
	file, err := os.Open(spec.Binary)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(spec.Binary)
	if err != nil || !os.SameFile(opened, current) || !opened.Mode().IsRegular() || metadataFromFileInfo(opened) != metadataFromFileInfo(current) {
		return fmt.Errorf("kernel %s: binary changed while repairing execute access", engine)
	}
	if opened.Mode().Perm() == 0o755 && opened.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 {
		return nil
	}
	if err := file.Chmod(0o755); err != nil {
		return fmt.Errorf("kernel %s: repair binary execute access: %w", engine, err)
	}
	return file.Sync()
}

func atomicDeployManagedConfiguration(engine core.Engine, spec EngineSpec, manager *ServiceManager, content string) (string, error) {
	defaultSpec, ok := DefaultSpecsForServiceManager(selectedServiceManager(manager).Kind())[engine]
	if !ok || spec != defaultSpec && !isSharedInstanceSpec(engine, spec, selectedServiceManager(manager).Kind()) {
		return atomicDeploy(spec.ConfigPath, content)
	}
	metadata, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, spec.ConfigPath)
	if err != nil {
		return "", err
	}
	if err := writeManagedConfigFiles(engine, spec.ConfigPath, content, metadata); err != nil {
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
	if rootPath == managedCoreConfigurationRoot {
		if err := ensureManagedConfigurationRootAccess(gid); err != nil {
			return fileMetadata{}, err
		}
	}

	// The root is synchronized separately through a bounded helper when an
	// older Agent mount exposes only individual engine directories as writable.
	directories := make([]string, 0)
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
	if stat, ok := info.Sys().(*syscall.Stat_t); !directory && ok && stat.Nlink != 1 {
		return fmt.Errorf("managed configuration path %s has multiple hard links; review ownership manually", path)
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
	if metadata := metadataFromFileInfo(opened); metadata.mode == mode && metadata.uid == 0 && metadata.gid == gid && opened.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 {
		return nil
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
