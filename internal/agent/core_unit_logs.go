package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	managedCoreJournalService = "systemd-journald@qagent-cores.service"
	managedCoreLogDropIn      = `[Service]
LogNamespace=qagent-cores
StandardOutput=journal
StandardError=journal
`
	managedCoreLogFallbackDropIn = `[Service]
LogNamespace=
StandardOutput=journal
StandardError=journal
`
	managedCoreJournalConfig = `[Journal]
Storage=volatile
RuntimeMaxUse=16M
RuntimeMaxFileSize=8M
MaxRetentionSec=15min
`
)

func ensureManagedCoreLogStreaming(ctx context.Context, specs map[core.Engine]EngineSpec, managers ...*ServiceManager) error {
	return ensureManagedCoreLogStreamingWithPolicy(ctx, specs, core.AgentPolicy{CoreLogMaxMiB: 16, CoreLogRotateCount: 1}, managers...)
}

func managedCoreJournalConfigForPolicy(policy core.AgentPolicy) []byte {
	maxBytes := uint64(policy.CoreLogMaxMiB) << 20
	fileBytes := maxBytes / uint64(policy.CoreLogRotateCount+1)
	return []byte(fmt.Sprintf("[Journal]\nStorage=volatile\nRuntimeMaxUse=%d\nRuntimeMaxFileSize=%d\nMaxRetentionSec=15min\n", maxBytes, fileBytes))
}

func ensureManagedCoreLogStreamingWithPolicy(ctx context.Context, specs map[core.Engine]EngineSpec, policy core.AgentPolicy, managers ...*ServiceManager) error {
	if policy.CoreLogMaxMiB == 0 {
		policy.CoreLogMaxMiB = 16
		policy.CoreLogRotateCount = 1
	}
	manager := selectedServiceManager(managers...)
	installedSpecs := make(map[core.Engine]EngineSpec, len(specs))
	for engine, spec := range specs {
		defaultSpec, exists := DefaultSpecsForServiceManager(manager.Kind())[engine]
		if !exists || spec != defaultSpec {
			continue
		}
		status, err := validateManagedServiceForExistingDiscovery(ctx, engine, spec, manager)
		if err != nil {
			return fmt.Errorf("validate managed log service %s: %w", spec.Service, err)
		}
		if status != "not-found" {
			installedSpecs[engine] = spec
		}
	}
	if len(installedSpecs) == 0 {
		return nil
	}
	if manager.Kind() == ServiceManagerOpenRC {
		return ensureOpenRCCoreLogDirectory()
	}
	ensureContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	base := defaultCoreUnitCapabilitySyncer()
	base.dropInRoot = "/etc/systemd"
	for _, executable := range []string{base.systemdRunPath, base.installPath, base.teePath, base.movePath, base.removePath, base.systemctlPath} {
		if err := validatePrivilegedExecutable(executable); err != nil {
			return fmt.Errorf("unsafe managed-log helper %s: %w", executable, err)
		}
	}
	if err := validateManagedUnitDirectory(base.dropInRoot); err != nil {
		return err
	}

	journalChanged, err := installManagedLogFile(ensureContext, base,
		"/etc/systemd/journald@qagent-cores.conf.d/10-qcontrolhub-volatile.conf",
		managedCoreJournalConfigForPolicy(policy))
	if err != nil {
		return fmt.Errorf("configure volatile core journal: %w", err)
	}
	if journalChanged {
		if output, err := run(ensureContext, base.systemctlPath, "daemon-reload"); err != nil {
			return fmt.Errorf("reload systemd after core journal update: %w: %s", err, output)
		}
		// A namespaced journald process only reads its limits on start. Restarting
		// this isolated namespace does not affect the host journal or proxy cores.
		_, _ = run(ensureContext, base.systemctlPath, "restart", managedCoreJournalService)
	}
	dropIn := []byte(managedCoreLogDropIn)
	if err := startManagedCoreJournal(ensureContext, base.systemctlPath); err != nil {
		dropIn = []byte(managedCoreLogFallbackDropIn)
	}
	changed := false
	for _, spec := range installedSpecs {
		installed, installErr := installManagedLogFile(ensureContext, base,
			filepath.Join("/etc/systemd/system", spec.Service+".d", "20-qcontrolhub-volatile-logs.conf"),
			dropIn)
		if installErr != nil {
			return fmt.Errorf("configure volatile logs for %s: %w", spec.Service, installErr)
		}
		changed = changed || installed
	}
	if changed {
		if output, err := run(ensureContext, base.systemctlPath, "daemon-reload"); err != nil {
			return fmt.Errorf("reload systemd after core log update: %w: %s", err, output)
		}
	}
	return nil
}

// ensureOpenRCCoreLogDirectory prepares the root that supervise-daemon
// output_log files for managed core services live under. The OpenRC service
// scripts recreate it through checkpath on every start; this keeps the
// collector working even when a service has not been started yet.
func ensureOpenRCCoreLogDirectory() error {
	if err := os.MkdirAll(openRCCoreLogRoot, 0o750); err != nil {
		return fmt.Errorf("create managed OpenRC core log directory: %w", err)
	}
	info, err := os.Lstat(openRCCoreLogRoot)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("managed OpenRC core log path is not a real directory")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.New("managed OpenRC core log directory is writable by group or others")
	}
	if err := validateOwner(info, "managed OpenRC core log directory"); err != nil {
		return err
	}
	return nil
}

func startManagedCoreJournal(ctx context.Context, systemctl string) error {
	if output, err := run(ctx, systemctl, "start", managedCoreJournalService); err != nil {
		return fmt.Errorf("start volatile core journal: %w: %s", err, output)
	}
	status, err := waitForServiceState(ctx, "active", 500*time.Millisecond, 100*time.Millisecond, func(probeContext context.Context) (string, error) {
		output, probeErr := run(probeContext, systemctl, "is-active", managedCoreJournalService)
		if probeErr != nil {
			trimmed := strings.TrimSpace(output)
			if trimmed == "inactive" || trimmed == "failed" || trimmed == "activating" {
				return trimmed, nil
			}
			return trimmed, probeErr
		}
		return strings.TrimSpace(output), nil
	})
	if err != nil || status != "active" {
		if err == nil {
			err = fmt.Errorf("journal service is %s", status)
		}
		return fmt.Errorf("verify volatile core journal: %w", err)
	}
	return nil
}

func installManagedLogFile(ctx context.Context, syncer coreUnitCapabilitySyncer, destination string, contents []byte) (bool, error) {
	if !filepath.IsAbs(destination) || !pathWithin(destination, syncer.dropInRoot) {
		return false, errors.New("managed log path escapes /etc/systemd")
	}
	directory := filepath.Dir(destination)
	if err := syncer.runHelper(ctx, nil, syncer.installPath, "-d", "-m", "0755", directory); err != nil {
		return false, fmt.Errorf("create managed log directory: %w", err)
	}
	if err := validateManagedUnitDirectory(directory); err != nil {
		return false, err
	}
	if _, err := os.Lstat(destination); err == nil {
		if err := validateManagedUnitFile(destination); err != nil {
			return false, err
		}
		existing, err := os.ReadFile(destination)
		if err != nil {
			return false, err
		}
		if bytes.Equal(existing, contents) {
			return false, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	suffix, err := randomSuffix(10)
	if err != nil {
		return false, err
	}
	temporaryPath := filepath.Join(directory, ".qcontrolhub-core-logs-"+suffix+".tmp")
	if err := syncer.runHelper(ctx, contents, syncer.teePath, temporaryPath); err != nil {
		return false, fmt.Errorf("write managed log configuration: %w", err)
	}
	moved := false
	defer func() {
		if !moved {
			cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			_ = syncer.runHelper(cleanupContext, nil, syncer.removePath, "-f", temporaryPath)
		}
	}()
	if err := syncer.runHelper(ctx, nil, syncer.movePath, "-fT", temporaryPath, destination); err != nil {
		return false, fmt.Errorf("install managed log configuration: %w", err)
	}
	moved = true
	if err := validateManagedUnitFile(destination); err != nil {
		return false, err
	}
	return true, nil
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
