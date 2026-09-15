package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func openCoreMigrationStateRoot(prefix string) (*os.Root, error) {
	if prefix == "" {
		return nil, errors.New("core migration marker path is not configured")
	}
	directory := filepath.Dir(prefix)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	if err := validateProtectedDirectoryChain(directory); err != nil {
		return nil, fmt.Errorf("core migration state directory is unsafe: %w", err)
	}
	return os.OpenRoot(directory)
}

func coreMigrationBackupName(prefix string, engine core.Engine, kind string) string {
	return filepath.Base(coreMigrationMarkerPath(prefix, engine)) + "." + kind + "-backup"
}

func coreMigrationBackupPath(prefix string, engine core.Engine, kind string) string {
	return filepath.Join(filepath.Dir(prefix), coreMigrationBackupName(prefix, engine, kind))
}

func cleanupCoreMigrationBackups(prefix string, engine core.Engine) error {
	root, err := openCoreMigrationStateRoot(prefix)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, kind := range coreMigrationBackupKinds() {
		if err := root.Remove(coreMigrationBackupName(prefix, engine, kind)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncRootDirectory(root)
}

func coreMigrationBackupKinds() []string {
	return []string{"binary", "config", "asset-geoip.dat", "asset-geosite.dat"}
}
