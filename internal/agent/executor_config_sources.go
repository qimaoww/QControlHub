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
	var content, sourceDigest string
	var err error
	if engine == core.EngineXray && existing.ConfigDirectory != "" {
		sourceDigest, err = readExistingXraySourceDigest(existing)
	} else {
		content, sourceDigest, err = readExistingConfigurationSources(existing)
	}
	if err != nil {
		return "", fmt.Errorf("read existing %s configuration: %w", engine, err)
	}
	if existing.WorkingDirectory != "" {
		if err := validateNoRelativeSingBoxResources(content); err != nil {
			return "", fmt.Errorf("existing %s configuration has a resource that cannot be migrated safely: %w", engine, err)
		}
	}
	if err := validateExistingCoreExecutable(existing.Binary); err != nil {
		return "", fmt.Errorf("cannot safely invoke existing %s binary: %w", engine, err)
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(existing.Binary)); err != nil {
		return "", fmt.Errorf("cannot safely traverse existing %s binary path: %w", engine, err)
	}
	if err := validateExistingServiceExecutable(existing); err != nil {
		return "", fmt.Errorf("cannot safely map existing %s service executable: %w", engine, err)
	}
	invocationSpec, cleanupInvocation, err := e.existingCoreInvocationSpec(engine, managed, existing)
	if err != nil {
		return "", fmt.Errorf("prepare existing %s binary for protected invocation: %w", engine, err)
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
			return "", fmt.Errorf("dump existing Xray configuration sources: %w", err)
		}
	} else if engine != core.EngineXray {
		if err := validateExistingSourceInvocation(ctx, engine, invocationSpec); err != nil {
			return "", fmt.Errorf("existing %s configuration sources failed real core validation: %w", engine, err)
		}
	}
	switch engine {
	case core.EngineShadowsocksRust:
		plan, planErr := prepareSSRustImport(existing, content)
		if planErr != nil {
			return "", planErr
		}
		content = plan.managedContent
	case core.EngineXray:
		content, err = normalizeImportedXrayLogDestinations(content)
		if err != nil {
			return "", fmt.Errorf("normalize existing Xray log destinations: %w", err)
		}
	case core.EngineSingBox:
		content, err = normalizeImportedSingBoxLogDestination(content)
		if err != nil {
			return "", fmt.Errorf("normalize existing sing-box log destination: %w", err)
		}
	}
	if _, err := e.validateSnapshot(ctx, engine, validationSpec, content); err != nil {
		return "", fmt.Errorf("existing %s configuration failed real core validation: %w", engine, err)
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
		return "", fmt.Errorf("recheck existing %s configuration sources: %w", engine, err)
	}
	return content, nil
}
