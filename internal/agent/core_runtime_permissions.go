package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

const managedCoreNetworkCapabilityMask = uint64(1<<10 | 1<<12)

func managedSpecForService(service, kind string) (core.Engine, EngineSpec, bool) {
	for engine, base := range DefaultSpecsForServiceManager(kind) {
		if service == base.Service {
			return engine, base, true
		}
		prefix, suffix := "qagent-"+managedCoreAssetName(engine)+"@", ".service"
		if kind == ServiceManagerOpenRC {
			prefix, suffix = "qagent-"+managedCoreAssetName(engine)+"-", ""
		}
		id, found := strings.CutPrefix(strings.TrimSuffix(service, suffix), prefix)
		if found && (suffix == "" || strings.HasSuffix(service, suffix)) && core.ValidAgentShareID(id) {
			spec, err := sharedInstanceSpec(engine, base, id, kind)
			return engine, spec, err == nil
		}
	}
	return "", EngineSpec{}, false
}

func managedServiceProcessPID(ctx context.Context, manager *ServiceManager, service string) (int, error) {
	if manager.Kind() == ServiceManagerOpenRC {
		identity, err := boundOpenRCServiceProcess(ctx, service)
		if os.IsPermission(err) {
			return 0, fmt.Errorf("service %s: inspect service-bound cross-UID /proc identity: %w; verify the Agent host/container grants its existing process-inspection authority (CAP_SYS_PTRACE); do not grant ptrace to the core", service, err)
		}
		return identity.Child.PID, err
	}
	value, err := run(ctx, manager.executable, "show", service, "--property=MainPID", "--value")
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("service %s has no live MainPID", service)
	}
	return pid, nil
}

func readManagedProcessStatus(ctx context.Context, manager *ServiceManager, pid int) (string, error) {
	path := filepath.Join(openRCProcRoot, strconv.Itoa(pid), "status")
	value, err := os.ReadFile(path)
	if err == nil || manager.Kind() != ServiceManagerSystemd {
		return string(value), err
	}
	// The Agent's ProtectProc=invisible intentionally hides other UIDs. Read
	// only this service-bound PID's public status in a transient unit, without
	// granting ptrace or changing the Agent's process/filesystem sandbox.
	return run(ctx, "/usr/bin/systemd-run", "--pipe", "--wait", "--collect", "--quiet", "--service-type=exec",
		"--property=User=root", "--property=NoNewPrivileges=yes", "--property=CapabilityBoundingSet=", "--property=AmbientCapabilities=",
		"--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=PrivateDevices=yes", "--property=RestrictAddressFamilies=AF_UNIX",
		"--", "/usr/bin/cat", path)
}

func validateManagedProcessStatus(value string, identity commandIdentity) error {
	fields := map[string]string{}
	for _, line := range strings.Split(value, "\n") {
		if key, text, ok := strings.Cut(line, ":"); ok {
			fields[key] = strings.TrimSpace(text)
		}
	}
	for key, want := range map[string]uint32{"Uid": identity.uid, "Gid": identity.gid} {
		ids := strings.Fields(fields[key])
		if len(ids) != 4 {
			return fmt.Errorf("process %s status is incomplete", key)
		}
		for _, id := range ids {
			got, err := strconv.ParseUint(id, 10, 32)
			if err != nil || got != uint64(want) || got == 0 {
				return fmt.Errorf("process %s=%s; required non-root identity %d", key, fields[key], want)
			}
		}
	}
	for _, key := range []string{"CapInh", "CapEff", "CapPrm", "CapAmb", "CapBnd"} {
		got, err := strconv.ParseUint(fields[key], 16, 64)
		if err != nil || got != managedCoreNetworkCapabilityMask {
			return fmt.Errorf("process %s=%s; require only CAP_NET_BIND_SERVICE (low-port listen) and CAP_NET_ADMIN (SO_MARK independent egress); inspect service overrides and host capability bounding set", key, fields[key])
		}
	}
	if fields["NoNewPrivs"] != "1" {
		return errors.New("process NoNewPrivs is not 1; inspect service no_new_privs/NoNewPrivileges and OpenRC conf.d overrides")
	}
	for _, id := range strings.Fields(fields["Groups"]) {
		got, err := strconv.ParseUint(id, 10, 32)
		allowed := false
		for _, gid := range identity.groups {
			allowed = allowed || got == uint64(gid)
		}
		if err != nil || got == 0 || !allowed {
			return fmt.Errorf("process has unexpected supplementary group %q; review managed account group membership", id)
		}
	}
	return nil
}

func verifyManagedServiceProcessPermissions(ctx context.Context, manager *ServiceManager, service string) error {
	engine, _, managed := runtimePermissionManagedService(ctx, manager, service)
	if !managed {
		return nil
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return err
	}
	pid, err := managedServiceProcessPID(ctx, manager, service)
	if err != nil {
		return err
	}
	status, err := readManagedProcessStatus(ctx, manager, pid)
	if err == nil {
		err = validateManagedProcessStatus(status, identity)
	}
	if err != nil {
		return fmt.Errorf("kernel %s service %s PID %d: verify actual runtime permissions: %w", engine, service, pid, err)
	}
	if current, err := managedServiceProcessPID(ctx, manager, service); err != nil || current != pid {
		return fmt.Errorf("kernel %s service %s process changed during permission verification; retry", engine, service)
	}
	return nil
}

// Reload updates systemd's next execution context, not an existing process.
// Compare the manager's start timestamp with every loaded fragment/drop-in.
// OpenRC compares its child start ticks with the exact script and binary.
func managedServicePolicyNeedsRestart(ctx context.Context, manager *ServiceManager, service string) (bool, error) {
	_, spec, managed := runtimePermissionManagedService(ctx, manager, service)
	if !managed {
		return false, nil
	}
	var started time.Time
	paths := []string{spec.Binary}
	if manager.Kind() == ServiceManagerSystemd {
		stamp, err := runInDirectoryWithEnvironment(ctx, "", "TZ=UTC", manager.executable, "show", service, "--property=ExecMainStartTimestamp", "--value", "--timestamp=us")
		if err != nil {
			return false, err
		}
		started, err = time.Parse("Mon 2006-01-02 15:04:05 MST", stamp)
		if err != nil {
			return false, fmt.Errorf("service %s start timestamp cannot be inspected: %w", service, err)
		}
		for _, property := range []string{"FragmentPath", "DropInPaths"} {
			value, err := run(ctx, manager.executable, "show", service, "--property="+property, "--value")
			if err != nil {
				return false, err
			}
			paths = append(paths, strings.Fields(value)...)
		}
	} else {
		process, err := boundOpenRCServiceProcess(ctx, service)
		if err != nil {
			return false, err
		}
		ticks, err := strconv.ParseFloat(process.Child.StartTime, 64)
		if err != nil {
			return false, err
		}
		uptime, err := os.ReadFile(filepath.Join(openRCProcRoot, "uptime"))
		if err != nil || len(strings.Fields(string(uptime))) == 0 {
			return false, fmt.Errorf("read boot uptime for %s: %w", service, err)
		}
		seconds, err := strconv.ParseFloat(strings.Fields(string(uptime))[0], 64)
		if err != nil {
			return false, err
		}
		// Linux procfs reports USER_HZ (100), independent of kernel CONFIG_HZ.
		started = time.Now().Add(time.Duration((ticks/100 - seconds) * float64(time.Second)))
		paths = append(paths, filepath.Join(openRCInitRoot, service))
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return false, err
		}
		if info.ModTime().After(started) {
			return true, nil
		}
	}
	return false, nil
}

// A service name alone does not establish ownership. Custom executors can use
// qagent-looking names with their own paths; their process policy remains the
// administrator's responsibility, just like capability/config synchronization.
func runtimePermissionManagedService(ctx context.Context, manager *ServiceManager, service string) (core.Engine, EngineSpec, bool) {
	engine, spec, known := managedSpecForService(service, manager.Kind())
	if !known {
		return engine, spec, false
	}
	if manager.Kind() == ServiceManagerOpenRC {
		path := filepath.Join(openRCInitRoot, service)
		if err := validateManagedUnitFile(path); err != nil || validateOpenRCCoreOverrides(service) != nil {
			return engine, spec, false
		}
		got, err := os.ReadFile(path)
		if err != nil {
			return engine, spec, false
		}
		var want []byte
		if isSharedInstanceSpec(engine, spec, manager.Kind()) {
			contents, err := openRCSharedInstanceContent(engine, spec)
			if err != nil {
				return engine, spec, false
			}
			want = []byte(contents)
		} else {
			want, err = fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/openrc/"+service)
			if err != nil {
				return engine, spec, false
			}
		}
		return engine, spec, matchesAnyManagedDropIn(got, managedOpenRCScriptVariants(want))
	}
	fragment := spec.Service
	if isSharedInstanceSpec(engine, spec, manager.Kind()) {
		fragment = "qagent-" + managedCoreAssetName(engine) + "@.service"
	}
	value, err := run(ctx, manager.executable, "show", service, "--property=FragmentPath", "--value")
	path := filepath.Join(existingDiscoveryManagedUnitRoot, fragment)
	if err != nil || strings.TrimSpace(value) != path || validateManagedUnitFile(path) != nil {
		return engine, spec, false
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return engine, spec, false
	}
	if isSharedInstanceSpec(engine, spec, manager.Kind()) {
		_, err = sharedInstanceTemplateNeedsUpgrade(engine, path)
	} else {
		err = validateManagedUnitFragment(contents, engine, spec)
	}
	return engine, spec, err == nil && validateManagedUnitDropIns(ctx, service) == nil
}
