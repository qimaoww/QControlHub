package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Only staging may defer a bind held by the exact core being replaced. Apply
// probes again after the core restart, when the public port must be free.
func (m *tlsFrontendManager) checkNativeTLSHandoff(ctx context.Context, service, candidate string, frontends []core.TLSFrontend, port int) error {
	engine, spec, ok := managedSpecForService(service, m.services.Kind())
	if !ok || engine != core.EngineSingBox && engine != core.EngineXray && engine != core.EngineMihomo {
		return errors.New("unrecognized native TLS service")
	}
	public := false
	for _, f := range frontends {
		public = public || !f.LocalOnly && f.PublicPort == port
	}
	if !public {
		return errors.New("port is not a public TLS frontend")
	}
	if err := serverconfig.ValidateIndependentEgress(engine, candidate); err != nil {
		return err
	}
	for _, endpoint := range serverconfig.DiscoverTrafficPorts(engine, candidate) {
		if endpoint.Port == port {
			return errors.New("candidate core still requires the public port")
		}
	}
	previous, err := readConfigurationFile(spec.ConfigPath)
	if err != nil {
		return err
	}
	if !nativeTLSListenerForEngine(engine, previous, port) {
		return errors.New("previous configuration has no native TLS listener on this port")
	}
	pid, err := managedServiceProcessPID(ctx, m.services, service)
	if err != nil {
		return err
	}
	before, err := readOpenRCProcessIdentity(pid)
	if err != nil {
		return err
	}
	configPath, configDirectory, workDirectory, matched := parseExistingArgv(engine, spec.Binary, before.Argv)
	if engine == core.EngineMihomo {
		argv := before.Argv
		matched = len(argv) == 5 && argv[0] == spec.Binary && argv[1] == "-d" && argv[2] == coreCertificateWorkingDirectory(engine, spec, m.services.Kind()) && argv[3] == "-f" && argv[4] == spec.ConfigPath
		configPath, configDirectory, workDirectory = spec.ConfigPath, "", ""
	}
	if before.Executable != spec.Binary || !matched || configPath != spec.ConfigPath || configDirectory != spec.ConfigDirectory || workDirectory != spec.WorkingDirectory {
		return errors.New("running core does not match the managed binary and configuration")
	}
	if err := processOwnsTCPPort(pid, port); err != nil {
		return err
	}
	after, err := readOpenRCProcessIdentity(pid)
	if err != nil || before.StartTime != after.StartTime || before.Executable != after.Executable || strings.Join(before.Argv, "\x00") != strings.Join(after.Argv, "\x00") {
		return errors.New("running core changed during TLS handoff inspection")
	}
	current, err := managedServiceProcessPID(ctx, m.services, service)
	if err != nil || current != pid {
		return errors.New("service process changed during TLS handoff inspection")
	}
	return nil
}

func nativeTLSListenerForEngine(engine core.Engine, content string, port int) bool {
	if engine != core.EngineSingBox {
		for _, input := range serverconfig.ParseAll(engine, content) {
			if input.Port == port && input.TLSEnabled && !input.RealityEnabled {
				return true
			}
		}
		return false
	}
	return nativeTLSListener(content, port)
}

func nativeTLSListener(content string, port int) bool {
	var root struct {
		Inbounds []struct {
			ListenPort int `json:"listen_port"`
			TLS        struct {
				Enabled bool `json:"enabled"`
			} `json:"tls"`
		} `json:"inbounds"`
	}
	if json.Unmarshal([]byte(content), &root) != nil {
		return false
	}
	for _, inbound := range root.Inbounds {
		if inbound.ListenPort == port && inbound.TLS.Enabled {
			return true
		}
	}
	return false
}

// A wildcard proxy bind overlaps every local TCP listener on this port. Every
// socket must belong to the service process, including IPv4 and IPv6 sockets;
// a saved config, matching PID alone, or another process of the same UID is
// insufficient evidence. This runs inside the existing privileged TLS helper.
func processOwnsTCPPort(pid, port int) error {
	processRoot := filepath.Join(openRCProcRoot, strconv.Itoa(pid))
	fds, err := os.ReadDir(filepath.Join(processRoot, "fd"))
	if err != nil {
		return err
	}
	owned := map[string]bool{}
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join(processRoot, "fd", fd.Name()))
		if err == nil && strings.HasPrefix(target, "socket:[") {
			owned[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = true
		}
	}
	found := false
	for _, family := range []string{"tcp", "tcp6"} {
		file, err := os.Open(filepath.Join(openRCProcRoot, "net", family))
		if errors.Is(err, os.ErrNotExist) && family == "tcp6" {
			continue
		}
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			_, text, ok := strings.Cut(fields[1], ":")
			value, err := strconv.ParseUint(text, 16, 16)
			if !ok || err != nil || int(value) != port {
				continue
			}
			if !owned[fields[9]] {
				file.Close()
				return fmt.Errorf("TCP port %d has a listener outside the target service", port)
			}
			found = true
		}
		err = scanner.Err()
		file.Close()
		if err != nil {
			return err
		}
	}
	if !found {
		return errors.New("target service has no matching TCP listener")
	}
	return nil
}

// A durable held deployment keeps rollback certificates/pages and prevents
// renewal or another instance's deployment from restoring a released route.
func (m *tlsFrontendManager) nativeHandoff(ctx context.Context, service, candidate, operation string, incoming ...[]core.TLSFrontend) error {
	tlsFrontendMutex.Lock()
	defer tlsFrontendMutex.Unlock()
	previous, err := m.load()
	if err != nil {
		return err
	}
	deployment, exists := previous[service]
	if !exists && (operation != "release" || len(incoming) == 0 || len(incoming[0]) == 0) {
		if operation == "commit" {
			return m.cleanupRetiredTLS(ctx, previous)
		}
		return nil
	}
	next := tlsDeployments{}
	for key, value := range previous {
		next[key] = value
	}
	switch operation {
	case "release":
		engine, _, ok := managedSpecForService(service, m.services.Kind())
		if !ok {
			return nil
		}
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(candidate)))
		if deployment.NativeHandoff != nil && deployment.PendingConfig != "" {
			if deployment.PendingConfig != digest {
				return errors.New("上次 TLS 部署尚未完成，请先重试原配置")
			}
			return nil
		}
		ports := map[int]bool{}
		for _, input := range serverconfig.ParseAll(engine, candidate) {
			if input.TLSEnabled && !input.RealityEnabled {
				ports[input.Port] = true
			}
		}
		for other, value := range previous {
			if other == service || value.Stopped {
				continue
			}
			for _, f := range value.Frontends {
				if ports[f.PublicPort] {
					return fmt.Errorf("端口 %d 仍由其他实例使用", f.PublicPort)
				}
			}
		}
		if held := deployment.NativeHandoff; held != nil {
			// A retry after an interrupted Agent may continue the same handoff.
			// Keep its original rollback resources until the retry commits.
			for _, f := range held.Frontends {
				retained := false
				for _, current := range deployment.Frontends {
					retained = retained || current.Tag == f.Tag && current.PublicPort == f.PublicPort
				}
				if !retained && !ports[f.PublicPort] {
					return errors.New("上次 TLS 模式切换尚未完成，请先重试原生 TLS 部署")
				}
			}
		}
		var retained []core.TLSFrontend
		for _, f := range deployment.Frontends {
			if f.LocalOnly || !ports[f.PublicPort] {
				retained = append(retained, f)
				continue
			}
			for other, value := range previous {
				if other == service || value.Stopped {
					continue
				}
				for _, foreign := range value.Frontends {
					if foreign.PublicPort == f.PublicPort {
						return fmt.Errorf("端口 %d 仍由其他实例使用", f.PublicPort)
					}
				}
			}
			listener, listenErr := net.Listen("tcp", ":"+strconv.Itoa(f.PublicPort))
			if listenErr == nil {
				listener.Close()
			} else {
				proxy := "qagent-tls-" + f.Mode
				if m.services.Kind() == ServiceManagerSystemd {
					proxy += ".service"
				}
				pid, pidErr := managedServiceProcessPID(ctx, m.services, proxy)
				if pidErr != nil {
					return pidErr
				}
				if err = processOwnsTCPPort(pid, f.PublicPort); err != nil {
					return err
				}
			}
		}
		held := deployment
		if deployment.NativeHandoff != nil {
			held = *deployment.NativeHandoff
		}
		deployment.Frontends, deployment.NativeHandoff, deployment.PendingConfig = retained, &held, digest
		next[service] = deployment
	case "restore", "commit":
		if deployment.NativeHandoff == nil {
			if operation == "commit" {
				return m.cleanupRetiredTLS(ctx, previous)
			}
			return nil
		}
		if operation == "restore" {
			next[service] = *deployment.NativeHandoff
		} else {
			deployment.NativeHandoff, deployment.PendingConfig = nil, ""
			if len(deployment.Frontends) == 0 {
				delete(next, service)
			} else {
				next[service] = deployment
			}
		}
	default:
		return errors.New("unknown native TLS handoff operation")
	}
	if _, err = activeTLSFrontends(next); err != nil {
		return err
	}
	if err = m.sync(ctx, next); err == nil {
		retainRetiredTLS(previous, next)
		data, encodeErr := json.Marshal(next)
		if encodeErr != nil {
			err = encodeErr
		} else {
			err = m.writeConfig(filepath.Join(m.root, "deployments.json"), string(data), true)
		}
	}
	if err != nil {
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return errors.Join(err, m.sync(recovery, previous))
	}
	if operation == "release" {
		return nil
	}
	return m.cleanupRetiredTLS(ctx, next)
}
