package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Only staging may defer a bind held by the exact core being replaced. Apply
// probes again after the core restart, when the public port must be free.
func (m *tlsFrontendManager) checkNativeTLSHandoff(ctx context.Context, service, candidate string, frontends []core.TLSFrontend, port int) error {
	engine, spec, ok := managedSpecForService(service, m.services.Kind())
	if !ok || engine != core.EngineSingBox {
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
	if !nativeTLSListener(previous, port) {
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
