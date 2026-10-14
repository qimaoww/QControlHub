package agent

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/qimaoww/qcontrolhub"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Restore only an exactly recognized project unit, after its effective command,
// environment and drop-ins have been checked. Keep a protected previous copy.
func syncManagedMihomoTemplate(ctx context.Context, spec EngineSpec) error {
	path := filepath.Join(existingDiscoveryManagedUnitRoot, spec.Service)
	want, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/systemd/"+spec.Service)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(path)
	if err != nil || bytes.Equal(got, want) {
		return err
	}
	if err := validateManagedUnitFragment(got, core.EngineMihomo, spec); err != nil {
		return fmt.Errorf("Mihomo certificate path policy: refusing customized unit %s: %w", path, err)
	}
	syncer := defaultCoreUnitCapabilitySyncer()
	for _, executable := range []string{syncer.systemdRunPath, syncer.installPath, syncer.teePath, syncer.movePath, syncer.removePath, syncer.systemctlPath} {
		if err := validatePrivilegedExecutable(executable); err != nil {
			return err
		}
	}
	suffix, err := randomSuffix(10)
	if err != nil {
		return err
	}
	if _, err := installManagedLogFile(ctx, syncer, path+".permissions.bak."+suffix, got); err != nil {
		return err
	}
	if _, err := installManagedLogFile(ctx, syncer, path, want); err != nil {
		return err
	}
	_, err = run(ctx, syncer.systemctlPath, "daemon-reload")
	return err
}

func (e *Executor) verifyCoreCertificateAccess(ctx context.Context, engine core.Engine, spec EngineSpec, content string, identity commandIdentity) error {
	_, references, err := coreCertificateReferences(engine, content)
	if err != nil {
		return err
	}
	state := coreCertificateWorkingDirectory(engine, spec, e.serviceManager().Kind())
	for _, reference := range references {
		path := reference.path
		if !filepath.IsAbs(path) {
			path = filepath.Join(state, path)
		}
		stage := fmt.Sprintf("kernel %s service %s: certificate file access at %s (%s)", engine, spec.Service, reference.field, path)
		if pathWithin(path, filepath.Dir(spec.ConfigPath)) {
			// The instance directory is protected and project-managed; restore
			// only referenced cert/key metadata, without changing file contents.
			if _, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, path); err != nil {
				return fmt.Errorf("%s: prepare certificate/key read: %w", stage, err)
			}
		}
		if engine == core.EngineMihomo && !pathWithin(path, state) && !pathWithin(path, filepath.Dir(spec.ConfigPath)) && !pathWithin(path, "/etc/qcontrolhub/tls") {
			return fmt.Errorf("%s: outside the managed SAFE_PATHS allowlist; install it in the protected instance configuration directory or /etc/qcontrolhub/tls and update the configuration", stage)
		}
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := e.runManagedIdentityCommand(probe, filepath.Dir(spec.ConfigPath), "", "/usr/bin/test", &identity, "-r", path)
		cancel()
		if err != nil {
			// Stat as the Agent only to refine a failed service-account probe.
			// The identity probe above remains authoritative, including its sandbox.
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
				return fmt.Errorf("%s: certificate/key file not found; install the referenced file or correct this configuration field", stage)
			}
			return fmt.Errorf("%s: certificate/key file unreadable as qcontrolhub-core: %w; check parent traversal and file read access (namei -l), use root:qcontrolhub-core 0750 directories and 0640 files, and keep certificates outside ProtectHome/PrivateTmp paths", stage, err)
		}
	}
	return nil
}

func coreCertificateWorkingDirectory(engine core.Engine, spec EngineSpec, manager string) string {
	state := "/var/lib/qcontrolhub-" + managedCoreAssetName(engine)
	if isSharedInstanceSpec(engine, spec, manager) {
		state += "-" + filepath.Base(filepath.Dir(spec.ConfigPath))
	}
	return state
}
