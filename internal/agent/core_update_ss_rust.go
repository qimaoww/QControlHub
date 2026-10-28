package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// ssserver has no check command. Exercise the target parser and listeners in a
// private network, with protected temporary input and the actual service ACL.
// Only listener addresses/ports change; cipher, users, resources and options
// still come from the deployed revision. A missing isolation helper rejects
// the update before any installed file or running service changes.
func (e *Executor) validateSSRustUpdateCandidate(ctx context.Context, spec EngineSpec, content, binary string, identity commandIdentity, metadata fileMetadata) error {
	if err := validateNoPersistentCoreLogs(core.EngineShadowsocksRust, content); err != nil {
		return err
	}
	probeContent, endpoints, err := ssRustUpdateProbeConfiguration(content)
	if err != nil {
		return err
	}
	acl := spec.ACLPath
	if acl == "" {
		acl = shadowsocksRustACLPath
	}
	if _, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, acl); err != nil {
		return fmt.Errorf("read service ACL: %w", err)
	}
	shell, err := coreUpdateProbeHelper("sh")
	if err != nil {
		return err
	}
	ss, err := coreUpdateProbeHelper("ss")
	if err != nil {
		return err
	}
	sleep, err := coreUpdateProbeHelper("sleep")
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(spec.ConfigPath))
	if err != nil {
		return err
	}
	defer root.Close()
	suffix, err := randomSuffix(10)
	if err != nil {
		return err
	}
	name := ".qch-update-probe-" + suffix + ".json"
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	if _, err = file.WriteString(probeContent); err == nil {
		err = applyFileMetadata(file, metadata)
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	args := []string{"-c", ssRustUpdateProbeScript, "qch-update-probe", binary,
		filepath.Join(filepath.Dir(spec.ConfigPath), name), acl, ss, sleep, strings.Join(endpoints, " ")}
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	state := coreCertificateWorkingDirectory(core.EngineShadowsocksRust, spec, e.serviceManager().Kind())
	var output string
	if e.serviceManager().Kind() == ServiceManagerSystemd {
		output, err = e.runSystemdValidationCommand(probeCtx, state, "", shell, identity, []string{
			"ReadWritePaths=", "PrivateNetwork=yes", "RuntimeMaxSec=15s",
			"CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_ADMIN", "AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN",
		}, args...)
	} else {
		// OpenRC cannot delegate a transient network namespace. Use only a
		// service user's unprivileged user/mount/network/PID namespaces; never
		// grant new capabilities to the Agent or fall back to host listeners.
		unshare, helperErr := coreUpdateProbeHelper("unshare")
		if helperErr != nil {
			return helperErr
		}
		mount, helperErr := coreUpdateProbeHelper("mount")
		if helperErr != nil {
			return helperErr
		}
		chroot, helperErr := coreUpdateProbeHelper("chroot")
		if helperErr != nil {
			return helperErr
		}
		setpriv, helperErr := coreUpdateProbeHelper("setpriv")
		if helperErr != nil {
			return helperErr
		}
		sandboxName := ".qch-update-root-" + suffix
		if err := root.Mkdir(sandboxName, 0o755); err != nil {
			return err
		}
		defer root.RemoveAll(sandboxName)
		sandbox := filepath.Join(filepath.Dir(spec.ConfigPath), sandboxName)
		// Bind the complete tree into a distinct root, then enter it. Merely
		// mounting over / leaves the caller's old fs root/cwd reachable. Drop
		// namespace administration/chroot capabilities before running the core.
		isolated := []string{"--user", "--map-root-user", "--mount", "--net", "--pid", "--fork", "--kill-child", "--",
			shell, "-c", `mount_helper=$1; sandbox=$2; chroot_helper=$3; shift 3; "$mount_helper" --rbind -o ro=recursive / "$sandbox" && exec "$chroot_helper" "$sandbox" "$@"`,
			"qch-update-isolation", mount, sandbox, chroot, setpriv,
			"--bounding-set=-all,+net_admin,+net_bind_service", "--inh-caps=-all,+net_admin,+net_bind_service",
			"--ambient-caps=-all,+net_admin,+net_bind_service", "--no-new-privs", shell, "-c",
			`state=$1; shift; cd "$state" && exec "$@"`, "qch-update-readonly", state, shell}
		isolated = append(isolated, args...)
		output, err = runInDirectoryWithEnvironmentAndIdentity(probeCtx, state, "", unshare, &identity, isolated...)
	}
	if err != nil || !strings.Contains(output, ssRustUpdateProbeReady) {
		redact := coreDiagnosticRedactor(core.EngineShadowsocksRust, content)
		output = redact(output)
		if len(output) > 2048 {
			output = strings.ToValidUTF8(output[len(output)-2048:], "�")
		}
		return fmt.Errorf("target ss-rust did not pass the isolated service startup check (requires private-network support and ss): %v; %s", err, output)
	}
	return nil
}

const ssRustUpdateProbeReady = "QCH_UPDATE_LISTENERS_READY"

const ssRustUpdateProbeScript = `set -eu
binary=$1; config=$2; acl=$3; ss=$4; pause=$5; endpoints=$6
"$binary" -c "$config" --acl "$acl" &
child=$!
trap 'kill "$child" 2>/dev/null || :; wait "$child" 2>/dev/null || :' EXIT
trap 'exit 1' HUP INT TERM
attempt=0
stable=0
while [ "$attempt" -lt 40 ]; do
  kill -0 "$child" 2>/dev/null || { wait "$child" || :; exit 1; }
  ready=1
  for endpoint in $endpoints; do
    protocol=${endpoint%:*}; port=${endpoint#*:}
    case "$protocol" in tcp) flag=-lnt ;; udp) flag=-lnu ;; *) exit 1 ;; esac
    listeners=$("$ss" -H "$flag" "sport = :$port") || exit 1
    [ -n "$listeners" ] || ready=0
  done
  if [ "$ready" = 1 ]; then stable=$((stable + 1)); else stable=0; fi
  if [ "$stable" -ge 5 ]; then echo QCH_UPDATE_LISTENERS_READY; exit 0; fi
  attempt=$((attempt + 1))
  "$pause" 0.1
done
echo 'target listeners did not become ready' >&2
exit 1
`

func coreUpdateProbeHelper(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("core update isolation helper %s is unavailable: %w", name, err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if err := validatePrivilegedExecutable(canonical); err != nil {
		return "", fmt.Errorf("unsafe core update helper %s: %w", name, err)
	}
	// Retain the applet name for BusyBox, after checking the root-owned link
	// chain and its final executable. These paths never come from a task.
	if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if err := validateOwner(info, "core update helper"); err != nil {
		return "", err
	}
	return path, nil
}

func ssRustUpdateProbeConfiguration(content string) (string, []string, error) {
	var config map[string]any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		return "", nil, err
	}
	var endpoints []string
	index := 0
	defaultMode, _ := config["mode"].(string)
	if defaultMode == "" {
		defaultMode = "tcp_only"
	}
	prepare := func(server map[string]any) error {
		address, ok := server["server"].(string)
		if !ok || net.ParseIP(address) == nil {
			return errors.New("ss-rust update check requires a numeric managed listen address")
		}
		if port, ok := server["server_port"].(json.Number); !ok {
			return errors.New("ss-rust update check requires server_port")
		} else if value, err := strconv.Atoi(string(port)); err != nil || value < 1 || value > 65535 {
			return errors.New("ss-rust server_port is invalid")
		}
		index++
		if index > 4096 {
			return errors.New("too many ss-rust listeners for an isolated update check")
		}
		port := 24000 + index
		server["server"] = "0.0.0.0"
		if strings.Contains(address, ":") {
			server["server"] = "::"
		}
		server["server_port"] = port
		mode, _ := server["mode"].(string)
		if mode == "" {
			mode = defaultMode
		}
		switch mode {
		case "tcp_only":
			endpoints = append(endpoints, "tcp:"+strconv.Itoa(port))
		case "udp_only":
			endpoints = append(endpoints, "udp:"+strconv.Itoa(port))
		case "tcp_and_udp":
			endpoints = append(endpoints, "tcp:"+strconv.Itoa(port), "udp:"+strconv.Itoa(port))
		default:
			return errors.New("ss-rust listener mode is invalid")
		}
		return nil
	}
	if _, exists := config["server"]; exists {
		if err := prepare(config); err != nil {
			return "", nil, err
		}
	}
	if value, exists := config["servers"]; exists {
		servers, ok := value.([]any)
		if !ok {
			return "", nil, errors.New("ss-rust servers is not an array")
		}
		for _, value := range servers {
			server, ok := value.(map[string]any)
			if !ok {
				return "", nil, errors.New("ss-rust server is not an object")
			}
			if err := prepare(server); err != nil {
				return "", nil, err
			}
		}
	}
	if len(endpoints) == 0 {
		return "", nil, errors.New("ss-rust configuration has no managed listeners to validate")
	}
	if value, exists := config["manager_address"]; exists {
		if address, ok := value.(string); !ok || address == "" {
			return "", nil, errors.New("ss-rust manager_address cannot be isolated")
		}
		config["manager_address"] = "127.0.0.1:23999"
	}
	encoded, err := json.Marshal(config)
	return string(encoded), endpoints, err
}
