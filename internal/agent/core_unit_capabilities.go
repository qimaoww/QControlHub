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

type coreUnitCapabilitySyncer struct {
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

func ensureManagedCoreServiceCapabilities(ctx context.Context, engine core.Engine, spec EngineSpec, managers ...*ServiceManager) error {
	manager := defaultSystemdServiceManager()
	if len(managers) > 0 && managers[0] != nil {
		manager = managers[0]
	}
	defaultSpec, exists := DefaultSpecsForServiceManager(manager.Kind())[engine]
	if !exists || spec != defaultSpec {
		return nil
	}
	// Alpine's managed OpenRC scripts grant CAP_NET_BIND_SERVICE when they
	// launch the non-root core. No unit drop-in is needed or available there.
	if manager.Kind() == ServiceManagerOpenRC {
		return nil
	}
	syncer := defaultCoreUnitCapabilitySyncer()
	if err := syncer.ensure(ctx, spec.Service); err != nil {
		return fmt.Errorf("prepare managed systemd service %s for privileged ports: %w", spec.Service, err)
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
	if err := validatePrivilegedExecutable(syncer.systemctlPath); err != nil {
		return fmt.Errorf("unsafe managed-unit helper %s: %w", syncer.systemctlPath, err)
	}

	ensureContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	configured, err := syncer.configured(ensureContext, service)
	if err != nil || configured {
		return err
	}
	for _, executable := range []string{syncer.systemdRunPath, syncer.installPath, syncer.teePath, syncer.movePath, syncer.removePath} {
		if err := validatePrivilegedExecutable(executable); err != nil {
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

	suffix, err := randomSuffix(10)
	if err != nil {
		return err
	}
	temporaryPath := filepath.Join(dropInDirectory, ".qcontrolhub-bind-low-ports-"+suffix+".tmp")
	if err := syncer.runHelper(ensureContext, []byte(managedCoreCapabilityDropIn), syncer.teePath, temporaryPath); err != nil {
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
		return errors.New("systemd did not load CAP_NET_BIND_SERVICE for the managed core")
	}
	return nil
}

func (syncer coreUnitCapabilitySyncer) configured(ctx context.Context, service string) (bool, error) {
	output, err := run(ctx, syncer.systemctlPath, "show", service,
		"--property=AmbientCapabilities", "--property=CapabilityBoundingSet", "--no-pager")
	if err != nil {
		return false, fmt.Errorf("inspect managed systemd service capabilities: %w: %s", err, output)
	}
	properties := make(map[string]string, 2)
	for _, line := range strings.Split(output, "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			properties[name] = strings.ToLower(value)
		}
	}
	return capabilityListContains(properties["AmbientCapabilities"], "cap_net_bind_service") &&
		capabilityListContains(properties["CapabilityBoundingSet"], "cap_net_bind_service"), nil
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
	runnerArguments := []string{
		"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec",
		"--property=User=root", "--property=UMask=0022", "--property=NoNewPrivileges=yes",
		"--property=CapabilityBoundingSet=", "--property=AmbientCapabilities=",
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
	switch service {
	case "qagent-mihomo.service", "qagent-xray.service", "qagent-sing-box.service", "qagent-shadowsocks-rust.service":
		return true
	default:
		return false
	}
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
