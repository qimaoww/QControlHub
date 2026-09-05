package agent

import (
	"context"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type AgentUpgradePreflight struct {
	Protocol    int    `json:"protocol"`
	Version     string `json:"version"`
	AssetDigest string `json:"asset_digest"`
}

// Run in the candidate process before replacing the current executable. It
// has no enrollment, discovery, service, network, or production-config side
// effects: it only prepares and validates its own bundled installation files.
func PreflightAgentUpgrade(version, managerKind, assetRoot string) (AgentUpgradePreflight, error) {
	if _, err := NewServiceManager(managerKind); err != nil {
		return AgentUpgradePreflight{}, err
	}
	if _, err := stageBundledCoreInstallAssets(assetRoot); err != nil {
		return AgentUpgradePreflight{}, err
	}
	_, digest, err := bundledCoreInstallAssets()
	return AgentUpgradePreflight{Protocol: 1, Version: version, AssetDigest: digest}, err
}

type agentUpgradeTransaction struct {
	executable      string
	candidate       string
	backup          string
	originalDigest  string
	candidateDigest string
	metadata        fileMetadata
}

func prepareAgentUpgrade(ctx context.Context, executable, candidate, version, managerKind string) (*agentUpgradeTransaction, error) {
	return prepareAgentUpgradeWithAssets(ctx, executable, candidate, version, managerKind, coreInstallAssetRoot)
}

func prepareAgentUpgradeWithAssets(ctx context.Context, executable, candidate, version, managerKind, assetRoot string) (*agentUpgradeTransaction, error) {
	if filepath.Dir(executable) != filepath.Dir(candidate) || !filepath.IsAbs(executable) || executable == candidate {
		return nil, errors.New("Agent upgrade candidate must be a separate file beside the executable")
	}
	if err := validatePrivilegedExecutable(executable); err != nil {
		return nil, err
	}
	info, err := os.Lstat(executable)
	if err != nil {
		return nil, err
	}
	original, exists, err := protectedCoreMigrationFileDigest(executable, core.MaxAgentBinaryBytes)
	if err != nil || !exists {
		if err == nil {
			err = os.ErrNotExist
		}
		return nil, fmt.Errorf("inspect current Agent binary: %w", err)
	}
	digest, exists, err := protectedCoreMigrationFileDigest(candidate, core.MaxAgentBinaryBytes)
	if err != nil || !exists {
		if err == nil {
			err = os.ErrNotExist
		}
		return nil, fmt.Errorf("inspect candidate Agent binary: %w", err)
	}
	if err := validateAgentExecutableArchitecture(candidate); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(candidate, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	metadata := metadataFromFileInfo(info)
	metadata.mode &= os.ModePerm
	err = applyFileMetadata(file, metadata)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return nil, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output, err := run(checkCtx, candidate, "upgrade-preflight", managerKind, assetRoot)
	if err != nil {
		return nil, fmt.Errorf("candidate Agent preflight failed; current binary unchanged: %w", err)
	}
	var report AgentUpgradePreflight
	if json.Unmarshal([]byte(output), &report) != nil || report.Protocol != 1 || len(report.AssetDigest) != 64 ||
		(version != "" && report.Version != version) {
		return nil, errors.New("candidate Agent preflight returned an incompatible build or resource manifest")
	}
	if _, err := hex.DecodeString(report.AssetDigest); err != nil {
		return nil, errors.New("candidate Agent resource digest is invalid")
	}
	if current, ok, err := protectedCoreMigrationFileDigest(candidate, core.MaxAgentBinaryBytes); err != nil || !ok || current != digest {
		return nil, errors.New("candidate Agent binary changed during preflight")
	}
	return &agentUpgradeTransaction{executable: executable, candidate: candidate,
		backup: executable + ".previous-" + original, originalDigest: original, candidateDigest: digest, metadata: metadata}, nil
}

func validateAgentExecutableArchitecture(path string) error {
	file, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("Agent candidate is not an ELF executable: %w", err)
	}
	defer file.Close()
	machines := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64, "386": elf.EM_386, "arm": elf.EM_ARM, "riscv64": elf.EM_RISCV, "ppc64le": elf.EM_PPC64, "s390x": elf.EM_S390}
	if machine, ok := machines[runtime.GOARCH]; !ok || file.Machine != machine || (file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN) {
		return errors.New("Agent candidate has an incompatible architecture or executable type")
	}
	return nil
}

func (transaction *agentUpgradeTransaction) commit() error {
	if digest, exists, err := protectedCoreMigrationFileDigest(transaction.executable, core.MaxAgentBinaryBytes); err != nil || !exists || digest != transaction.originalDigest {
		return errors.New("current Agent executable changed before upgrade")
	}
	backupExists := false
	if info, err := os.Lstat(transaction.backup); err == nil {
		if digest, _, err := protectedCoreMigrationFileDigest(transaction.backup, core.MaxAgentBinaryBytes); err != nil || !info.Mode().IsRegular() || digest != transaction.originalDigest {
			return errors.New("previous Agent backup is unsafe")
		}
		backupExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Keep the existing executable in place until its durable backup exists.
	if !backupExists {
		if _, err := copyExistingCoreBinary(transaction.executable, transaction.backup); err != nil {
			return fmt.Errorf("backup current Agent: %w", err)
		}
	}
	if digest, _, err := protectedCoreMigrationFileDigest(transaction.backup, core.MaxAgentBinaryBytes); err != nil || digest != transaction.originalDigest {
		return errors.New("Agent backup digest mismatch")
	}
	if digest, _, err := protectedCoreMigrationFileDigest(transaction.candidate, core.MaxAgentBinaryBytes); err != nil || digest != transaction.candidateDigest {
		return errors.New("Agent candidate changed before atomic replacement")
	}
	if err := os.Rename(transaction.candidate, transaction.executable); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(transaction.executable))
	if err != nil {
		return errors.Join(err, transaction.rollback())
	}
	defer root.Close()
	if err := syncRootDirectory(root); err != nil {
		return errors.Join(err, transaction.rollback())
	}
	return nil
}

func (transaction *agentUpgradeTransaction) rollback() error {
	current, exists, err := protectedCoreMigrationFileDigest(transaction.executable, core.MaxAgentBinaryBytes)
	if err != nil || !exists {
		return errors.New("cannot safely inspect Agent upgrade destination for rollback")
	}
	if current == transaction.originalDigest {
		return nil
	}
	if current != transaction.candidateDigest {
		return errors.New("Agent executable was modified externally; refusing rollback overwrite")
	}
	backup, exists, err := protectedCoreMigrationFileDigest(transaction.backup, core.MaxAgentBinaryBytes)
	if err != nil || !exists || backup != transaction.originalDigest {
		return errors.New("previous Agent backup is missing or changed")
	}
	if _, err := copyExistingCoreBinary(transaction.backup, transaction.executable); err != nil {
		return err
	}
	file, err := os.OpenFile(transaction.executable, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = applyFileMetadata(file, transaction.metadata)
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}
