package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func verifyCoreCandidate(ctx context.Context, engine core.Engine, candidatePath, releaseTag string) (string, error) {
	args := coreVersionArgs(engine)
	output, err := run(ctx, candidatePath, args...)
	if err != nil {
		return output, fmt.Errorf("downloaded %s binary could not report its version: %w", engine, err)
	}
	if output == "" {
		return "", errors.New("downloaded core binary returned an empty version")
	}
	if expected, err := core.NormalizeCoreVersionSelector(releaseTag); err == nil && !strings.Contains(strings.ToLower(output), strings.ToLower(expected)) {
		return output, errors.New("downloaded core binary version does not match the selected release")
	}
	if line, _, found := strings.Cut(output, "\n"); found {
		output = line
	}
	if len(output) > 200 {
		output = output[:200]
	}
	return strings.TrimSpace(output), nil
}

func coreVersionArgs(engine core.Engine) []string {
	if engine == core.EngineMihomo {
		return []string{"-v"}
	}
	if engine == core.EngineShadowsocksRust {
		return []string{"--version"}
	}
	return []string{"version"}
}

func replaceCoreBinary(root *os.Root, destinationName, candidateName string) (string, error) {
	return replaceCoreFile(root, destinationName, candidateName, 0o755)
}

func replaceCoreAsset(root *os.Root, destinationName, candidateName string) (string, error) {
	return replaceCoreFile(root, destinationName, candidateName, 0o644)
}

func replaceCoreFile(root *os.Root, destinationName, candidateName string, defaultMode os.FileMode) (string, error) {
	metadata := fileMetadata{mode: defaultMode}
	var backupName string
	if info, err := root.Lstat(destinationName); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
			return "", errors.New("existing core binary is not a protected regular file")
		}
		if err := validateOwner(info, "core binary"); err != nil {
			return "", err
		}
		metadata = metadataFromFileInfo(info)
		suffix, err := randomSuffix(6)
		if err != nil {
			return "", err
		}
		backupName = destinationName + ".bak-" + time.Now().UTC().Format("20060102T150405Z") + "-" + suffix
		if err := copyFileInRoot(root, destinationName, backupName, metadata); err != nil {
			return "", fmt.Errorf("back up current core binary: %w", err)
		}
		if err := cleanupBackups(root, destinationName, backupName, 2); err != nil {
			_ = root.Remove(backupName)
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := applyRootFileMetadata(root, candidateName, metadata); err != nil {
		return "", fmt.Errorf("preserve core binary metadata: %w", err)
	}
	if err := root.Rename(candidateName, destinationName); err != nil {
		return "", err
	}
	directory, err := root.Open(".")
	if err == nil {
		err = directory.Sync()
		directory.Close()
	}
	if err != nil {
		_, rollbackErr := rollbackCoreBinary(root, destinationName, backupName)
		if rollbackErr != nil {
			return backupName, fmt.Errorf("sync core binary directory: %v; rollback failed: %w", err, rollbackErr)
		}
		return "", fmt.Errorf("sync core binary directory: %w", err)
	}
	return backupName, nil
}

func rollbackCoreBinary(root *os.Root, destinationName, backupName string) (string, error) {
	if backupName == "" {
		if err := root.Remove(destinationName); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err := syncRootDirectory(root); err != nil {
			return "", err
		}
		return "rollback: removed newly installed core binary", nil
	}
	if !strings.HasPrefix(backupName, destinationName+".bak-") || filepath.Base(backupName) != backupName {
		return "", errors.New("core binary backup name is invalid")
	}
	if err := root.Rename(backupName, destinationName); err != nil {
		return "", err
	}
	if err := syncRootDirectory(root); err != nil {
		return "", err
	}
	return "rollback: previous core binary restored", nil
}
