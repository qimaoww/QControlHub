package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

var (
	openRCProcRoot             = "/proc"
	openRCStateRoot            = "/run/openrc"
	openRCSupervisorRoot       = "/run"
	openRCRunlevelsRoot        = "/etc/runlevels"
	openRCInitRoot             = "/etc/init.d"
	openRCSupervisorExecutable = openRCHelperExecutable("supervise-daemon", "/sbin/supervise-daemon")
)

var errOpenRCServiceProcessUnbound = errors.New("OpenRC service has no protected supervise-daemon process metadata")

type openRCProcessIdentity struct {
	PID        int
	ParentPID  int
	StartTime  string
	Executable string
	Argv       []string
}

type openRCServiceProcessIdentity struct {
	Service    string
	Supervisor openRCProcessIdentity
	Child      openRCProcessIdentity
}

func verifyOpenRCExistingServiceProcess(ctx context.Context, engine core.Engine, existing EngineSpec) (openRCServiceProcessIdentity, error) {
	identity, err := boundOpenRCServiceProcess(ctx, existing.Service)
	if err != nil {
		return openRCServiceProcessIdentity{}, err
	}
	if filepath.Clean(identity.Child.Executable) != filepath.Clean(existing.Binary) || !openRCProcessArgvMatches(engine, existing, identity.Child.Argv) {
		return openRCServiceProcessIdentity{}, errors.New("service-bound process executable or arguments no longer match the discovered core mapping")
	}
	matches, err := matchingOpenRCCoreProcessIDs(ctx, engine, existing)
	if err != nil {
		return openRCServiceProcessIdentity{}, err
	}
	if len(matches) != 1 || matches[0] != identity.Child.PID {
		return openRCServiceProcessIdentity{}, errors.New("existing OpenRC core process is ambiguous or is not uniquely owned by the reported service")
	}
	identityAgain, err := boundOpenRCServiceProcess(ctx, existing.Service)
	if err != nil || identityAgain.Supervisor.PID != identity.Supervisor.PID || identityAgain.Supervisor.StartTime != identity.Supervisor.StartTime ||
		identityAgain.Child.PID != identity.Child.PID || identityAgain.Child.StartTime != identity.Child.StartTime {
		return openRCServiceProcessIdentity{}, errors.New("service-bound OpenRC process identity changed during mapping verification")
	}
	return identity, nil
}

func matchingOpenRCCoreProcessIDs(ctx context.Context, engine core.Engine, existing EngineSpec) ([]int, error) {
	entries, err := os.ReadDir(openRCProcRoot)
	if err != nil {
		return nil, err
	}
	matches := make([]int, 0, 1)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !decimalProcessID(entry.Name()) {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		identity, err := readOpenRCProcessIdentity(pid)
		if err != nil || filepath.Clean(identity.Executable) != filepath.Clean(existing.Binary) || !openRCProcessArgvMatches(engine, existing, identity.Argv) {
			continue
		}
		matches = append(matches, pid)
	}
	return matches, nil
}

func boundOpenRCServiceProcess(ctx context.Context, service string) (openRCServiceProcessIdentity, error) {
	if !safeServiceName(service) || strings.Contains(service, ".service") {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC service name is unsafe")
	}
	if err := ctx.Err(); err != nil {
		return openRCServiceProcessIdentity{}, err
	}
	optionsDirectory := filepath.Join(openRCStateRoot, "options", service)
	childPIDPath := filepath.Join(optionsDirectory, "child_pid")
	pidfileMetadataPath := filepath.Join(optionsDirectory, "pidfile")
	childPIDText, childPIDInfo, err := readProtectedOpenRCStateMetadata(childPIDPath)
	if errors.Is(err, os.ErrNotExist) {
		return openRCServiceProcessIdentity{}, errOpenRCServiceProcessUnbound
	}
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("read supervise-daemon child PID metadata: %w", err)
	}
	childPID, err := parseOpenRCProcessID(childPIDText)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("parse supervise-daemon child PID metadata: %w", err)
	}
	pidfileValue, pidfileMetadataInfo, err := readProtectedOpenRCStateMetadata(pidfileMetadataPath)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("read supervise-daemon pidfile metadata: %w", err)
	}
	// An OpenRC init script chooses its own pidfile name, so the name is not a
	// trust anchor and pinning it to supervise-<service>.pid only rejected
	// installer layouts that are otherwise entirely provable. What establishes
	// trust is the supervisor identity verified below: the executable must be
	// the protected supervise-daemon helper, its argv must name this exact
	// service with --start, and the child must be its direct descendant running
	// the discovered binary and arguments. The path is still constrained to a
	// direct child of the run directory so nothing outside it can be read.
	supervisorPIDName, err := supervisorPIDFileName(pidfileValue)
	if err != nil {
		return openRCServiceProcessIdentity{}, err
	}
	supervisorPIDPath := filepath.Join(openRCSupervisorRoot, supervisorPIDName)
	supervisorPIDText, supervisorPIDInfo, err := readProtectedOpenRCMetadata(supervisorPIDPath)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("read supervise-daemon supervisor PID: %w", err)
	}
	supervisorPID, err := parseOpenRCProcessID(supervisorPIDText)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("parse supervise-daemon supervisor PID: %w", err)
	}

	supervisor, err := readOpenRCProcessIdentity(supervisorPID)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("read supervise-daemon supervisor identity: %w", err)
	}
	child, err := readOpenRCProcessIdentity(childPID)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("read supervise-daemon child identity: %w", err)
	}
	if child.ParentPID != supervisor.PID {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC service child is not owned by its supervise-daemon process")
	}
	if err := validatePrivilegedExecutable(openRCSupervisorExecutable); err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("unsafe supervise-daemon executable: %w", err)
	}
	expectedSupervisorExecutable, err := filepath.EvalSymlinks(openRCSupervisorExecutable)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("resolve supervise-daemon executable: %w", err)
	}
	if filepath.Clean(supervisor.Executable) != filepath.Clean(expectedSupervisorExecutable) ||
		len(supervisor.Argv) < 3 || filepath.Base(supervisor.Argv[0]) != "supervise-daemon" || supervisor.Argv[1] != service || !stringInSlice("--start", supervisor.Argv) {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC supervisor identity does not match this service's supervise-daemon invocation")
	}

	childPIDAgain, childPIDInfoAgain, err := readProtectedOpenRCStateMetadata(childPIDPath)
	if err != nil || !os.SameFile(childPIDInfo, childPIDInfoAgain) || childPIDAgain != childPIDText {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC child PID metadata changed during process verification")
	}
	pidfileValueAgain, pidfileMetadataInfoAgain, err := readProtectedOpenRCStateMetadata(pidfileMetadataPath)
	if err != nil || !os.SameFile(pidfileMetadataInfo, pidfileMetadataInfoAgain) || pidfileValueAgain != pidfileValue {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC pidfile metadata changed during process verification")
	}
	supervisorPIDAgain, supervisorPIDInfoAgain, err := readProtectedOpenRCMetadata(supervisorPIDPath)
	if err != nil || !os.SameFile(supervisorPIDInfo, supervisorPIDInfoAgain) || supervisorPIDAgain != supervisorPIDText {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC supervisor PID changed during process verification")
	}
	if alive, err := openRCProcessIdentityAlive(supervisor); err != nil || !alive {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC supervise-daemon identity changed during process verification")
	}
	if alive, err := openRCProcessIdentityAlive(child); err != nil || !alive {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC service child identity changed during process verification")
	}
	return openRCServiceProcessIdentity{Service: service, Supervisor: supervisor, Child: child}, nil
}

// supervisorPIDFileName validates the supervise-daemon pidfile recorded in
// protected OpenRC state and returns its file name. The path must be a clean
// absolute path that is a direct child of /run or /var/run and carries a plain
// .pid name; a nested directory, a traversal component, or any other character
// fails closed rather than letting service metadata point the reader at an
// arbitrary file.
func supervisorPIDFileName(pidfile string) (string, error) {
	if !filepath.IsAbs(pidfile) || pidfile != filepath.Clean(pidfile) || strings.ContainsAny(pidfile, " \t\r\n") {
		return "", errors.New("OpenRC supervise-daemon pidfile path is unsafe")
	}
	directory, name := filepath.Split(pidfile)
	if cleaned := filepath.Clean(directory); cleaned != "/run" && cleaned != "/var/run" {
		return "", errors.New("OpenRC supervise-daemon pidfile is outside the supported run directory")
	}
	if !strings.HasSuffix(name, ".pid") || len(name) <= len(".pid") || !safeOpenRCPIDFileName(name) {
		return "", errors.New("OpenRC supervise-daemon pidfile name is unsupported")
	}
	return name, nil
}

func safeOpenRCPIDFileName(name string) bool {
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '.', character == '_', character == '-':
		default:
			return false
		}
	}
	return !strings.HasPrefix(name, ".")
}

func readProtectedOpenRCMetadata(path string) (string, os.FileInfo, error) {
	return readProtectedOpenRCMetadataWithStateRoot(path, "")
}

// readProtectedOpenRCStateMetadata is reserved for child_pid and pidfile below
// /run/openrc. Stock OpenRC creates that state directory as root:root 0775, so
// this read permits the root-group write bit while all other OpenRC metadata,
// including the supervisor PID file directly below /run, stays strict.
func readProtectedOpenRCStateMetadata(path string) (string, os.FileInfo, error) {
	return readProtectedOpenRCMetadataWithStateRoot(path, openRCStateRoot)
}

func readProtectedOpenRCMetadataWithStateRoot(path, relaxedStateRoot string) (string, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() <= 0 || info.Size() > 4096 {
		return "", nil, errors.New("OpenRC process metadata is not a protected small regular file")
	}
	if err := validateOwner(info, "OpenRC process metadata"); err != nil {
		return "", nil, err
	}
	directory := filepath.Dir(path)
	var directoryErr error
	if relaxedStateRoot == "" {
		directoryErr = validateProtectedDirectoryChain(directory)
	} else {
		directoryErr = validateOpenRCStateDirectoryChain(directory, relaxedStateRoot)
	}
	if directoryErr != nil {
		return "", nil, directoryErr
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) || openedInfo.Size() != info.Size() {
		return "", nil, errors.New("OpenRC process metadata changed while it was opened")
	}
	contents, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(contents) == 0 || len(contents) > 4096 {
		return "", nil, errors.New("OpenRC process metadata is empty or too large")
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(contents), "\n"), "\r")
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", nil, errors.New("OpenRC process metadata is malformed")
	}
	return value, openedInfo, nil
}

func parseOpenRCProcessID(value string) (int, error) {
	if !decimalProcessID(value) {
		return 0, errors.New("process ID is not decimal")
	}
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 1 {
		return 0, errors.New("process ID is outside the supported range")
	}
	return pid, nil
}

func readOpenRCProcessIdentity(pid int) (openRCProcessIdentity, error) {
	processRoot := filepath.Join(openRCProcRoot, strconv.Itoa(pid))
	parentPID, startTime, err := readOpenRCProcessStat(filepath.Join(processRoot, "stat"))
	if err != nil {
		return openRCProcessIdentity{}, err
	}
	executable, err := os.Readlink(filepath.Join(processRoot, "exe"))
	if err != nil {
		return openRCProcessIdentity{}, err
	}
	argv, err := readOpenRCProcessArgv(filepath.Join(processRoot, "cmdline"))
	if err != nil {
		return openRCProcessIdentity{}, err
	}
	parentPIDAgain, startTimeAgain, err := readOpenRCProcessStat(filepath.Join(processRoot, "stat"))
	if err != nil || parentPIDAgain != parentPID || startTimeAgain != startTime {
		return openRCProcessIdentity{}, errors.New("process identity changed while executable and arguments were read")
	}
	return openRCProcessIdentity{PID: pid, ParentPID: parentPID, StartTime: startTime, Executable: filepath.Clean(executable), Argv: argv}, nil
}

func readOpenRCProcessStat(path string) (int, string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return 0, "", err
	}
	separator := strings.LastIndex(string(contents), ") ")
	if separator < 0 {
		return 0, "", errors.New("process stat is malformed")
	}
	fields := strings.Fields(string(contents)[separator+2:])
	if len(fields) < 20 || !decimalProcessID(fields[19]) {
		return 0, "", errors.New("process stat lacks a valid start time")
	}
	parentPID, err := strconv.Atoi(fields[1])
	if err != nil || parentPID < 0 {
		return 0, "", errors.New("process stat lacks a valid parent PID")
	}
	return parentPID, fields[19], nil
}

func openRCProcessIdentityAlive(identity openRCProcessIdentity) (bool, error) {
	_, startTime, err := readOpenRCProcessStat(filepath.Join(openRCProcRoot, strconv.Itoa(identity.PID), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return startTime == identity.StartTime, nil
}

func waitForOpenRCServiceProcessExit(ctx context.Context, identity openRCServiceProcessIdentity) error {
	stableContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var stableSince time.Time
	for {
		supervisorAlive, supervisorErr := openRCProcessIdentityAlive(identity.Supervisor)
		childAlive, childErr := openRCProcessIdentityAlive(identity.Child)
		if supervisorErr != nil || childErr != nil {
			return errors.Join(supervisorErr, childErr)
		}
		_, boundErr := boundOpenRCServiceProcess(stableContext, identity.Service)
		unbound := errors.Is(boundErr, errOpenRCServiceProcessUnbound)
		if !supervisorAlive && !childAlive && unbound {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= 500*time.Millisecond {
				return nil
			}
		} else {
			stableSince = time.Time{}
		}
		select {
		case <-stableContext.Done():
			return fmt.Errorf("OpenRC service-bound process remained alive or supervised after stop: %w", stableContext.Err())
		case <-ticker.C:
		}
	}
}

func decimalProcessID(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func readOpenRCProcessArgv(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 || len(contents) > 64<<10 {
		return nil, errors.New("OpenRC process command line is empty or too large")
	}
	if contents[len(contents)-1] == 0 {
		contents = contents[:len(contents)-1]
	}
	fields := strings.Split(string(contents), "\x00")
	for _, field := range fields {
		if field == "" || strings.ContainsAny(field, "\r\n") {
			return nil, errors.New("OpenRC process command line is malformed")
		}
	}
	return fields, nil
}

func openRCProcessArgvMatches(engine core.Engine, existing EngineSpec, argv []string) bool {
	if len(argv) == 0 || (argv[0] != existingServiceBinary(existing) && argv[0] != existing.Binary) {
		return false
	}
	// The official sing-box working-directory form has no supervised OpenRC
	// binding this mapping could prove, so it stays rejected here.
	configPath, configDirectory, workDirectory, ok := parseExistingArgv(engine, argv[0], argv)
	return ok && workDirectory == "" &&
		configPath == existing.ConfigPath && configDirectory == existing.ConfigDirectory
}

func validateOpenRCServiceScript(service, ownershipMarker string) error {
	if !safeServiceName(service) || strings.Contains(service, ".service") {
		return errors.New("OpenRC service name is unsafe")
	}
	path := filepath.Join(openRCInitRoot, service)
	if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
		return err
	}
	if err := validatePrivilegedExecutable(path); err != nil {
		return err
	}
	if ownershipMarker == "" {
		return nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(contents), ownershipMarker+"\n") {
		return errors.New("OpenRC service script lacks the QAgent ownership marker")
	}
	return nil
}

func parseSingleSystemdExecStart(value string) (string, string, error) {
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	if strings.ContainsAny(value, "\r\n") || strings.Contains(value, "} {") || strings.Contains(value, "; path=") {
		return "", "", errors.New("systemd ExecStart contains multiple commands")
	}
	const prefix = "{ path="
	if !strings.HasPrefix(value, prefix) {
		return "", "", errors.New("systemd ExecStart has an unsupported structure")
	}
	remainder := strings.TrimPrefix(value, prefix)
	const argvSeparator = " ; argv[]="
	argvIndex := strings.Index(remainder, argvSeparator)
	if argvIndex <= 0 {
		return "", "", errors.New("systemd ExecStart has no executable argv")
	}
	executable := remainder[:argvIndex]
	remainder = remainder[argvIndex+len(argvSeparator):]
	const metadataSeparator = " ; ignore_errors="
	metadataIndex := strings.Index(remainder, metadataSeparator)
	if metadataIndex <= 0 {
		return "", "", errors.New("systemd ExecStart has no command metadata")
	}
	argv := remainder[:metadataIndex]
	metadata := remainder[metadataIndex:]
	if !strings.HasSuffix(metadata, " }") || strings.ContainsAny(strings.TrimSuffix(metadata, " }"), "{}") {
		return "", "", errors.New("systemd ExecStart has ambiguous command metadata")
	}
	return executable, argv, nil
}

func supportedExistingExecStart(engine core.Engine, existing EngineSpec, argv string) bool {
	serviceBinary := existingServiceBinary(existing)
	configPath, configDirectory, workDirectory, ok := parseExistingArgv(engine, serviceBinary, strings.Fields(argv))
	return ok && configPath == existing.ConfigPath && configDirectory == existing.ConfigDirectory &&
		workDirectory == existing.WorkingDirectory && existingSSRustACLArg(engine, strings.Fields(argv)) == existing.ACLPath
}
