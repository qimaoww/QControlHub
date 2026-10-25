package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (e *Executor) validateCoreUpdateCandidate(ctx context.Context, engine core.Engine, spec EngineSpec, content, binary, assetDirectory string) error {
	redact := coreDiagnosticRedactor(engine, content)
	if err := core.ValidateConfig(engine, content); err != nil {
		return err
	}
	metadata, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, spec.ConfigPath)
	if err != nil {
		return err
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return err
	}
	if err := e.verifyCoreCertificateAccess(ctx, engine, spec, content, identity); err != nil {
		return err
	}
	state := coreCertificateWorkingDirectory(engine, spec, e.serviceManager().Kind())
	if err := prepareSharedStateDirectory(state); err != nil {
		return fmt.Errorf("prepare service working directory: %w", err)
	}
	environment := ""
	var args []string
	switch engine {
	case core.EngineMihomo:
		environment = "SAFE_PATHS=" + filepath.Dir(spec.ConfigPath) + ":/etc/qcontrolhub/tls"
		if e.serviceManager().Kind() == ServiceManagerOpenRC {
			// Mihomo may write/download data during -t. Use the existing isolated
			// validation home when the manager has no read-only transient units.
			_, err := e.validateSnapshotCandidate(ctx, engine, spec, content, &identity, metadata, binary, environment)
			return err
		}
		args = []string{"-t", "-d", state, "-f", spec.ConfigPath}
	case core.EngineXray:
		if err := validateXrayXHTTPCapabilities(ctx, binary, content); err != nil {
			return err
		}
		environment = "XRAY_LOCATION_ASSET=" + assetDirectory
		args = []string{"run", "-test", "-config", spec.ConfigPath}
	case core.EngineSingBox:
		if err := validateSingBoxCapabilities(ctx, binary, content); err != nil {
			return err
		}
		args = []string{"check", "-c", spec.ConfigPath}
	case core.EngineShadowsocksRust:
		return e.validateSSRustUpdateCandidate(ctx, spec, content, binary, identity, metadata)
	default:
		return fmt.Errorf("unsupported engine %s", engine)
	}
	validationCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var output string
	if e.serviceManager().Kind() == ServiceManagerSystemd {
		output, err = e.runSystemdValidationCommand(validationCtx, state, environment, binary, identity,
			[]string{"ReadWritePaths=", "RuntimeMaxSec=45s"}, args...)
	} else {
		output, err = e.runManagedIdentityCommand(validationCtx, state, environment, binary, &identity, args...)
	}
	if err != nil {
		output = redact(output)
		if len(output) > 2048 {
			output = strings.ToValidUTF8(output[len(output)-2048:], "�")
		}
		return fmt.Errorf("target %s rejected deployed configuration: %w; %s", engine, err, output)
	}
	return nil
}
