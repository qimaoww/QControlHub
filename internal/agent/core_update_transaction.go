package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// One release replaces a binary shared by every private instance. Validate the
// complete deployment before stopping anything, then recover the same set of
// instances if publication or activation fails.
type coreUpdateTransaction interface {
	Preflight(context.Context, *os.Root, string, map[string]string) (string, error)
	Stop(context.Context) (string, error)
	Activate(context.Context) (string, error)
	Recover(context.Context) (string, error)
	Close()
}

type coreUpdateInstance struct {
	spec       EngineSpec
	content    string
	wasActive  bool
	start      bool
	configured bool
}

type managedCoreUpdate struct {
	executor  *Executor
	engine    core.Engine
	spec      EngineSpec
	manager   *ServiceManager
	instances []coreUpdateInstance
	locks     []*sync.RWMutex
}

func managedCoreUpdateTransaction(executor *Executor, engine core.Engine, spec EngineSpec, manager *ServiceManager) coreUpdateTransaction {
	manager = selectedServiceManager(manager)
	if spec != DefaultSpecsForServiceManager(manager.Kind())[engine] {
		return nil // Custom executors do not own the project's private instances.
	}
	if executor == nil {
		executor = &Executor{Services: manager}
	}
	return &managedCoreUpdate{executor: executor, engine: engine, spec: spec, manager: manager}
}

func (update *managedCoreUpdate) Close() {
	for index := len(update.locks) - 1; index >= 0; index-- {
		update.locks[index].Unlock()
	}
	update.locks = nil
}

func (update *managedCoreUpdate) Preflight(ctx context.Context, root *os.Root, candidate string, assets map[string]string) (string, error) {
	_, binaryErr := os.Lstat(update.spec.Binary)
	if binaryErr != nil && !errors.Is(binaryErr, os.ErrNotExist) {
		return "", binaryErr
	}
	if err := update.addInstance(ctx, update.spec, errors.Is(binaryErr, os.ErrNotExist), false); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(sharedInstanceRoot(update.engine))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect shared configurations: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !core.ValidAgentShareID(entry.Name()) {
			return "", errors.New("unrecognized entry in shared configuration directory")
		}
		lock := update.executor.sharedAccountingLock(update.engine, entry.Name())
		lock.Lock()
		update.locks = append(update.locks, lock)
		spec, err := sharedInstanceSpec(update.engine, update.spec, entry.Name(), update.manager.Kind())
		if err != nil {
			return "", err
		}
		if err := update.addInstance(ctx, spec, false, true); err != nil {
			return "", err
		}
	}
	if errors.Is(binaryErr, os.ErrNotExist) {
		for _, instance := range update.instances {
			if instance.wasActive {
				return "", fmt.Errorf("service %s is running without its installed binary; restore that binary before updating so rollback remains possible", instance.spec.Service)
			}
		}
	}
	// The service user needs execute/read access to the candidate and exactly
	// this release's Xray resources, without exposing or replacing live assets.
	suffix, err := randomSuffix(10)
	if err != nil {
		return "", err
	}
	workspace := ".qch-update-check-" + suffix
	if err := root.Mkdir(workspace, 0o755); err != nil {
		return "", err
	}
	defer root.RemoveAll(workspace)
	if err := copyFileInRoot(root, candidate, filepath.Join(workspace, filepath.Base(update.spec.Binary)), fileMetadata{mode: 0o755}); err != nil {
		return "", err
	}
	for name, staged := range assets {
		if err := copyFileInRoot(root, staged, filepath.Join(workspace, name), fileMetadata{mode: 0o644}); err != nil {
			return "", err
		}
	}
	directory := filepath.Join(filepath.Dir(update.spec.Binary), workspace)
	checked := 0
	for _, instance := range update.instances {
		if !instance.configured {
			continue
		}
		if err := update.executor.validateCoreUpdateCandidate(ctx, update.engine, instance.spec, instance.content,
			filepath.Join(directory, filepath.Base(update.spec.Binary)), directory); err != nil {
			return "", fmt.Errorf("service %s: %w", instance.spec.Service, err)
		}
		checked++
	}
	return fmt.Sprintf("target release validated %d deployed configuration(s); stopped instances remain stopped", checked), nil
}

func (update *managedCoreUpdate) addInstance(ctx context.Context, spec EngineSpec, fresh, shared bool) error {
	status, err := serviceStatusWithManager(ctx, update.manager, spec.Service)
	if err != nil {
		return fmt.Errorf("inspect %s before update: %w", spec.Service, err)
	}
	if status != "active" && status != "inactive" && status != "failed" {
		return fmt.Errorf("service %s is %s; retry after its state settles", spec.Service, status)
	}
	instance := coreUpdateInstance{spec: spec, wasActive: status == "active"}
	instance.content, err = readConfigurationFile(spec.ConfigPath)
	if errors.Is(err, os.ErrNotExist) && !instance.wasActive {
		if shared {
			state, stateErr := readSharedDeploymentState(spec)
			if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
				return fmt.Errorf("inspect %s deployment identity: %w", spec.Service, stateErr)
			}
			if state.ConfigID != "" || state.Digest != "" {
				return fmt.Errorf("service %s has lost its deployed configuration", spec.Service)
			}
		}
		err = nil // A prepared but never deployed instance has nothing to load.
	} else if err == nil {
		instance.configured = true
		if shared {
			state, stateErr := readSharedDeploymentState(spec)
			if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
				return fmt.Errorf("inspect %s deployment identity: %w", spec.Service, stateErr)
			}
			if state.Digest != "" && state.Digest != configurationDigest(instance.content) {
				return fmt.Errorf("service %s configuration differs from its deployed revision", spec.Service)
			}
		}
		if err := ensureManagedCoreServiceCapabilities(ctx, update.engine, spec, update.manager); err != nil {
			return err
		}
	}
	if err != nil {
		return fmt.Errorf("read %s configuration before update: %w", spec.Service, err)
	}
	instance.start = instance.wasActive || fresh && instance.configured
	update.instances = append(update.instances, instance)
	return nil
}

func (update *managedCoreUpdate) Stop(ctx context.Context) (string, error) {
	return update.apply(ctx, core.ActionStop, false)
}

func (update *managedCoreUpdate) Activate(ctx context.Context) (string, error) {
	return update.apply(ctx, core.ActionStart, false)
}

func (update *managedCoreUpdate) Recover(ctx context.Context) (string, error) {
	return update.apply(ctx, core.ActionStart, true)
}

func (update *managedCoreUpdate) apply(ctx context.Context, action core.Action, recovering bool) (string, error) {
	var outputs []string
	var failures []error
	for _, instance := range update.instances {
		if !instance.start {
			continue
		}
		instanceAction := action
		if recovering && !instance.wasActive {
			// Also stop a partially started first install, including respawn.
			instanceAction = core.ActionStop
		}
		output, err := serviceCommandAndVerifyWithManager(ctx, update.manager, instance.spec.Service, instanceAction)
		redact := coreDiagnosticRedactor(update.engine, instance.content)
		outputs = append(outputs, redact(output))
		if err != nil {
			failures = append(failures, fmt.Errorf("%s %s: %s", instanceAction, instance.spec.Service, redact(err.Error())))
		}
	}
	return strings.Join(outputs, "\n"), errors.Join(failures...)
}

func coreUpdateRecoveryContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Minute)
}

func joinCoreUpdateOutput(values ...string) string {
	var parts []string
	for _, value := range values {
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}

// Compensation is best effort for every resource and the executable. An
// individual failed rename must never prevent the remaining recovery steps.
func rollbackCoreUpdateFiles(root *os.Root, binary, backup string, assets map[string]string) (string, error) {
	var outputs []string
	var failures []error
	for _, name := range xrayMigrationAssetNames {
		assetBackup, exists := assets[name]
		if !exists {
			continue
		}
		output, err := rollbackCoreBinary(root, name, assetBackup)
		outputs = append(outputs, output)
		if err != nil {
			failures = append(failures, fmt.Errorf("restore Xray resource %s: %w", name, err))
		}
	}
	output, err := rollbackCoreBinary(root, binary, backup)
	outputs = append(outputs, output)
	if err != nil {
		failures = append(failures, fmt.Errorf("restore core binary: %w", err))
	}
	return joinCoreUpdateOutput(outputs...), errors.Join(failures...)
}
