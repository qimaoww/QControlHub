package agent

import (
	"errors"
	"fmt"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"
)

// resolveImportSource follows links as data. Both the link's directory and the
// final target must be protected; resolving a path never grants read access.
func resolveImportSource(path string) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("import source must be an absolute path without control characters")
	}
	// EvalSymlinks bounds link traversal and rejects broken links and cycles.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	// Check each lexical parent as well as each resolved parent. A writable link
	// directory must not be able to redirect a later snapshot to another file.
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSymlink == 0 && info.Mode()&os.ModeSticky == 0 {
			return "", fmt.Errorf("import source %s is writable by group or others", current)
		}
		if err := validateOwner(info, "import source"); err != nil {
			return "", err
		}
		if current == filepath.Dir(current) {
			break
		}
		current = filepath.Dir(current)
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(resolved)); err != nil {
		return "", err
	}
	return resolved, nil
}

func readImportSource(path string, limit int64) ([]byte, string, error) {
	resolved, err := resolveImportSource(path)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return nil, "", err
	}
	file, opened, err := openProtectedCoreMigrationFile(resolved, info, limit)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > limit {
		return nil, "", fmt.Errorf("import source exceeds %d bytes", limit)
	}
	after, err := file.Stat()
	if err != nil || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return nil, "", errors.New("import source changed while being read")
	}
	again, err := filepath.EvalSymlinks(path)
	if err != nil || again != resolved {
		return nil, "", errors.New("import source link changed while being read")
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(opened, current) || current.Size() != after.Size() || !current.ModTime().Equal(after.ModTime()) {
		return nil, "", errors.New("import source replaced while being read")
	}
	return data, resolved, nil
}

func readImportConfigurationFile(path string) (string, error) {
	data, resolved, err := readImportSource(path, core.MaxConfigBytes)
	if err != nil {
		return "", err
	}
	if err := validateImportResourceAuthorization(path, resolved); err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", errors.New("configuration is not valid UTF-8")
	}
	return string(data), nil
}

func validateImportDirectory(path string) error {
	resolved, err := resolveImportSource(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a real directory", path)
	}
	return nil
}

func importFileIdentity(path string, installerBinary ...bool) (string, error) {
	resolved, err := resolveImportSource(path)
	if len(installerBinary) > 0 && installerBinary[0] && installerCoreAllowsOrphanOwner(path) {
		// Preserve the existing, separately verified orphan-owner compatibility
		// policy for installer binaries. It never applies to configuration data.
		err = validateExistingCoreExecutable(path)
		resolved = path
	}
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("source filesystem does not expose a stable file identity")
	}
	// Access time changes during an ordinary read. Pin the inode and change
	// time instead, so a replacement with equal bytes cannot reuse a snapshot.
	return fmt.Sprintf("%s:%d:%d:%d:%d:%d:%d:%d:%d:%d", resolved, stat.Dev, stat.Ino, stat.Uid, stat.Gid, info.Mode(), info.Size(), info.ModTime().UnixNano(), stat.Ctim.Sec, stat.Ctim.Nsec), nil
}

func validateImportResourceAuthorization(path, resolved string) error {
	for _, candidate := range []string{path, resolved} {
		if pathWithin(candidate, managedCoreConfigurationRoot) || pathWithin(candidate, "/etc/qcontrolhub/panel-certificates") || pathWithin(candidate, "/etc/qcontrolhub/tls") {
			return errors.New("source belongs to a managed instance or panel certificate and requires its existing ownership mapping")
		}
	}
	return nil
}
