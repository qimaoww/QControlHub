package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (e *Executor) readCurrentConfig(ctx context.Context, engine core.Engine, spec EngineSpec) (string, error) {
	content, err := readConfigurationFile(spec.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("read current %s configuration: %w", engine, err)
	}
	if err := validatePrivilegedExecutable(spec.Binary); err != nil {
		return "", fmt.Errorf("cannot safely invoke %s for current configuration validation: %w", engine, err)
	}
	defaultSpec, managed := DefaultSpecsForServiceManager(e.serviceManager().Kind())[engine]
	if managed && spec == defaultSpec && engine != core.EngineShadowsocksRust {
		_, err = e.validateManagedServiceSnapshot(ctx, engine, spec, content)
	} else {
		_, err = e.validateSnapshot(ctx, engine, spec, content)
	}
	if err != nil {
		return "", fmt.Errorf("current %s configuration failed real core validation: %w", engine, err)
	}
	return content, nil
}

func (e *Executor) readExistingConfig(ctx context.Context, engine core.Engine, managed, existing EngineSpec) (string, error) {
	plan, err := e.readExistingConfigPlan(ctx, engine, managed, existing)
	return plan.managedContent, err
}

func (e *Executor) readExistingConfigPlan(ctx context.Context, engine core.Engine, managed, existing EngineSpec) (coreImportPlan, error) {
	if existing.SourceContext != "" {
		if err := verifyExistingServiceMapping(ctx, engine, existing, e.serviceManager()); err != nil {
			return coreImportPlan{}, err
		}
	}
	var content, sourceDigest string
	var err error
	if engine == core.EngineXray && existing.ConfigDirectory != "" {
		sourceDigest, err = readExistingXraySourceDigest(existing)
	} else {
		content, sourceDigest, err = readExistingConfigurationSources(existing)
	}
	if err != nil {
		return coreImportPlan{}, fmt.Errorf("read existing %s configuration: %w", engine, err)
	}
	if err := validateExistingCoreExecutable(existing.Binary); err != nil {
		return coreImportPlan{}, fmt.Errorf("cannot safely invoke existing %s binary: %w", engine, err)
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(existing.Binary)); err != nil {
		return coreImportPlan{}, fmt.Errorf("cannot safely traverse existing %s binary path: %w", engine, err)
	}
	if err := validateExistingServiceExecutable(existing); err != nil {
		return coreImportPlan{}, fmt.Errorf("cannot safely map existing %s service executable: %w", engine, err)
	}
	binaryDigest, present, err := protectedExistingCoreMigrationFileDigest(existing.Binary, maxReleaseAssetSize)
	if err != nil || !present {
		return coreImportPlan{}, fmt.Errorf("fingerprint existing %s binary: %w", engine, errors.Join(err, errors.New("binary unavailable")))
	}
	binaryIdentity, err := importFileIdentity(existing.Binary, true)
	if err != nil {
		return coreImportPlan{}, err
	}
	invocationSpec, cleanupInvocation, err := e.existingCoreInvocationSpec(engine, managed, existing)
	if err != nil {
		return coreImportPlan{}, fmt.Errorf("prepare existing %s binary for protected invocation: %w", engine, err)
	}
	defer cleanupInvocation()
	validationSpec := managed
	validationSpec.Binary = invocationSpec.Binary
	validationSpec.commandEnv = invocationSpec.commandEnv
	if engine == core.EngineXray && existing.ConfigDirectory != "" {
		// Xray's multi-file semantics are typed and tag-aware: later scalar
		// sections replace earlier ones, while same-tag inbounds/outbounds are
		// updated rather than appended. Reusing sing-box's generic JSON merge
		// would silently change a valid service. Ask the protected source binary
		// for its canonical merged form instead and fail closed if it cannot dump
		// one.
		content, err = dumpExistingXrayConfiguration(ctx, invocationSpec)
		if err != nil {
			return coreImportPlan{}, fmt.Errorf("dump existing Xray configuration sources: %w", err)
		}
	} else if engine != core.EngineXray {
		if err := validateExistingSourceInvocation(ctx, engine, invocationSpec); err != nil {
			return coreImportPlan{}, fmt.Errorf("existing %s configuration sources failed real core validation: %w", engine, err)
		}
	}
	plan := coreImportPlan{managedContent: content}
	switch engine {
	case core.EngineShadowsocksRust:
		var planErr error
		plan, planErr = prepareSSRustImport(existing, content, managed)
		if planErr != nil {
			return coreImportPlan{}, planErr
		}
		content = plan.managedContent
	case core.EngineXray:
		content, err = normalizeImportedXrayLogDestinations(content)
		if err != nil {
			return coreImportPlan{}, fmt.Errorf("normalize existing Xray log destinations: %w", err)
		}
	case core.EngineSingBox:
		content, err = normalizeImportedSingBoxLogDestination(content)
		if err != nil {
			return coreImportPlan{}, fmt.Errorf("normalize existing sing-box log destination: %w", err)
		}
	}
	if engine == core.EngineMihomo {
		plan, err = prepareMihomoDependencyImport(existing, managed, content)
		if err != nil {
			return coreImportPlan{}, fmt.Errorf("resolve existing Mihomo dependencies: %w", err)
		}
	}
	if engine == core.EngineXray || engine == core.EngineSingBox {
		plan, err = prepareJSONDependencyImport(engine, existing, managed, content)
		if err != nil {
			return coreImportPlan{}, fmt.Errorf("resolve existing %s dependencies: %w", engine, err)
		}
	}
	plan.managedContent = contentIfEmpty(plan.managedContent, content)
	plan.auxiliary, err = inspectImportAuxiliary(ctx, e.serviceManager(), engine, existing, plan)
	if err != nil {
		return coreImportPlan{}, fmt.Errorf("inspect existing auxiliary service: %w", err)
	}
	if err := e.validateImportPlanSnapshot(ctx, engine, validationSpec, plan); err != nil {
		return coreImportPlan{}, fmt.Errorf("existing %s configuration failed real core validation: %w", engine, err)
	}
	if err := plan.verifySources(); err != nil {
		return coreImportPlan{}, err
	}
	var currentDigest string
	if engine == core.EngineXray && existing.ConfigDirectory != "" {
		currentDigest, err = readExistingXraySourceDigest(existing)
	} else {
		_, currentDigest, err = readExistingConfigurationSources(existing)
	}
	if err != nil || currentDigest != sourceDigest {
		if err == nil {
			err = errors.New("configuration sources changed while the snapshot was validated")
		}
		return coreImportPlan{}, fmt.Errorf("recheck existing %s configuration sources: %w", engine, err)
	}
	currentBinary, present, err := protectedExistingCoreMigrationFileDigest(existing.Binary, maxReleaseAssetSize)
	currentBinaryIdentity, identityErr := importFileIdentity(existing.Binary, true)
	if err != nil || identityErr != nil || !present || currentBinary != binaryDigest || currentBinaryIdentity != binaryIdentity {
		return coreImportPlan{}, errors.New("existing core binary changed while the snapshot was validated")
	}
	if existing.SourceContext != "" {
		if err := verifyExistingServiceMapping(ctx, engine, existing, e.serviceManager()); err != nil {
			return coreImportPlan{}, err
		}
	}
	plan.snapshotDigest = coreMigrationConfigDigest(coreMigrationSourceDigest(existing) + "\x00" + sourceDigest + "\x00" + binaryDigest + "\x00" + binaryIdentity + "\x00" + importPlanFingerprint(plan))
	return plan, nil
}

func contentIfEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
