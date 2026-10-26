package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const managedCoreCapabilityDropIn = `[Service]
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
`

func managedCapabilityDropInVariants(service string) [][]byte {
	variants := [][]byte{[]byte(managedCoreCapabilityDropIn)}
	if managedCoreServiceName(service) {
		variants = append(variants, []byte(strings.ReplaceAll(managedCoreCapabilityDropIn, "=CAP_NET_BIND_SERVICE", "=CAP_NET_BIND_SERVICE CAP_NET_ADMIN")))
	}
	if managedCoreNeedsNetlink(service) {
		variants = append(variants, []byte(string(variants[1])+"RestrictAddressFamilies=AF_NETLINK\n"))
	}
	return variants
}

type coreUnitCapabilitySyncer struct {
	outboundMarks  bool
	metadataWrites bool
	systemctlPath  string
	systemdRunPath string
	installPath    string
	teePath        string
	movePath       string
	removePath     string
	dropInRoot     string
}

func defaultCoreUnitCapabilitySyncer() coreUnitCapabilitySyncer {
	return coreUnitCapabilitySyncer{
		systemctlPath:  systemctlPath,
		systemdRunPath: "/usr/bin/systemd-run",
		installPath:    "/usr/bin/install",
		teePath:        "/usr/bin/tee",
		movePath:       "/usr/bin/mv",
		removePath:     "/usr/bin/rm",
		dropInRoot:     "/etc/systemd/system",
	}
}

// Distribution coreutils may use direct symlinks to a multicall executable.
// Keep the original command name (argv[0]), but accept only a protected link
// directly to a protected regular executable, never another symlink chain.
func validateManagedUnitHelper(path string) error {
	if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return validatePrivilegedExecutable(path)
	}
	if err := validateOwner(info, "managed-unit helper link"); err != nil {
		return err
	}
	target, err := os.Readlink(path)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(target)); err != nil {
		return err
	}
	return validatePrivilegedExecutable(target)
}

func ensureManagedCoreServiceCapabilities(ctx context.Context, engine core.Engine, spec EngineSpec, managers ...*ServiceManager) error {
	manager := defaultSystemdServiceManager()
	if len(managers) > 0 && managers[0] != nil {
		manager = managers[0]
	}
	defaultSpec, exists := DefaultSpecsForServiceManager(manager.Kind())[engine]
	if !exists || spec != defaultSpec && !isSharedInstanceSpec(engine, spec, manager.Kind()) {
		return nil
	}
	if spec == defaultSpec {
		if _, err := validateManagedServiceForExistingDiscovery(ctx, engine, spec, manager); err != nil {
			return fmt.Errorf("kernel %s service %s: permission sync refused custom service/override: %w; restore the bundled service or maintain its permissions manually", engine, spec.Service, err)
		}
		if manager.Kind() == ServiceManagerSystemd && engine == core.EngineMihomo {
			if err := syncManagedMihomoTemplate(ctx, spec); err != nil {
				return err
			}
		}
	} else if manager.Kind() == ServiceManagerSystemd {
		if err := validateManagedSharedService(ctx, engine, spec); err != nil {
			return fmt.Errorf("kernel %s service %s: permission sync refused shared service/override: %w", engine, spec.Service, err)
		}
	}
	// Alpine's managed OpenRC scripts grant CAP_NET_BIND_SERVICE when they
	// launch the non-root core. No unit drop-in is needed or available there.
	if manager.Kind() == ServiceManagerOpenRC {
		if isSharedInstanceSpec(engine, spec, manager.Kind()) {
			return ensureOpenRCSharedInstanceService(engine, spec)
		}
		return ensureOpenRCOutboundMarkCapability(spec.Service)
	}
	syncer := defaultCoreUnitCapabilitySyncer()
	syncer.outboundMarks = engine == core.EngineMihomo || engine == core.EngineShadowsocksRust || engine == core.EngineSingBox || engine == core.EngineXray
	if err := syncer.ensure(ctx, spec.Service); err != nil {
		return fmt.Errorf("prepare managed systemd service %s capabilities and network access: %w", spec.Service, err)
	}
	return nil
}

func (syncer coreUnitCapabilitySyncer) ensure(ctx context.Context, service string) error {
	if !managedCoreServiceName(service) {
		return errors.New("refusing to update a non-QAgent core service")
	}
	if syncer.dropInRoot == "" || !filepath.IsAbs(syncer.dropInRoot) {
		return errors.New("managed systemd drop-in root must be absolute")
	}
	if err := validateManagedUnitHelper(syncer.systemctlPath); err != nil {
		return fmt.Errorf("unsafe managed-unit helper %s: %w", syncer.systemctlPath, err)
	}

	ensureContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	configured, err := syncer.configured(ensureContext, service)
	if err != nil || configured {
		return err
	}
	for _, executable := range []string{syncer.systemdRunPath, syncer.installPath, syncer.teePath, syncer.movePath, syncer.removePath} {
		if err := validateManagedUnitHelper(executable); err != nil {
			return fmt.Errorf("unsafe managed-unit helper %s: %w", executable, err)
		}
	}
	if err := validateManagedUnitDirectory(syncer.dropInRoot); err != nil {
		return err
	}

	dropInDirectory := filepath.Join(syncer.dropInRoot, service+".d")
	dropInPath := filepath.Join(dropInDirectory, "10-qcontrolhub-bind-low-ports.conf")
	if err := syncer.runHelper(ensureContext, nil, syncer.installPath, "-d", "-m", "0755", dropInDirectory); err != nil {
		return fmt.Errorf("create managed systemd drop-in directory: %w", err)
	}
	if err := validateManagedUnitDirectory(dropInDirectory); err != nil {
		return err
	}
	if err := validateManagedUnitFile(dropInPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if current, err := os.ReadFile(dropInPath); err == nil && !matchesAnyManagedDropIn(current, managedCapabilityDropInVariants(service)) {
		return fmt.Errorf("refusing administrator-modified permission drop-in %s; review it and restore the QControlHub content before retrying", dropInPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	suffix, err := randomSuffix(10)
	if err != nil {
		return err
	}
	temporaryPath := filepath.Join(dropInDirectory, ".qcontrolhub-bind-low-ports-"+suffix+".tmp")
	content := managedCoreCapabilityDropIn
	if syncer.outboundMarks {
		content = strings.ReplaceAll(content, "=CAP_NET_BIND_SERVICE", "=CAP_NET_BIND_SERVICE CAP_NET_ADMIN")
	}
	if managedCoreNeedsNetlink(service) {
		// Repeated positive directives extend systemd's existing allowlist.
		// Keep the rest of the managed sandbox and grant only netlink sockets.
		content += "RestrictAddressFamilies=AF_NETLINK\n"
	}
	if err := syncer.runHelper(ensureContext, []byte(content), syncer.teePath, temporaryPath); err != nil {
		return fmt.Errorf("write managed systemd drop-in: %w", err)
	}
	moved := false
	defer func() {
		if !moved {
			cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			_ = syncer.runHelper(cleanupContext, nil, syncer.removePath, "-f", temporaryPath)
		}
	}()
	if err := syncer.runHelper(ensureContext, nil, syncer.movePath, "-fT", temporaryPath, dropInPath); err != nil {
		return fmt.Errorf("install managed systemd drop-in: %w", err)
	}
	moved = true
	if err := validateManagedUnitFile(dropInPath); err != nil {
		return err
	}
	if output, err := run(ensureContext, syncer.systemctlPath, "daemon-reload"); err != nil {
		return fmt.Errorf("reload systemd after managed service update: %w: %s", err, output)
	}
	configured, err = syncer.configured(ensureContext, service)
	if err != nil {
		return err
	}
	if !configured {
		return errors.New("systemd did not load the required managed core capabilities and address families")
	}
	return nil
}

func (syncer coreUnitCapabilitySyncer) configured(ctx context.Context, service string) (bool, error) {
	output, err := run(ctx, syncer.systemctlPath, "show", service,
		"--property=AmbientCapabilities", "--property=CapabilityBoundingSet", "--property=RestrictAddressFamilies", "--no-pager")
	if err != nil {
		return false, fmt.Errorf("inspect managed systemd service capabilities and address families: %w: %s", err, output)
	}
	properties := make(map[string]string, 3)
	for _, line := range strings.Split(output, "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			properties[name] = strings.ToLower(value)
		}
	}
	networkReady := true
	if managedCoreNeedsNetlink(service) {
		families, present := properties["RestrictAddressFamilies"]
		networkReady = present && systemdAddressFamilyAllowed(families, "af_netlink")
	}
	return networkReady && capabilityListContains(properties["AmbientCapabilities"], "cap_net_bind_service") &&
		capabilityListContains(properties["CapabilityBoundingSet"], "cap_net_bind_service") &&
		(!syncer.outboundMarks || capabilityListContains(properties["AmbientCapabilities"], "cap_net_admin") && capabilityListContains(properties["CapabilityBoundingSet"], "cap_net_admin")), nil
}

func systemdAddressFamilyAllowed(value, family string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true // An empty RestrictAddressFamilies property is unrestricted.
	}
	if denied, negative := strings.CutPrefix(value, "~"); negative {
		return !capabilityListContains(denied, family)
	}
	return capabilityListContains(value, family)
}

func capabilityListContains(value, capability string) bool {
	for _, item := range strings.Fields(value) {
		if item == capability {
			return true
		}
	}
	return false
}

func (syncer coreUnitCapabilitySyncer) runHelper(ctx context.Context, input []byte, executable string, arguments ...string) error {
	capabilities := ""
	if syncer.metadataWrites {
		capabilities = "CAP_CHOWN"
	}
	runnerArguments := []string{
		"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec",
		"--property=User=root", "--property=UMask=0022", "--property=NoNewPrivileges=yes",
		"--property=CapabilityBoundingSet=" + capabilities, "--property=AmbientCapabilities=" + capabilities,
		"--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=PrivateTmp=yes",
		"--property=PrivateDevices=yes", "--property=RestrictAddressFamilies=AF_UNIX",
		"--property=ReadWritePaths=" + syncer.dropInRoot, "--", executable,
	}
	runnerArguments = append(runnerArguments, arguments...)
	command := exec.CommandContext(ctx, syncer.systemdRunPath, runnerArguments...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	command.Stdin = bytes.NewReader(input)
	configureCommand(command)
	output := &boundedOutput{limit: 16 << 10}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(strings.ToValidUTF8(output.String(), "�"))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func managedCoreServiceName(service string) bool {
	_, ok := managedCoreServiceEngine(service)
	return ok
}

func managedCoreServiceEngine(service string) (core.Engine, bool) {
	for _, engine := range core.AllEngines() {
		base := "qagent-" + managedCoreAssetName(engine)
		if service == base+".service" {
			return engine, true
		}
		if id, found := strings.CutPrefix(strings.TrimSuffix(service, ".service"), base+"@"); found && strings.HasSuffix(service, ".service") && core.ValidAgentShareID(id) {
			return engine, true
		}
	}
	return "", false
}

func managedCoreNeedsNetlink(service string) bool {
	engine, ok := managedCoreServiceEngine(service)
	return ok && (engine == core.EngineMihomo || engine == core.EngineSingBox)
}

func validateManagedUnitDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("managed systemd directory %s is symlinked or writable by group/others", path)
	}
	return validateOwner(info, "managed systemd directory")
}

func validateManagedUnitFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("managed systemd drop-in %s is not a protected regular file", path)
	}
	return validateOwner(info, "managed systemd drop-in")
}
