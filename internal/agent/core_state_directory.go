package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// The Agent cannot traverse a core-owned 0750 state directory or write to
// /var/lib outside its own StateDirectory. Delegate only the existing metadata
// repair, without changing the long-running Agent or core service policies.
func (e *Executor) prepareCoreUpdateStateDirectory(ctx context.Context, engine core.Engine, spec EngineSpec) error {
	manager := e.serviceManager()
	wantEngine, want, ok := managedSpecForService(spec.Service, manager.Kind())
	if !ok || wantEngine != engine || want != spec {
		return errors.New("state directory preparation requires an exact managed service mapping")
	}
	if manager.Kind() == ServiceManagerOpenRC {
		return prepareSharedStateDirectory(coreCertificateWorkingDirectory(engine, spec, manager.Kind()))
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if e.coreStateHelperPath != "" {
		executable = e.coreStateHelperPath
	}
	executable, err = ipQualityExecutable(executable)
	if err != nil {
		return err
	}
	runner, err := ipQualityExecutable("/usr/bin/systemd-run")
	if err != nil {
		return err
	}
	suffix, err := randomSuffix(10)
	if err != nil {
		return err
	}
	unit := "qagent-core-state-" + suffix + ".service"
	prepareCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	output, err := run(prepareCtx, runner, coreStateDirectoryHelperArguments(executable, unit, spec.Service)...)
	// systemd-run cancellation alone does not stop the transient service.
	cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_, _ = run(cleanup, manager.executable, "stop", unit)
	if err != nil {
		return fmt.Errorf("managed state directory helper: %w; %s", err, output)
	}
	return nil
}

func coreStateDirectoryHelperArguments(executable, unit, service string) []string {
	return []string{
		"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec", "--unit=" + unit,
		"--property=User=root", "--property=Group=root", "--property=UMask=0027",
		"--property=NoNewPrivileges=yes", "--property=RuntimeMaxSec=35s",
		"--property=CapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH",
		"--property=AmbientCapabilities=CAP_CHOWN CAP_DAC_READ_SEARCH",
		"--property=ProtectSystem=strict", "--property=ProtectHome=yes",
		"--property=PrivateTmp=yes", "--property=PrivateDevices=yes",
		"--property=RestrictAddressFamilies=AF_UNIX", "--property=RestrictSUIDSGID=yes",
		// The fixed helper must also create a missing instance directory. It
		// derives that one path from a verified service, never caller input.
		"--property=ReadWritePaths=/var/lib", "--", executable, "prepare-core-state", service,
	}
}

// PrepareCoreStateDirectory accepts only a bundled main/shared systemd service.
// Revalidate ownership before touching metadata; never start or stop a core.
func PrepareCoreStateDirectory(ctx context.Context, service string) error {
	engine, spec, ok := managedSpecForService(service, ServiceManagerSystemd)
	if !ok {
		return errors.New("unrecognized managed core service")
	}
	if isSharedInstanceSpec(engine, spec, ServiceManagerSystemd) {
		if err := validateManagedSharedService(ctx, engine, spec); err != nil {
			return err
		}
	} else {
		status, err := validateManagedServiceForExistingDiscovery(ctx, engine, spec)
		if err != nil {
			return err
		}
		if status == "not-found" {
			return errors.New("managed core service is not installed")
		}
	}
	state := coreCertificateWorkingDirectory(engine, spec, ServiceManagerSystemd)
	if filepath.Dir(state) != "/var/lib" {
		return errors.New("managed core state path is outside /var/lib")
	}
	return prepareSharedStateDirectory(state)
}
