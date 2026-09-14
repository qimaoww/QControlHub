package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func supportedExistingService(engine core.Engine, service string) bool {
	return supportedExistingServiceForManager(defaultSystemdServiceManager(), engine, service)
}

func supportedExistingServiceForManager(manager *ServiceManager, engine core.Engine, service string) bool {
	if manager != nil && manager.Kind() == ServiceManagerOpenRC {
		return (engine == core.EngineXray && service == "xray") ||
			(engine == core.EngineSingBox && (service == "sing-box" || service == "singbox"))
	}
	return (engine == core.EngineXray && service == "xray.service") ||
		(engine == core.EngineShadowsocksRust && service == "shadowsocks-rust.service") ||
		(engine == core.EngineSingBox && (service == "sing-box.service" || service == "singbox.service"))
}

func validatePrivilegedExecutable(path string) error {
	return validateExecutableMetadata(path, false)
}

func validateExecutableMetadata(path string, allowOrphanOwner bool) error {
	if !filepath.IsAbs(path) {
		return errors.New("executable path is not absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("executable must be a regular, non-symlink file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return errors.New("file is not executable")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.New("executable is writable by group or others")
	}
	if err := validateOwner(info, "privileged executable"); err != nil {
		if !allowOrphanOwner || !fileOwnerIsInactiveAndUnassigned(info) {
			return err
		}
	}
	directoryInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() || directoryInfo.Mode().Perm()&0o022 != 0 {
		return errors.New("executable directory is symlinked or writable by group/others")
	}
	return validateOwner(directoryInfo, "executable directory")
}

func existingServiceBinary(spec EngineSpec) string {
	if spec.ServiceBinary != "" {
		return spec.ServiceBinary
	}
	return spec.Binary
}

// validateExistingServiceExecutable accepts the real core directly, a symlink
// to that core, or one narrowly defined forwarding script. The forwarding
// script must contain exactly a /bin/sh shebang and an unconditional
// `exec <protected-real-core> "$@"`; arbitrary wrappers are never invoked or
// copied into the managed core namespace.
func validateExistingServiceExecutable(spec EngineSpec) error {
	serviceBinary := existingServiceBinary(spec)
	if strings.ContainsAny(serviceBinary+spec.Binary, " \t\r\n") {
		return errors.New("service executable mapping contains unsupported whitespace")
	}
	if serviceBinary == spec.Binary {
		if err := validateProtectedDirectoryChain(filepath.Dir(spec.Binary)); err != nil {
			return fmt.Errorf("service executable parent chain: %w", err)
		}
		return validateExistingCoreExecutable(spec.Binary)
	}
	if !filepath.IsAbs(serviceBinary) {
		return errors.New("service executable path is not absolute")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(serviceBinary)); err != nil {
		return fmt.Errorf("service executable parent chain: %w", err)
	}
	serviceInfo, err := os.Lstat(serviceBinary)
	if err != nil {
		return err
	}
	if serviceInfo.Mode()&os.ModeSymlink == 0 {
		return errors.New("alternate service executable must be a symlink")
	}
	resolved, err := os.Readlink(serviceBinary)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(serviceBinary), resolved)
	}
	resolved = filepath.Clean(resolved)
	resolvedInfo, err := os.Lstat(resolved)
	if err != nil {
		return err
	}
	if resolvedInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("service executable must use at most one symlink")
	}
	if resolved == spec.Binary {
		if err := validateProtectedDirectoryChain(filepath.Dir(spec.Binary)); err != nil {
			return fmt.Errorf("real core parent chain: %w", err)
		}
		return validateExistingCoreExecutable(spec.Binary)
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(resolved)); err != nil {
		return fmt.Errorf("forwarder parent chain: %w", err)
	}
	if err := validatePrivilegedExecutable(resolved); err != nil {
		return fmt.Errorf("forwarder script: %w", err)
	}
	contents, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}
	if len(contents) > 1024 {
		return errors.New("forwarder script exceeds the supported fixed form")
	}
	want := "#!/bin/sh\nexec " + spec.Binary + " \"$@\"\n"
	if string(contents) != want {
		return errors.New("service wrapper is not the supported fixed exec forwarder")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(spec.Binary)); err != nil {
		return fmt.Errorf("real core parent chain: %w", err)
	}
	return validateExistingCoreExecutable(spec.Binary)
}

func validateNativeCoreExecutable(path string) error {
	return validateNativeCoreExecutableWithOwnerPolicy(path, false)
}

// validateExistingCoreExecutable permits one historical installer artifact:
// 233boy archives can preserve a numeric owner such as 1001 even though that
// UID has no account on the target. The exception applies only to fixed,
// whitelisted real-core paths and only while no live thread holds that UID.
// Every managed/helper executable and every other source remains root-owned.
func validateExistingCoreExecutable(path string) error {
	return validateNativeCoreExecutableWithOwnerPolicy(path, installerCoreAllowsOrphanOwner(path))
}

// existingCoreInvocationSpec invokes every fixed installer-path core through a
// root-owned private copy without restoring CAP_DAC_OVERRIDE to QAgent. This
// covers both historical orphan-owned archives with a missing other-execute
// bit and installer directories whose execution policy can deny the original
// path even when the core itself is root-owned. Every source must first pass
// the full owner-policy, protected-path, regular-file, and native-core checks;
// cores outside the four fixed compatibility paths continue to run in place.
func (e *Executor) existingCoreInvocationSpec(engine core.Engine, managed, existing EngineSpec) (EngineSpec, func(), error) {
	if err := validateProtectedDirectoryChain(filepath.Dir(existing.Binary)); err != nil {
		return EngineSpec{}, func() {}, err
	}
	if err := validateExistingCoreExecutable(existing.Binary); err != nil {
		return EngineSpec{}, func() {}, err
	}
	info, err := os.Lstat(existing.Binary)
	if err != nil {
		return EngineSpec{}, func() {}, err
	}
	if !installerCoreAllowsOrphanOwner(existing.Binary) {
		return existing, func() {}, nil
	}

	stagingDirectory := filepath.Dir(managed.ConfigPath)
	if e != nil && e.MigrationMarkerPrefix != "" {
		stagingDirectory = filepath.Dir(e.MigrationMarkerPrefix)
	}
	if !filepath.IsAbs(stagingDirectory) {
		return EngineSpec{}, func() {}, errors.New("existing core invocation directory is not absolute")
	}
	if _, err := os.Lstat(stagingDirectory); errors.Is(err, os.ErrNotExist) {
		if err := validateProtectedDirectoryChain(filepath.Dir(stagingDirectory)); err != nil {
			return EngineSpec{}, func() {}, fmt.Errorf("existing core invocation directory parent is unsafe: %w", err)
		}
		if err := os.Mkdir(stagingDirectory, 0o700); err != nil {
			return EngineSpec{}, func() {}, err
		}
	} else if err != nil {
		return EngineSpec{}, func() {}, err
	}
	if err := validateProtectedDirectoryChain(stagingDirectory); err != nil {
		return EngineSpec{}, func() {}, fmt.Errorf("existing core invocation directory is unsafe: %w", err)
	}

	destinationRoot, err := os.OpenRoot(stagingDirectory)
	if err != nil {
		return EngineSpec{}, func() {}, err
	}
	tempName, err := randomCoreTempName(destinationRoot)
	if err != nil {
		destinationRoot.Close()
		return EngineSpec{}, func() {}, err
	}
	cleanup := func() {
		_ = destinationRoot.Remove(tempName)
		_ = destinationRoot.Close()
	}

	sourceInfo, err := os.Lstat(existing.Binary)
	if err != nil || !os.SameFile(info, sourceInfo) {
		cleanup()
		return EngineSpec{}, func() {}, errors.New("existing core binary changed before its invocation copy was opened")
	}
	sourceRoot, err := os.OpenRoot(filepath.Dir(existing.Binary))
	if err != nil {
		cleanup()
		return EngineSpec{}, func() {}, err
	}
	input, err := sourceRoot.Open(filepath.Base(existing.Binary))
	if err != nil {
		sourceRoot.Close()
		cleanup()
		return EngineSpec{}, func() {}, err
	}
	openedInfo, err := input.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		input.Close()
		sourceRoot.Close()
		cleanup()
		return EngineSpec{}, func() {}, errors.New("existing core binary changed while its invocation copy was opened")
	}
	if openedInfo.Size() <= 0 || openedInfo.Size() > maxReleaseAssetSize {
		input.Close()
		sourceRoot.Close()
		cleanup()
		return EngineSpec{}, func() {}, errors.New("existing core invocation copy source has an invalid size")
	}

	output, err := destinationRoot.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		input.Close()
		sourceRoot.Close()
		cleanup()
		return EngineSpec{}, func() {}, err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, maxReleaseAssetSize+1))
	input.Close()
	sourceRoot.Close()
	if copyErr == nil && (written <= 0 || written > maxReleaseAssetSize) {
		copyErr = errors.New("existing core invocation copy exceeded the supported limit")
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		cleanup()
		return EngineSpec{}, func() {}, copyErr
	}
	if closeErr != nil {
		cleanup()
		return EngineSpec{}, func() {}, closeErr
	}
	if err := destinationRoot.Chmod(tempName, 0o700); err != nil {
		cleanup()
		return EngineSpec{}, func() {}, err
	}

	invocationPath := filepath.Join(stagingDirectory, tempName)
	if err := validateNativeCoreExecutable(invocationPath); err != nil {
		cleanup()
		return EngineSpec{}, func() {}, fmt.Errorf("validate existing core invocation copy: %w", err)
	}
	invocation := existing
	invocation.Binary = invocationPath
	invocation.commandDirectory = filepath.Dir(existing.Binary)
	if engine == core.EngineXray {
		invocation.commandEnv = "XRAY_LOCATION_ASSET=" + filepath.Dir(existing.Binary)
	}
	return invocation, cleanup, nil
}

func validateNativeCoreExecutableWithOwnerPolicy(path string, allowOrphanOwner bool) error {
	if err := validateExecutableMetadata(path, allowOrphanOwner); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	prefix := make([]byte, 2)
	if _, err := io.ReadFull(file, prefix); err != nil {
		return err
	}
	if string(prefix) == "#!" {
		return errors.New("real core executable must not be a script")
	}
	return nil
}

func validateProtectedDirectoryChain(directory string) error {
	return validateDirectoryChain(directory, false)
}

// validateOpenRCStateDirectoryChain validates a directory inside OpenRC's own
// runtime state. OpenRC creates /run/openrc as root:root 0775. Tolerating
// exactly that policy shape — root owner, root group, no world-write — makes
// the stock state readable. This is a real but deliberately narrow relaxation
// because a non-root account could be a member of gid 0; every path outside the
// supplied OpenRC state root keeps the stricter rule.
func validateOpenRCStateDirectoryChain(directory, stateRoot string) error {
	relative, err := filepath.Rel(stateRoot, directory)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("OpenRC state directory is outside the OpenRC state root")
	}
	if err := validateDirectoryChain(directory, true); err != nil {
		return err
	}
	// The gid-0 write exception ends at the state root. Its parent chain (for
	// stock OpenRC, /run and /) is still a general protected path and therefore
	// must remain non-writable by both group and others.
	return validateProtectedDirectoryChain(filepath.Dir(filepath.Clean(stateRoot)))
}

func validateDirectoryChain(directory string, allowRootGroupWrite bool) error {
	if !filepath.IsAbs(directory) {
		return errors.New("path is not absolute")
	}
	for {
		info, err := os.Lstat(directory)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%s is not a real directory", directory)
		}
		if info.Mode().Perm()&0o022 != 0 {
			// A sticky directory is a safe traversal boundary: another user
			// cannot replace a protected child entry. This keeps tests and
			// deliberately staged installations below /tmp safe without
			// accepting a writable non-sticky parent.
			if info.Mode()&os.ModeSticky != 0 {
				if err := validateOwnerOrRoot(info, "sticky protected path parent"); err != nil {
					return err
				}
				return nil
			}
			if !allowRootGroupWrite || !rootOwnedRootGroupDirectory(info) {
				return fmt.Errorf("%s is writable by group or others", directory)
			}
		}
		if err := validateOwner(info, "protected path parent"); err != nil {
			return err
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return nil
		}
		directory = parent
	}
}

// rootOwnedRootGroupDirectory reports whether a directory has the exact relaxed
// OpenRC ownership shape: root owner, root group, and no world-write bit. Any
// unknown ownership fails closed.
func rootOwnedRootGroupDirectory(info os.FileInfo) bool {
	if info.Mode().Perm()&0o002 != 0 {
		return false
	}
	uid, gid, known := fileOwnership(info)
	return known && uid == 0 && gid == 0
}

// validateExistingSpecPaths checks the pure structural path constraints of an
// existing core mapping: absolute executable and configuration paths, no
// whitespace anywhere, and an absolute configuration/working directory for
// sing-box. It is independent of the running UID so the same fail-closed rule
// can be asserted by tests running as root or as an unprivileged CI user.
func validateExistingSpecPaths(engine core.Engine, spec EngineSpec) error {
	// A directory-authoritative mapping carries no main configuration file, so
	// the confdir stands in as the single required absolute source.
	configPathMapped := filepath.IsAbs(spec.ConfigPath) ||
		(spec.ConfigPath == "" && filepath.IsAbs(spec.ConfigDirectory))
	if !filepath.IsAbs(spec.Binary) || !configPathMapped {
		return fmt.Errorf("existing %s paths must be absolute", engine)
	}
	for label, path := range map[string]string{
		"binary": spec.Binary, "configuration": spec.ConfigPath,
		"configuration directory": spec.ConfigDirectory, "service executable": existingServiceBinary(spec),
		"working directory": spec.WorkingDirectory,
		"ACL":               spec.ACLPath,
	} {
		if strings.ContainsAny(path, " \t\r\n") {
			return fmt.Errorf("existing %s %s path contains unsupported whitespace", engine, label)
		}
	}
	// Xray reads a confdir via -confdir and sing-box via -C; both are supported.
	// A working directory remains a sing-box-only packaging shape.
	if spec.ConfigDirectory != "" && !filepath.IsAbs(spec.ConfigDirectory) {
		return fmt.Errorf("existing %s configuration directory is unsupported or not absolute", engine)
	}
	if spec.WorkingDirectory != "" && (engine != core.EngineSingBox || !filepath.IsAbs(spec.WorkingDirectory)) {
		return fmt.Errorf("existing %s working directory is unsupported or not absolute", engine)
	}
	if spec.ACLPath != "" && (engine != core.EngineShadowsocksRust || spec.ACLPath != filepath.Join(filepath.Dir(spec.ConfigPath), "block_cn.acl")) {
		return fmt.Errorf("existing %s ACL path is unsupported", engine)
	}
	if engine == core.EngineShadowsocksRust && spec.ConfigDirectory != "" {
		return errors.New("SS Rust configuration directories are unsupported")
	}
	return nil
}
