package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nftBackend struct {
	nftPath        string
	systemdRunPath string
	direct         bool
	initialization error
	missing        bool
	installMu      sync.Mutex
	installTried   bool
	installer      func(context.Context, *nftBackend) error
}

var (
	nftExecutablePath = "/usr/sbin/nft"
	nftAPTGetPath     = "/usr/bin/apt-get"
	nftAPKPaths       = []string{"/usr/sbin/apk", "/sbin/apk"}
)

func newNFTBackend() *nftBackend {
	return newNFTBackendForServiceManager(defaultSystemdServiceManager())
}

func newNFTBackendForServiceManager(serviceManager *ServiceManager) *nftBackend {
	serviceManager = selectedServiceManager(serviceManager)
	backend := &nftBackend{nftPath: nftExecutablePath, direct: processHasCapability(12), installer: installNFTablesPackage}
	if serviceManager.Kind() == ServiceManagerSystemd {
		backend.systemdRunPath = "/usr/bin/systemd-run"
	}
	if err := validatePrivilegedExecutable(backend.nftPath); err != nil {
		backend.initialization = fmt.Errorf("nftables is unavailable: %w", err)
		_, statErr := os.Lstat(backend.nftPath)
		backend.missing = errors.Is(statErr, os.ErrNotExist)
	} else if !backend.direct && serviceManager.Kind() == ServiceManagerOpenRC {
		backend.initialization = errors.New("nftables CAP_NET_ADMIN is unavailable in the OpenRC Agent service; update the QAgent OpenRC service and restart it")
	} else if !backend.direct {
		if err := validatePrivilegedExecutable(backend.systemdRunPath); err != nil {
			backend.initialization = fmt.Errorf("nftables privilege runner is unavailable: %w", err)
		}
	}
	return backend
}

func (backend *nftBackend) Counters(ctx context.Context) (map[string]uint64, bool, error) {
	if err := backend.ensureAvailable(ctx); err != nil {
		return nil, false, err
	}
	output, err := backend.run(ctx, nil, "-j", "list", "table", "inet", trafficTableName)
	if err != nil {
		if strings.Contains(err.Error(), "No such file or directory") {
			return map[string]uint64{}, false, nil
		}
		return nil, false, fmt.Errorf("read nftables traffic counters: %w", err)
	}
	counters, err := parseNFTTrafficCounters(output)
	if err != nil {
		return nil, true, err
	}
	return counters, true, nil
}

func (backend *nftBackend) Replace(ctx context.Context, script string) error {
	if err := backend.ensureAvailable(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(script) == "" {
		return nil
	}
	if _, err := backend.run(ctx, []byte(script), "-f", "-"); err != nil {
		return fmt.Errorf("apply nftables traffic rules: %w", err)
	}
	return nil
}

func (backend *nftBackend) ensureAvailable(ctx context.Context) error {
	if backend == nil {
		return errors.New("nftables backend is unavailable")
	}
	backend.installMu.Lock()
	defer backend.installMu.Unlock()
	if backend.initialization == nil {
		return nil
	}
	if !backend.missing || backend.installTried {
		return backend.initialization
	}
	backend.installTried = true
	installer := backend.installer
	if installer == nil {
		installer = installNFTablesPackage
	}
	installContext, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := installer(installContext, backend); err != nil {
		backend.initialization = fmt.Errorf("nftables is unavailable and automatic installation failed: %w", err)
		return backend.initialization
	}
	if err := validatePrivilegedExecutable(backend.nftPath); err != nil {
		backend.initialization = fmt.Errorf("nftables package was installed but the executable is unavailable: %w", err)
		return backend.initialization
	}
	if !backend.direct && backend.systemdRunPath == "" {
		backend.initialization = errors.New("nftables CAP_NET_ADMIN is unavailable in the OpenRC Agent service; update the QAgent OpenRC service and restart it")
		return backend.initialization
	}
	if !backend.direct {
		if err := validatePrivilegedExecutable(backend.systemdRunPath); err != nil {
			backend.initialization = fmt.Errorf("nftables privilege runner is unavailable: %w", err)
			return backend.initialization
		}
	}
	backend.missing = false
	backend.initialization = nil
	return nil
}

func installNFTablesPackage(ctx context.Context, backend *nftBackend) error {
	if backend == nil {
		return errors.New("nftables backend is unavailable")
	}
	if err := validatePrivilegedExecutable(nftAPTGetPath); err == nil {
		if err := runNFTPackageCommand(ctx, backend, []string{"DEBIAN_FRONTEND=noninteractive"}, nftAPTGetPath, "update", "-qq"); err != nil {
			return fmt.Errorf("update APT metadata: %w", err)
		}
		if err := runNFTPackageCommand(ctx, backend, []string{"DEBIAN_FRONTEND=noninteractive"}, nftAPTGetPath,
			"install", "-y", "--no-install-recommends", "nftables"); err != nil {
			return fmt.Errorf("install nftables with APT: %w", err)
		}
		return nil
	}
	for _, apkPath := range nftAPKPaths {
		if err := validatePrivilegedExecutable(apkPath); err != nil {
			continue
		}
		if err := runNFTPackageCommand(ctx, backend, nil, apkPath, "add", "--no-cache", "nftables"); err != nil {
			return fmt.Errorf("install nftables with apk: %w", err)
		}
		return nil
	}
	return errors.New("no supported Debian apt-get or Alpine apk package manager was found")
}

func runNFTPackageCommand(ctx context.Context, backend *nftBackend, environment []string, executable string, arguments ...string) error {
	commandName := executable
	commandArguments := append([]string(nil), arguments...)
	if backend != nil && backend.systemdRunPath != "" {
		if _, err := os.Lstat("/run/systemd/system"); err == nil {
			if err := validatePrivilegedExecutable(backend.systemdRunPath); err != nil {
				return fmt.Errorf("validate systemd package runner: %w", err)
			}
			commandName = backend.systemdRunPath
			commandArguments = []string{
				"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec",
				"--property=User=root", "--property=NoNewPrivileges=no",
				"--property=RuntimeMaxSec=40s", "--property=TimeoutStopSec=5s",
			}
			for _, value := range environment {
				commandArguments = append(commandArguments, "--setenv="+value)
			}
			commandArguments = append(commandArguments, "--", executable)
			commandArguments = append(commandArguments, arguments...)
			environment = nil
		}
	}
	command := exec.CommandContext(ctx, commandName, commandArguments...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	for _, value := range environment {
		command.Env = append(command.Env, value)
	}
	configureCommand(command)
	output := &boundedOutput{limit: 64 << 10}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func (backend *nftBackend) run(ctx context.Context, input []byte, nftArguments ...string) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	commandName := backend.nftPath
	arguments := nftArguments
	if !backend.direct {
		commandName = backend.systemdRunPath
		arguments = []string{
			"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec", "--setenv=LC_ALL=C",
			"--property=User=root", "--property=NoNewPrivileges=yes",
			"--property=CapabilityBoundingSet=CAP_NET_ADMIN", "--property=AmbientCapabilities=CAP_NET_ADMIN",
			"--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=PrivateTmp=yes",
			"--property=PrivateDevices=yes", "--property=RestrictAddressFamilies=AF_NETLINK",
			"--", backend.nftPath,
		}
		arguments = append(arguments, nftArguments...)
	}
	command := exec.CommandContext(commandContext, commandName, arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return stdout.Bytes(), nil
}

func processHasCapability(bit uint) bool {
	contents, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(contents), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), 16, 64)
		return err == nil && value&(uint64(1)<<bit) != 0
	}
	return false
}

func parseNFTTrafficCounters(contents []byte) (map[string]uint64, error) {
	var document struct {
		NFTables []struct {
			Counter *struct {
				Name   string  `json:"name"`
				Bytes  *uint64 `json:"bytes"`
				Handle uint64  `json:"handle"`
			} `json:"counter"`
			Rule *struct {
				Comment string `json:"comment"`
				Expr    []struct {
					Counter json.RawMessage `json:"counter"`
				} `json:"expr"`
			} `json:"rule"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		return nil, fmt.Errorf("parse nftables traffic counters: %w", err)
	}
	result := make(map[string]uint64)
	for _, item := range document.NFTables {
		if item.Counter != nil && validTrafficCounterName(item.Counter.Name) {
			if item.Counter.Bytes == nil || *item.Counter.Bytes > math.MaxInt64 {
				return nil, errors.New("invalid or missing nftables counter bytes")
			}
			if _, exists := result[item.Counter.Name]; exists {
				return nil, errors.New("duplicate nftables traffic counter")
			}
			result[item.Counter.Name] = *item.Counter.Bytes
			result["handle:"+item.Counter.Name] = item.Counter.Handle
		}
		if item.Rule != nil && strings.HasPrefix(item.Rule.Comment, "qch:block:") {
			name := strings.TrimPrefix(item.Rule.Comment, "qch:block:")
			if validTrafficCounterName(name) {
				result["drop:"+name]++
			}
		}
		if item.Rule == nil || !strings.HasPrefix(item.Rule.Comment, "qch:trf_") {
			continue
		}
		for _, expression := range item.Rule.Expr {
			if len(expression.Counter) == 0 {
				continue
			}
			var name string
			if json.Unmarshal(expression.Counter, &name) == nil {
				if validTrafficCounterName(name) {
					result["rule:"+name]++
				}
				continue
			}
			var counter struct {
				Bytes *uint64 `json:"bytes"`
			}
			if err := json.Unmarshal(expression.Counter, &counter); err != nil {
				return nil, fmt.Errorf("parse nftables counter expression: %w", err)
			}
			if counter.Bytes == nil || *counter.Bytes > math.MaxInt64 {
				return nil, errors.New("invalid or missing nftables anonymous counter bytes")
			}
			result[item.Rule.Comment] = saturatedTrafficAdd(result[item.Rule.Comment], *counter.Bytes)
		}
	}
	return result, nil
}
