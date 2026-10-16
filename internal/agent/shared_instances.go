package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/qimaoww/qcontrolhub"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func sharedInstanceRoot(engine core.Engine) string {
	return filepath.Join(managedCoreConfigurationRoot, managedCoreAssetName(engine), "shares")
}

func sharedCoreTemplatesSupported(specs map[core.Engine]EngineSpec, manager *ServiceManager) bool {
	for engine, spec := range specs {
		if spec != DefaultSpecsForServiceManager(manager.Kind())[engine] {
			return false
		}
	}
	return true
}

func (e *Executor) ensureSharedInstanceService(ctx context.Context, engine core.Engine, spec EngineSpec) error {
	if e.serviceManager().Kind() == ServiceManagerOpenRC {
		return ensureOpenRCSharedInstanceService(engine, spec)
	}
	path := filepath.Join("/etc/systemd/system", "qagent-"+managedCoreAssetName(engine)+"@.service")
	if err := validateManagedUnitFile(path); err == nil {
		upgrade, err := sharedInstanceTemplateNeedsUpgrade(engine, path)
		if err != nil {
			return err
		}
		if !upgrade {
			return validateManagedSharedService(ctx, engine, spec)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	want, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/systemd/qagent-"+managedCoreAssetName(engine)+"@.service")
	if err != nil {
		return err
	}
	// Refresh only the recognized shared template. Bootstrapping the whole
	// engine here also rewrote the owner's main unit and enablement.
	syncer := defaultCoreUnitCapabilitySyncer()
	if _, err := installManagedLogFile(ctx, syncer, path, want); err != nil {
		return err
	}
	if output, err := run(ctx, syncer.systemctlPath, "daemon-reload"); err != nil {
		return fmt.Errorf("reload shared service template: %w: %s", err, output)
	}
	if err := validateManagedUnitFile(path); err != nil {
		return err
	}
	if err := verifySharedInstanceTemplate(engine, path); err != nil {
		return err
	}
	return validateManagedSharedService(ctx, engine, spec)
}

func verifySharedInstanceTemplate(engine core.Engine, path string) error {
	upgrade, err := sharedInstanceTemplateNeedsUpgrade(engine, path)
	if err == nil && upgrade {
		return errors.New("shared core service template requires the managed permission update")
	}
	return err
}

func sharedInstanceTemplateNeedsUpgrade(engine core.Engine, path string) (bool, error) {
	want, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/systemd/qagent-"+managedCoreAssetName(engine)+"@.service")
	if err != nil {
		return false, err
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if bytes.Equal(got, want) {
		return false, nil
	}
	// Earlier generated templates duplicated StateDirectory directives. Accept
	// only the exact bundled historical variants, including Mihomo's old family
	// filter; extra administrator directives still require manual review.
	variants := [][]byte{want}
	if engine == core.EngineMihomo {
		variants = append(variants, bytes.Replace(want, []byte("Environment=SAFE_PATHS=/etc/qagent/mihomo/shares/%i:/etc/qcontrolhub/tls\n"), nil, 1))
	}
	if engine == core.EngineMihomo || engine == core.EngineSingBox {
		for _, value := range append([][]byte(nil), variants...) {
			variants = append(variants, bytes.Replace(value, []byte("AF_INET6 AF_NETLINK\n"), []byte("AF_INET6\n"), 1))
		}
	}
	for _, value := range variants {
		if bytes.Equal(got, value) || bytes.Equal(got, bytes.Replace(value, []byte("StateDirectoryMode=0750\n"), []byte("StateDirectoryMode=0750\nStateDirectory=qcontrolhub-"+managedCoreAssetName(engine)+"-%i\nStateDirectoryMode=0750\n"), 1)) {
			return true, nil
		}
	}
	return false, errors.New("shared core service template differs from the bundled QAgent asset")
}

func ensureOpenRCSharedInstanceService(engine core.Engine, spec EngineSpec) error {
	if !isSharedInstanceSpec(engine, spec, ServiceManagerOpenRC) {
		return errors.New("invalid OpenRC shared core service")
	}
	contents, err := openRCSharedInstanceContent(engine, spec)
	if err != nil {
		return err
	}
	if err := validateProtectedDirectoryChain("/etc/init.d"); err != nil {
		return err
	}
	path := filepath.Join("/etc/init.d", spec.Service)
	if err := validateOpenRCCoreOverrides(spec.Service); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err == nil {
		_, writeErr := file.WriteString(contents)
		if writeErr == nil {
			writeErr = file.Chmod(0o755)
		}
		if writeErr == nil {
			writeErr = file.Sync()
		}
		err = errors.Join(writeErr, file.Close())
		if err != nil {
			_ = os.Remove(path)
			return err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := validateManagedUnitFile(path); err != nil {
		return err
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(stored, []byte(contents)) {
		if !matchesAnyManagedDropIn(stored, managedOpenRCScriptVariants([]byte(contents))) {
			return fmt.Errorf("OpenRC shared service %s differs from recognized QAgent scripts; review administrator changes before permission sync", spec.Service)
		}
		_, err = atomicDeployWithDefaultMetadata(path, contents, fileMetadata{mode: 0o755})
		return err
	}
	return nil
}

func validateManagedSharedService(ctx context.Context, engine core.Engine, spec EngineSpec) error {
	if !isSharedInstanceSpec(engine, spec, ServiceManagerSystemd) {
		return errors.New("invalid shared systemd service")
	}
	base := "qagent-" + managedCoreAssetName(engine) + "@.service"
	if err := verifySharedInstanceTemplate(engine, filepath.Join(existingDiscoveryManagedUnitRoot, base)); err != nil {
		return err
	}
	for property, want := range map[string]string{
		"FragmentPath": filepath.Join(existingDiscoveryManagedUnitRoot, base), "User": managedCoreServiceGroup, "Group": managedCoreServiceGroup,
	} {
		got, err := systemdUnitProperty(ctx, spec.Service, property)
		if err != nil || got != want {
			return fmt.Errorf("shared service %s effective %s is not %q", spec.Service, property, want)
		}
	}
	return validateManagedUnitDropIns(ctx, spec.Service)
}

func openRCSharedInstanceContent(engine core.Engine, spec EngineSpec) (string, error) {
	baseName := "qagent-" + managedCoreAssetName(engine)
	asset, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/openrc/"+baseName)
	if err != nil {
		return "", err
	}
	base := DefaultSpecsForServiceManager(ServiceManagerOpenRC)[engine]
	contents := strings.ReplaceAll(string(asset), baseName, spec.Service)
	contents = strings.ReplaceAll(contents, base.ConfigPath, spec.ConfigPath)
	if engine == core.EngineMihomo {
		contents = strings.ReplaceAll(contents, "SAFE_PATHS=\""+filepath.Dir(base.ConfigPath)+":", "SAFE_PATHS=\""+filepath.Dir(spec.ConfigPath)+":")
	}
	contents = strings.ReplaceAll(contents, "/var/lib/qcontrolhub-"+managedCoreAssetName(engine),
		filepath.Join("/var/lib", "qcontrolhub-"+managedCoreAssetName(engine)+"-"+filepath.Base(filepath.Dir(spec.ConfigPath))))
	if engine == core.EngineShadowsocksRust {
		contents = strings.ReplaceAll(contents, shadowsocksRustACLPath, spec.ACLPath)
	}
	return contents, nil
}

func prepareSharedStateDirectory(path string) error {
	return repairManagedCoreStateDirectory(path)
}

func sharedInstanceSpec(engine core.Engine, base EngineSpec, shareID, managerKind string) (EngineSpec, error) {
	if !engine.Valid() || !core.ValidAgentShareID(shareID) {
		return EngineSpec{}, errors.New("invalid shared core instance identity")
	}
	name := "config.json"
	if engine == core.EngineMihomo {
		name = "config.yaml"
	}
	base.ConfigPath = filepath.Join(sharedInstanceRoot(engine), shareID, name)
	base.Service = fmt.Sprintf("qagent-%s@%s.service", managedCoreAssetName(engine), shareID)
	if managerKind == ServiceManagerOpenRC {
		base.Service = fmt.Sprintf("qagent-%s-%s", managedCoreAssetName(engine), shareID)
	}
	if engine == core.EngineShadowsocksRust {
		base.ACLPath = filepath.Join(filepath.Dir(base.ConfigPath), "qch-mainland-block.acl")
	}
	return base, nil
}

func isSharedInstanceSpec(engine core.Engine, spec EngineSpec, managerKind string) bool {
	shareID := filepath.Base(filepath.Dir(spec.ConfigPath))
	if !core.ValidAgentShareID(shareID) {
		return false
	}
	base := DefaultSpecsForServiceManager(managerKind)[engine]
	want, err := sharedInstanceSpec(engine, base, shareID, managerKind)
	return err == nil && spec == want
}

func prepareSharedInstanceDirectory(engine core.Engine, spec EngineSpec, managerKind string) error {
	if !isSharedInstanceSpec(engine, spec, managerKind) {
		return errors.New("invalid shared core service paths")
	}
	root := sharedInstanceRoot(engine)
	if err := validateProtectedDirectoryChain(filepath.Dir(root)); err != nil {
		return err
	}
	for _, directory := range []string{root, filepath.Dir(spec.ConfigPath)} {
		if err := os.Mkdir(directory, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := validateProtectedDirectoryChain(directory); err != nil {
			return err
		}
	}
	if engine == core.EngineShadowsocksRust {
		if err := ensureSharedShadowsocksRustACL(spec.ACLPath); err != nil {
			return err
		}
		if _, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, spec.ACLPath); err != nil {
			return err
		}
	}
	if managerKind == ServiceManagerOpenRC {
		statePath := filepath.Join("/var/lib", "qcontrolhub-"+managedCoreAssetName(engine)+"-"+filepath.Base(filepath.Dir(spec.ConfigPath)))
		if err := prepareSharedStateDirectory(statePath); err != nil {
			return err
		}
	}
	return nil
}

func ensureSharedShadowsocksRustACL(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if errors.Is(err, os.ErrExist) {
		return validateProtectedRegularFile(path)
	}
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString("# QControlHub private Shadowsocks Rust ACL.\n[outbound_block_list]\n")
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func validateProtectedRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("unsafe shared core file %s", path)
	}
	return validateOwner(info, "shared core file")
}

// A lost accounting policy must not leave any private instance accepting
// traffic. Directory identities are generated from validated share IDs only.
func stopSharedInstances(ctx context.Context, manager *ServiceManager, engine core.Engine, base EngineSpec) error {
	entries, err := os.ReadDir(sharedInstanceRoot(engine))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if !entry.IsDir() || !core.ValidAgentShareID(entry.Name()) {
			failures = append(failures, errors.New("unrecognized shared core instance directory"))
			continue
		}
		spec, err := sharedInstanceSpec(engine, base, entry.Name(), manager.Kind())
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if _, err := manager.command(ctx, spec.Service, core.ActionStop); err != nil {
			failures = append(failures, err)
		}
		if err := stopServiceTLSFrontends(ctx, manager, engine, spec); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// The pre-instance layout ran a recipient in the base service. Only stop that
// service when its current listeners actually include a metered shared port;
// the node owner's base service must continue when a private share fails.
func stopLegacySharedBase(ctx context.Context, manager *ServiceManager, engine core.Engine, base EngineSpec, sharedPorts []int) error {
	if len(sharedPorts) == 0 {
		return nil
	}
	status, err := manager.status(ctx, base.Service)
	if err != nil {
		return fmt.Errorf("inspect base core status before clearing shared enforcement: %w", err)
	}
	if status == "inactive" || status == "failed" {
		return nil
	}
	content, err := readConfigurationFile(base.ConfigPath)
	if err != nil {
		return fmt.Errorf("inspect base core before clearing shared enforcement: %w", err)
	}
	endpoints := serverconfig.DiscoverTrafficPorts(engine, content)
	for _, endpoint := range endpoints {
		if slices.Contains(sharedPorts, endpoint.Port) {
			_, err := manager.command(ctx, base.Service, core.ActionStop)
			return errors.Join(err, stopServiceTLSFrontends(ctx, manager, engine, base))
		}
	}
	return nil
}

func (e *Executor) SharedInstanceStatuses(ctx context.Context) []core.SharedInstanceStatus {
	e.specsMu.RLock()
	specs := make(map[core.Engine]EngineSpec, len(e.Specs))
	for engine, spec := range e.Specs {
		specs[engine] = spec
	}
	e.specsMu.RUnlock()
	var statuses []core.SharedInstanceStatus
	markedEgressChecked := false
	var markedEgressError error
	for engine, base := range specs {
		entries, err := os.ReadDir(sharedInstanceRoot(engine))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || !core.ValidAgentShareID(entry.Name()) {
				continue
			}
			spec, err := sharedInstanceSpec(engine, base, entry.Name(), e.serviceManager().Kind())
			if err != nil {
				continue
			}
			status, err := serviceStatusWithManager(ctx, e.serviceManager(), spec.Service)
			if err != nil {
				status = "unknown"
				if _, stopErr := e.serviceManager().command(ctx, spec.Service, core.ActionStop); stopErr == nil {
					status = "inactive"
				}
			}
			if status == "active" {
				content, readErr := readConfigurationFile(spec.ConfigPath)
				if readErr == nil {
					_, readErr = serverconfig.SharedTrafficEndpoints(engine, content)
				}
				var plan serverconfig.AccountingPlan
				if readErr == nil {
					plan, readErr = serverconfig.PrepareIndependentEgress(engine, content)
					if readErr == nil && plan.Source == "disabled" {
						readErr = errors.New("independent exit is disabled")
					}
				}
				if readErr == nil && plan.Source == "nft-dual" {
					if !markedEgressChecked {
						markedEgressError = checkOutboundMarkRouting(ctx)
						markedEgressChecked = true
					}
					readErr = markedEgressError
				}
				if readErr != nil {
					if _, stopErr := e.serviceManager().command(ctx, spec.Service, core.ActionStop); stopErr == nil {
						status = "inactive"
					} else {
						status = "unknown"
					}
				}
			}
			statuses = append(statuses, core.SharedInstanceStatus{Engine: engine, ShareID: entry.Name(), Status: status})
		}
	}
	return statuses
}

// Only create protected managed directories. The owner's configuration, unit,
// enablement and running services are untouched by a recipient's preparation.
func prepareSharedCorePrerequisites(base EngineSpec) error {
	for _, path := range []string{filepath.Dir(base.Binary), filepath.Dir(base.ConfigPath)} {
		if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
			return err
		}
		if err := os.Mkdir(path, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := validateProtectedDirectoryChain(path); err != nil {
			return err
		}
	}
	return nil
}
