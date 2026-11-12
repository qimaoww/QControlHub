//go:build linux

package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub"
	"golang.org/x/sys/unix"
)

var agentOpenRCEnvironmentExport = regexp.MustCompile(`^export QCH_[A-Z0-9_]+='(?:[^']|'\\'')*'$`)

// Recognize the exact policy shipped immediately before diagnostics added RAW.
// Never overwrite administrator changes or extend an administrator's drop-ins.
func agentNetworkPolicyUpgrade(current, bundled []byte, kind string) ([]byte, bool) {
	legacy := bytes.ReplaceAll(bundled, []byte(" CAP_NET_RAW"), nil)
	if kind == ServiceManagerOpenRC {
		legacy = bytes.ReplaceAll(bundled, []byte("if qagent_capability_is_bound 13; then\n\tcapabilities=\"${capabilities},^cap_net_raw\"\nfi\n\n"), nil)
	}
	return bundled, !bytes.Equal(legacy, bundled) && bytes.Equal(current, legacy)
}

// A service name is not proof that this Agent belongs to it. Bind the repair
// to the manager's actual child PID before scheduling it, and again in helper.
func agentNetworkPolicyTarget(ctx context.Context, manager *ServiceManager, pid int) (string, []byte, error) {
	kind := manager.Kind()
	asset, path := "deploy/systemd/qagent.service", "/etc/systemd/system/qagent.service"
	if kind == ServiceManagerOpenRC {
		identity, err := boundOpenRCServiceProcess(ctx, "qagent")
		if err != nil || identity.Child.PID != pid {
			return "", nil, nil
		}
		path, asset = filepath.Join(openRCInitRoot, "qagent"), "deploy/openrc/qagent"
		// The installer owns QCH_* environment exports, but conf.d is sourced
		// after the script and can also override capabilities or identity.
		conf := filepath.Join(filepath.Dir(openRCInitRoot), "conf.d", "qagent")
		paths, err := filepath.Glob(conf + ".*")
		if err != nil {
			return "", nil, err
		}
		for _, override := range append(paths, conf) {
			value, err := os.ReadFile(override)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return "", nil, err
			}
			for _, line := range strings.Split(string(value), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				// Never interpret shell syntax while checking ownership.
				if !agentOpenRCEnvironmentExport.MatchString(line) {
					return "", nil, errors.New("Agent OpenRC override requires administrator review before CAP_NET_RAW recovery")
				}
			}
		}
	} else {
		output, err := run(ctx, manager.executable, "show", "qagent.service", "--property=MainPID", "--property=FragmentPath", "--property=DropInPaths")
		if err != nil {
			return "", nil, nil
		} // Direct/container execution has no unit.
		properties := map[string]string{}
		for _, line := range strings.Split(output, "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok {
				properties[k] = v
			}
		}
		if properties["MainPID"] != strconv.Itoa(pid) || properties["FragmentPath"] != path {
			return "", nil, nil
		}
		if properties["DropInPaths"] != "" {
			return "", nil, errors.New("Agent service has administrator drop-ins; review CAP_NET_RAW and restart it manually")
		}
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
		return "", nil, err
	}
	if err := validateManagedUnitFile(path); err != nil {
		return "", nil, err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	bundled, err := fs.ReadFile(qcontrolhub.AgentServiceAssets(), asset)
	if err != nil {
		return "", nil, err
	}
	content, upgrade := agentNetworkPolicyUpgrade(current, bundled, kind)
	if !upgrade {
		return "", nil, nil
	}
	return path, content, nil
}

// Called once before admitting tasks. The helper belongs to a different
// service/process session, so restarting qagent cannot kill its own restart.
// No permission failure triggers retries or a privilege escalation fallback.
func StartAgentNetworkPolicyRecovery(ctx context.Context, manager *ServiceManager) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	path, _, err := agentNetworkPolicyTarget(ctx, manager, os.Getpid())
	if err != nil || path == "" {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = ipQualityExecutable(executable)
	if err != nil {
		return err
	}
	args := []string{"recover-agent-network-policy", manager.Kind(), strconv.Itoa(os.Getpid())}
	if manager.Kind() == ServiceManagerSystemd {
		suffix, err := randomSuffix(8)
		if err != nil {
			return err
		}
		_, err = run(ctx, "/usr/bin/systemd-run", append([]string{"--collect", "--quiet", "--service-type=exec", "--unit=qagent-network-policy-" + suffix,
			"--property=User=root", "--property=NoNewPrivileges=yes", "--property=CapabilityBoundingSet=", "--property=AmbientCapabilities=",
			"--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=PrivateTmp=yes", "--property=PrivateDevices=yes",
			"--property=RestrictAddressFamilies=AF_UNIX", "--property=ReadWritePaths=/etc/systemd/system", "--property=RuntimeMaxSec=30s", "--", executable}, args...)...)
		return err
	}
	command := exec.Command(executable, args...)
	command.Env = os.Environ()
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

// RunAgentNetworkPolicyRecovery only accepts a manager and the verified parent
// PID, never a path or policy from an API request. Exact policy matching also
// makes a completed migration idempotent, even when host policy still denies RAW.
func RunAgentNetworkPolicyRecovery(ctx context.Context, kind string, pid int) error {
	manager, err := NewServiceManager(kind)
	if err != nil || pid <= 1 {
		return errors.New("invalid Agent policy recovery target")
	}
	path, content, err := agentNetworkPolicyTarget(ctx, manager, pid)
	if err != nil || path == "" {
		return err
	}
	// Do not replace a working restricted service with an impossible ambient
	// capability request. PID 1 is the manager/ancestor's upper bounding set,
	// whereas this helper deliberately runs with no effective capabilities.
	status, err := os.ReadFile("/proc/1/status")
	if err != nil {
		return fmt.Errorf("cannot verify host CAP_NET_RAW boundary: %w", err)
	}
	if !agentRawCapabilityBound(string(status)) {
		return errors.New("host/container PID 1 does not bound CAP_NET_RAW; service unchanged; administrator action required")
	}
	if _, err := atomicDeployWithDefaultMetadata(path, string(content), fileMetadata{mode: 0644}); err != nil {
		return err
	}
	if kind == ServiceManagerSystemd {
		if _, err := run(ctx, manager.executable, "daemon-reload"); err != nil {
			return err
		}
		if _, err := run(ctx, manager.executable, "--no-block", "restart", "qagent.service"); err != nil {
			return fmt.Errorf("Agent policy updated; service restart required: %w", err)
		}
		return nil
	}
	if _, err := run(ctx, manager.executable, "qagent", "restart"); err != nil {
		return fmt.Errorf("Agent policy updated; service restart required: %w", err)
	}
	return nil
}

func agentRawCapabilityBound(status string) bool {
	for _, line := range strings.Split(status, "\n") {
		if value, ok := strings.CutPrefix(line, "CapBnd:"); ok {
			mask, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
			return err == nil && mask&(1<<unix.CAP_NET_RAW) != 0
		}
	}
	return false
}
