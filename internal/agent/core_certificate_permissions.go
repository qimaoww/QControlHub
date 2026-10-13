package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
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

func coreCertificatePaths(engine core.Engine, content string) ([]string, error) {
	var document any
	var err error
	if engine == core.EngineMihomo {
		err = yaml.Unmarshal([]byte(content), &document)
	} else {
		err = json.Unmarshal([]byte(content), &document)
	}
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	switch engine {
	case core.EngineMihomo:
		keys["certificate"], keys["private-key"] = true, true
	case core.EngineXray:
		keys["certificateFile"], keys["keyFile"] = true, true
	case core.EngineSingBox:
		keys["certificate_path"], keys["key_path"], keys["client_certificate_path"] = true, true, true
	}
	var paths []string
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if keys[key] {
					values := []any{child}
					if list, ok := child.([]any); ok {
						values = list
					}
					for _, item := range values {
						if path, ok := item.(string); ok && path != "" && !strings.Contains(path, "\n") && !strings.Contains(path, "-----BEGIN ") {
							paths = append(paths, path)
						}
					}
				} else {
					visit(child)
				}
			}
		case []any:
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(document)
	return paths, nil
}

func (e *Executor) verifyCoreCertificateAccess(ctx context.Context, engine core.Engine, spec EngineSpec, content string, identity commandIdentity) error {
	paths, err := coreCertificatePaths(engine, content)
	if err != nil {
		return err
	}
	state := "/var/lib/qcontrolhub-" + managedCoreAssetName(engine)
	if isSharedInstanceSpec(engine, spec, e.serviceManager().Kind()) {
		state += "-" + filepath.Base(filepath.Dir(spec.ConfigPath))
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(state, path)
		}
		if pathWithin(path, filepath.Dir(spec.ConfigPath)) {
			// The instance directory is protected and project-managed; restore
			// only referenced cert/key metadata, without changing file contents.
			if _, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, path); err != nil {
				return fmt.Errorf("kernel %s service %s: prepare certificate/key read %s: %w", engine, spec.Service, path, err)
			}
		}
		if engine == core.EngineMihomo && !pathWithin(path, state) && !pathWithin(path, filepath.Dir(spec.ConfigPath)) && !pathWithin(path, "/etc/qcontrolhub/tls") {
			return fmt.Errorf("kernel %s service %s: read certificate/key %s: outside the managed SAFE_PATHS allowlist; install it in the protected instance configuration directory or /etc/qcontrolhub/tls and update the configuration", engine, spec.Service, path)
		}
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := e.runManagedIdentityCommand(probe, filepath.Dir(spec.ConfigPath), "", "/usr/bin/test", &identity, "-r", path)
		cancel()
		if err != nil {
			return fmt.Errorf("kernel %s service %s: read certificate/key %s as qcontrolhub-core: %w; check existence, parent traversal and file read access (namei -l), use root:qcontrolhub-core 0750 directories and 0640 files, and keep certificates outside ProtectHome/PrivateTmp paths", engine, spec.Service, path, err)
		}
	}
	return nil
}
