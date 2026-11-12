//go:build linux

package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
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

// Recognize exact shipped policies, including installations that predate
// certificate management. A binary upgrade never updated those service files.
// Never overwrite administrator changes or extend an administrator's drop-ins.
func agentNetworkPolicyUpgrade(current, bundled []byte, kind string) ([]byte, bool) {
	legacy := bytes.ReplaceAll(bundled, []byte(" CAP_NET_RAW"), nil)
	if kind == ServiceManagerOpenRC {
		legacy = bytes.ReplaceAll(bundled, []byte("if qagent_capability_is_bound 13; then\n\tcapabilities=\"${capabilities},^cap_net_raw\"\nfi\n\n"), nil)
	}
	if !bytes.Equal(legacy, bundled) && bytes.Equal(current, legacy) {
		return bundled, true
	}
	if kind != ServiceManagerSystemd || !olderAgentSystemdPolicy(current) {
		return nil, false
	}
	// Only add RAW. Do not grant BIND_SERVICE/identity capabilities or replace
	// unrelated service isolation when recovering an older managed policy.
	lines := strings.Split(string(current), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "CapabilityBoundingSet=") || strings.HasPrefix(line, "AmbientCapabilities=") {
			lines[i] += " CAP_NET_RAW"
		}
	}
	return []byte(strings.Join(lines, "\n")), true
}

// SHA-256 allowlist of every older systemd template on published main. Match
// the entire file so even a comment or unrelated administrator setting keeps
// the service outside automatic recovery.
func olderAgentSystemdPolicy(current []byte) bool {
	switch fmt.Sprintf("%x", sha256.Sum256(current)) {
	case "925d98f26d188a6c4b15868184ccc0c96e6048463e74b2e780d4b584339f302f": // b84342e8
		return true
	case "61f577c4e2f8e99ef9a5357bc3c55286bea4a6c25e7c53a5ed44a2bd36e339c8": // 40c19e80
		return true
	case "cd9b0e670000fd5a43f8697ff7ba7e5b48647a19745be8a33afc0a5fb822b33b": // 02f8429e
		return true
	case "52ef15c4df336f941945d7fe30ed82846ece0c043d9952c22dce4ac00537b3e4": // 6179366e
		return true
	case "e84dc5376f9fc8747c290ce663db2f0af012b73065c7c45c2ca1018aedcfb7e3": // b5c76a9d
		return true
	case "a4516271fc24d1a748650e5c23c57d28ace76da36db41aaa5b70db375848d7a9": // d5bc3926
		return true
	case "caa6e73d0a7d31f17adb7d16e2c1bd054f7ea3bdf2addb09672866993181bb53": // 4768b3cd
		return true
	case "7d1401b566d00041eacbf49e508836b6eb6feb22c55e93ad34e32159cf4a9f5e": // 0e537993
		return true
	case "fa3912c9518dfbaf27e2a118b83eb166b979ab17005b1e652fbe670cc1d44017": // b7409554
		return true
	case "9b98113daffc03a2627d5cbf645a48ad113740c076f3e53a5fc2593ab40a4ba7": // 107bde75
		return true
	}
	return false
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
