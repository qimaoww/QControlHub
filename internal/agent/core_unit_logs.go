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
	managedCoreJournalConfig = `[Journal]
Storage=volatile
RuntimeMaxUse=16M
RuntimeMaxFileSize=2M
MaxRetentionSec=15min
`
)

func ensureManagedCoreLogStreaming(ctx context.Context, specs map[core.Engine]EngineSpec, managers ...*ServiceManager) error {
	if len(managers) > 0 && managers[0] != nil && managers[0].Kind() == ServiceManagerOpenRC {
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

	changed, err := installManagedLogFile(ensureContext, base,
		"/etc/systemd/journald@qagent-cores.conf.d/10-qcontrolhub-volatile.conf",
		[]byte(managedCoreJournalConfig))
	if err != nil {
		return fmt.Errorf("configure volatile core journal: %w", err)
	}
	for engine, spec := range specs {
		defaultSpec, exists := DefaultSpecs()[engine]
		if !exists || spec != defaultSpec || !managedCoreServiceName(spec.Service) {
			continue
		}
		installed, installErr := installManagedLogFile(ensureContext, base,
			filepath.Join("/etc/systemd/system", spec.Service+".d", "20-qcontrolhub-volatile-logs.conf"),
			[]byte(managedCoreLogDropIn))
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
	if err := startManagedCoreJournal(ensureContext, base.systemctlPath); err != nil {
		return err
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
