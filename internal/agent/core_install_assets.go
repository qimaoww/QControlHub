package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/qimaoww/qcontrolhub"
)

// This namespace is already writable in installed QAgent systemd units. Do
// not update /usr/local/share or require an Agent service-unit migration just
// to make the first binary-only upgrade work.
const coreInstallAssetRoot = "/usr/local/lib/qagent/core-install"
const coreInstallBootstrapName = "deploy/bootstrap-core-services.sh"

var coreInstallAssetsMu sync.Mutex

type coreInstallAsset struct {
	name    string
	content []byte
	mode    fs.FileMode
}

func bundledCoreInstallAssets() ([]coreInstallAsset, string, error) {
	var assets []coreInstallAsset
	digest := sha256.New()
	source := qcontrolhub.CoreInstallAssets()
	err := fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(source, name)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if name == coreInstallBootstrapName || strings.HasPrefix(name, "deploy/openrc/") {
			mode = 0o755
		}
		assets = append(assets, coreInstallAsset{name: name, content: content, mode: mode})
		fmt.Fprintf(digest, "%s\x00%o\x00%d\x00", name, mode, len(content))
		digest.Write(content)
		return nil
	})
	return assets, hex.EncodeToString(digest.Sum(nil)), err
}

// Publish the complete bundle by one directory rename. Interrupted staging
// never becomes executable input; old bundles and installer-cached resources
// are retained, and a modified published bundle fails closed instead of being
// overwritten or silently falling back to an older script.
func stageBundledCoreInstallAssets(directory string) (string, error) {
	coreInstallAssetsMu.Lock()
	defer coreInstallAssetsMu.Unlock()
	if os.Geteuid() != 0 {
		return "", errors.New("bundled core install assets require root")
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return "", errors.New("core install asset directory must be a clean absolute path")
	}
	if err := ensureCoreInstallAssetParent(filepath.Dir(directory)); err != nil {
		return "", fmt.Errorf("unsafe core install asset parent: %w", err)
	}
	if err := os.Mkdir(directory, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	if err := validateProtectedDirectoryChain(directory); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	assets, digest, err := bundledCoreInstallAssets()
	if err != nil {
		return "", err
	}
	scriptPath := filepath.Join(directory, digest, coreInstallBootstrapName)
	if _, err := root.Lstat(digest); err == nil {
		if err := verifyCoreInstallAssets(root, digest, assets); err != nil {
			return "", fmt.Errorf("bundled core install assets are unsafe: %w", err)
		}
		return scriptPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	tempName, err := randomCoreTempName(root)
	if err != nil {
		return "", err
	}
	if err := root.Mkdir(tempName, 0o700); err != nil {
		return "", err
	}
	defer root.RemoveAll(tempName)
	for _, asset := range assets {
		name := filepath.Join(tempName, asset.name)
		if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return "", err
		}
		output, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", err
		}
		_, writeErr := output.Write(asset.content)
		if writeErr == nil {
			writeErr = output.Chmod(asset.mode)
		}
		if writeErr == nil {
			writeErr = output.Sync()
		}
		closeErr := output.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return "", err
		}
	}
	if err := verifyCoreInstallAssets(root, tempName, assets); err != nil {
		return "", err
	}
	// Sync directory entries as well as the individual asset bytes before
	// exposing the versioned root to a transient systemd/OpenRC bootstrap.
	if err := syncCoreInstallDirectories(root, tempName); err != nil {
		return "", err
	}
	if err := root.Rename(tempName, digest); err != nil {
		// Another Agent process may have published the same immutable bundle.
		if verifyErr := verifyCoreInstallAssets(root, digest, assets); verifyErr != nil {
			return "", errors.Join(err, verifyErr)
		}
	}
	if err := syncRootDirectory(root); err != nil {
		return "", err
	}
	return scriptPath, nil
}

// Older hand-installed Agents may have no private binary directory yet.
// Build only a missing, protected parent chain; never chmod existing paths.
func ensureCoreInstallAssetParent(directory string) error {
	if _, err := os.Lstat(directory); err == nil {
		return validateProtectedDirectoryChain(directory)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(directory)
	if parent == directory {
		return errors.New("core install asset parent has no existing ancestor")
	}
	if err := ensureCoreInstallAssetParent(parent); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return validateProtectedDirectoryChain(directory)
}

func verifyCoreInstallAssets(root *os.Root, directory string, assets []coreInstallAsset) error {
	expected := make(map[string]coreInstallAsset, len(assets))
	for _, asset := range assets {
		expected[asset.name] = asset
	}
	seen := 0
	err := fs.WalkDir(root.FS(), directory, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Mode().Perm()&0o022 != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("unsafe core install asset %s", name)
		}
		if err := validateOwner(info, "core install asset"); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		asset, ok := expected[strings.TrimPrefix(name, directory+"/")]
		if !ok || info.Mode().Perm() != asset.mode || info.Size() != int64(len(asset.content)) {
			return fmt.Errorf("unexpected core install asset or metadata: %s", name)
		}
		input, err := root.Open(name)
		if err != nil {
			return err
		}
		openedInfo, statErr := input.Stat()
		content, readErr := io.ReadAll(io.LimitReader(input, int64(len(asset.content))+1))
		closeErr := input.Close()
		if err := errors.Join(statErr, readErr, closeErr); err != nil {
			return err
		}
		if !os.SameFile(info, openedInfo) || !bytes.Equal(content, asset.content) {
			return fmt.Errorf("core install asset content changed: %s", name)
		}
		seen++
		return nil
	})
	if err != nil {
		return err
	}
	if seen != len(assets) {
		return errors.New("core install asset bundle is incomplete")
	}
	return nil
}

func syncCoreInstallDirectories(root *os.Root, directory string) error {
	var directories []string
	err := fs.WalkDir(root.FS(), directory, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && entry.IsDir() {
			directories = append(directories, name)
		}
		return walkErr
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		handle, err := root.Open(directories[i])
		if err != nil {
			return err
		}
		if err := errors.Join(handle.Sync(), handle.Close()); err != nil {
			return err
		}
	}
	return nil
}
