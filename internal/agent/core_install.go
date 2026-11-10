package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func verifyCoreCandidate(ctx context.Context, engine core.Engine, candidatePath, releaseTag string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := coreVersionArgs(engine)
	output, err := run(ctx, candidatePath, args...)
	if err != nil {
		return output, fmt.Errorf("downloaded %s binary could not report its version: %w", engine, err)
	}
	if output == "" {
		return "", errors.New("downloaded core binary returned an empty version")
	}
	if expected, err := core.NormalizeCoreVersionSelector(releaseTag); err == nil {
		version := regexp.MustCompile(`(?i)(^|[^0-9a-z.+-])v?` + regexp.QuoteMeta(expected) + `($|[^0-9a-z.+-])`)
		if !version.MatchString(output) {
			return output, errors.New("downloaded core binary version does not match the selected release")
		}
	}
	if line, _, found := strings.Cut(output, "\n"); found {
		output = line
	}
	if len(output) > 200 {
		output = output[:200]
	}
	return coreInstallDiagnostic(output), nil
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

// Only the dynamic loader's missing GLIBC version diagnostic permits the
// same-release musl fallback. CLI, CPU, checksum and other failures stay fatal.
type ssRustGLIBCError struct{ error }

func (e ssRustGLIBCError) Unwrap() error { return e.error }

var missingGLIBCVersion = regexp.MustCompile("^/[^:\\r\\n]+: version `GLIBC_[0-9]+\\.[0-9]+(?:\\.[0-9]+)?' not found$")

func ssRustNeedsMusl(candidate, output string, err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || output == "" {
		return false
	}
	for _, line := range strings.Split(output, "\n") {
		body, ok := strings.CutPrefix(line, candidate+": ")
		if !ok {
			return false
		}
		body, ok = strings.CutSuffix(body, " (required by "+candidate+")")
		if !ok || !missingGLIBCVersion.MatchString(body) {
			return false
		}
	}
	return true
}

// Keep enough room for both attempts in the durable Agent receipt (4 KiB).
// Version commands have no configuration input and use the controlled env.
func coreInstallDiagnostic(value string) string {
	value = core.RedactDiagnostic(strings.ToValidUTF8(value, "�"))
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, value)
	const limit = 1400
	if len(value) > limit {
		value = strings.ToValidUTF8(value[:700], "�") + "\n... [truncated] ...\n" + strings.ToValidUTF8(value[len(value)-700:], "�")
	}
	return strings.TrimSpace(value)
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
