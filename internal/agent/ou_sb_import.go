package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const ouSBResourceLimit = 4 << 20

var (
	ouSBConfigPath       = "/etc/sing-box/config.json"
	ouSBCertificateRoot  = "/etc/ou-sb/certs"
	ouSBManagedStateRoot = "/var/lib/qcontrolhub-sing-box"
)

type coreImportResource struct {
	source      string
	destination string
}

type coreImportPlan struct {
	managedContent string
	resourceRoot   string
	stateRoot      string
	resources      []coreImportResource
	auxiliary      coreMigrationAuxiliaryService
}

type coreMigrationAuxiliaryService struct {
	name         string
	enableState  string
	initialState string
}

func prepareOUSBSingBoxImport(ctx context.Context, manager *ServiceManager, existing EngineSpec, content string) (coreImportPlan, error) {
	if filepath.Clean(existing.ConfigPath) != filepath.Clean(ouSBConfigPath) ||
		(existing.Service != "sing-box.service" && existing.Service != "sing-box") {
		return coreImportPlan{managedContent: content}, nil
	}

	requestDigest := coreMigrationConfigDigest(content)
	resourceRoot := filepath.Join(ouSBManagedStateRoot, "ou-sb", requestDigest)
	replacements := map[string]string{
		filepath.Join(ouSBCertificateRoot, "fullchain.pem"): filepath.Join(resourceRoot, "fullchain.pem"),
		filepath.Join(ouSBCertificateRoot, "privkey.pem"):   filepath.Join(resourceRoot, "privkey.pem"),
	}
	managedContent, used, err := rewriteOUSBSingBoxResources(content, replacements)
	if err != nil {
		return coreImportPlan{}, err
	}
	if len(used) == 0 {
		return coreImportPlan{managedContent: content}, nil
	}

	plan := coreImportPlan{managedContent: managedContent, resourceRoot: resourceRoot}
	for _, source := range []string{
		filepath.Join(ouSBCertificateRoot, "fullchain.pem"),
		filepath.Join(ouSBCertificateRoot, "privkey.pem"),
	} {
		if used[source] {
			plan.resources = append(plan.resources, coreImportResource{source: source, destination: replacements[source]})
		}
	}
	plan.auxiliary, err = inspectOUSBAuxiliaryService(ctx, manager)
	if err != nil {
		return coreImportPlan{}, err
	}
	return plan, nil
}

func rewriteOUSBSingBoxResources(content string, replacements map[string]string) (string, map[string]bool, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return "", nil, fmt.Errorf("parse OU-SB sing-box configuration: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return "", nil, fmt.Errorf("parse OU-SB sing-box configuration: %w", err)
	}
	used := make(map[string]bool)
	var rewrite func(any) (any, error)
	rewrite = func(value any) (any, error) {
		switch current := value.(type) {
		case map[string]any:
			for key, child := range current {
				rewritten, err := rewrite(child)
				if err != nil {
					return nil, err
				}
				current[key] = rewritten
			}
		case []any:
			for index, child := range current {
				rewritten, err := rewrite(child)
				if err != nil {
					return nil, err
				}
				current[index] = rewritten
			}
		case string:
			if destination, ok := replacements[current]; ok {
				used[current] = true
				return destination, nil
			}
			if strings.HasPrefix(filepath.Clean(current), filepath.Clean(filepath.Dir(ouSBCertificateRoot))+string(filepath.Separator)) {
				return nil, fmt.Errorf("OU-SB resource %s is not supported for protected import", current)
			}
		}
		return value, nil
	}
	rewritten, err := rewrite(document)
	if err != nil {
		return "", nil, err
	}
	encoded, err := json.MarshalIndent(rewritten, "", "  ")
	if err != nil {
		return "", nil, err
	}
	return string(encoded) + "\n", used, nil
}

func (plan coreImportPlan) active() bool {
	return len(plan.resources) > 0
}

func (plan coreImportPlan) stage(identity commandIdentity) error {
	if !plan.active() {
		return nil
	}
	stateRoot := plan.stateRoot
	if stateRoot == "" {
		stateRoot = ouSBManagedStateRoot
	}
	if err := prepareImportResourceDirectory(stateRoot, plan.resourceRoot, identity); err != nil {
		return err
	}
	for _, resource := range plan.resources {
		if err := copyProtectedImportResource(resource.source, resource.destination, identity); err != nil {
			return err
		}
	}
	return nil
}

func (plan coreImportPlan) cleanup() error {
	if !plan.active() {
		return nil
	}
	for _, resource := range plan.resources {
		if err := os.Remove(resource.destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Remove(plan.resourceRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func prepareOUSBResourceDirectory(directory string, identity commandIdentity) error {
	return prepareImportResourceDirectory(ouSBManagedStateRoot, directory, identity)
}

func prepareImportResourceDirectory(stateRoot, directory string, identity commandIdentity) error {
	root := filepath.Clean(stateRoot)
	relative, err := filepath.Rel(root, filepath.Clean(directory))
	if err != nil || filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("managed import resource directory escapes the core state root")
	}
	if err := validateManagedStateRoot(root, identity); err != nil {
		return err
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			return errors.New("managed import resource directory is invalid")
		}
		current = filepath.Join(current, component)
		if err := os.Mkdir(current, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o027 != 0 {
			return fmt.Errorf("managed import resource directory %s is unsafe", current)
		}
		if err := os.Chown(current, 0, int(identity.gid)); err != nil {
			return err
		}
		if err := os.Chmod(current, 0o750); err != nil {
			return err
		}
	}
	return nil
}

func validateManagedStateRoot(path string, identity commandIdentity) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect managed core state directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o027 != 0 {
		return errors.New("managed core state directory is unsafe")
	}
	uid, gid, known := fileOwnership(info)
	if known && !((uid == 0 || uid == int(identity.uid)) && (gid == 0 || gid == int(identity.gid))) {
		return errors.New("managed core state directory has unexpected ownership")
	}
	return validateProtectedDirectoryChain(filepath.Dir(path))
}

func copyProtectedImportResource(source, destination string, identity commandIdentity) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect protected import resource %s: %w", source, err)
	}
	input, _, err := openProtectedCoreMigrationFile(source, info, ouSBResourceLimit)
	if err != nil {
		return fmt.Errorf("open protected import resource %s: %w", source, err)
	}
	defer input.Close()
	root, err := os.OpenRoot(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer root.Close()
	tempName, err := randomCoreTempName(root)
	if err != nil {
		return err
	}
	defer root.Remove(tempName)
	output, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, ouSBResourceLimit+1))
	if copyErr == nil && (written <= 0 || written > ouSBResourceLimit) {
		copyErr = errors.New("protected import resource is empty or exceeds the supported limit")
	}
	if copyErr == nil {
		copyErr = applyFileMetadata(output, fileMetadata{mode: 0o640, uid: 0, gid: int(identity.gid), ownershipKnown: true})
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := root.Rename(tempName, filepath.Base(destination)); err != nil {
		return err
	}
	return syncRootDirectory(root)
}

func inspectOUSBAuxiliaryService(ctx context.Context, manager *ServiceManager) (coreMigrationAuxiliaryService, error) {
	manager = selectedServiceManager(manager)
	service := "ou-sb-firewall.service"
	if manager.Kind() == ServiceManagerOpenRC {
		service = "ou-sb-firewall"
		info, err := os.Lstat(filepath.Join(openRCInitRoot, service))
		if errors.Is(err, os.ErrNotExist) {
			return coreMigrationAuxiliaryService{}, nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
			return coreMigrationAuxiliaryService{}, errors.New("OU-SB OpenRC firewall helper is unsafe")
		}
		if err := validateOwner(info, "OU-SB OpenRC firewall helper"); err != nil {
			return coreMigrationAuxiliaryService{}, err
		}
	} else {
		loadState, err := run(ctx, manager.executable, "show", service, "--property=LoadState", "--value")
		if err != nil || strings.TrimSpace(loadState) == "not-found" {
			return coreMigrationAuxiliaryService{}, nil
		}
		if strings.TrimSpace(loadState) != "loaded" {
			return coreMigrationAuxiliaryService{}, fmt.Errorf("OU-SB firewall helper load state is %s", strings.TrimSpace(loadState))
		}
	}
	status, err := manager.status(ctx, service)
	if err != nil {
		return coreMigrationAuxiliaryService{}, fmt.Errorf("inspect OU-SB firewall helper status: %w", err)
	}
	if status != "active" && status != "inactive" {
		return coreMigrationAuxiliaryService{}, fmt.Errorf("OU-SB firewall helper status is %s", status)
	}
	enableState, err := serviceEnableState(ctx, service, manager)
	if err != nil {
		return coreMigrationAuxiliaryService{}, fmt.Errorf("inspect OU-SB firewall helper enablement: %w", err)
	}
	if !migrationEnableStatesSupported(enableState, "disabled") {
		return coreMigrationAuxiliaryService{}, fmt.Errorf("OU-SB firewall helper enablement is %s", enableState)
	}
	return coreMigrationAuxiliaryService{name: service, enableState: enableState, initialState: status}, nil
}

func stopAndDisableMigrationAuxiliary(ctx context.Context, record coreMigrationRecord, manager *ServiceManager) error {
	if record.AuxiliaryService == "" {
		return nil
	}
	if status, err := manager.status(ctx, record.AuxiliaryService); err != nil {
		return err
	} else if status != "inactive" {
		if _, err := serviceCommandAndVerifyWithManager(ctx, manager, record.AuxiliaryService, core.ActionStop); err != nil {
			return err
		}
	}
	return disableServiceCompletely(ctx, record.AuxiliaryService, manager)
}

func restoreMigrationAuxiliary(ctx context.Context, record coreMigrationRecord, manager *ServiceManager) error {
	if record.AuxiliaryService == "" {
		return nil
	}
	if err := restoreServiceEnableState(ctx, record.AuxiliaryService, record.AuxiliaryEnableState, manager); err != nil {
		return err
	}
	status, err := manager.status(ctx, record.AuxiliaryService)
	if err != nil {
		return err
	}
	if record.AuxiliaryInitialState == "active" && status != "active" {
		_, err = serviceCommandAndVerifyWithManager(ctx, manager, record.AuxiliaryService, core.ActionStart)
		return err
	}
	if record.AuxiliaryInitialState == "inactive" && status != "inactive" {
		_, err = serviceCommandAndVerifyWithManager(ctx, manager, record.AuxiliaryService, core.ActionStop)
		return err
	}
	return nil
}
