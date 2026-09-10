package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Immutable bundles precede the atomic runtime-config commit. The core keeps
// consuming its validated merged config; rollback selects the old content and
// therefore the old bundle without ever mixing revisions of source fragments.
// Bundle paths are derived, never accepted from a remote request.
func writeManagedConfigFiles(engine core.Engine, configPath, content string, metadata fileMetadata) error {
	if engine != core.EngineXray && engine != core.EngineSingBox {
		return nil
	}
	files, err := serverconfig.SplitConfigFiles(engine, content)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(content))
	// Version the paired layout: neither numbered v1 nor split-outbound v2
	// snapshots may be overwritten during upgrade or rollback.
	name := "sources-v3-" + hex.EncodeToString(sum[:])
	directory := filepath.Dir(configPath)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if info, err := root.Lstat(name); err == nil {
		if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
			return errors.New("unsafe existing configuration bundle")
		}
		if err := validateOwner(info, "configuration bundle"); err != nil {
			return err
		}
		for _, file := range files {
			path := filepath.Join(name, file.Path)
			info, err := root.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o027 != 0 {
				return errors.New("unsafe configuration fragment")
			}
			value, err := root.ReadFile(path)
			if err != nil || string(value) != file.Content {
				return errors.New("configuration bundle content mismatch")
			}
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage, err := os.MkdirTemp(directory, ".sources-staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	setDirectory := func(path string) error {
		if err := os.Chmod(path, 0o750); err != nil {
			return err
		}
		if metadata.ownershipKnown {
			return os.Chown(path, metadata.uid, metadata.gid)
		}
		return nil
	}
	if err := setDirectory(stage); err != nil {
		return err
	}
	for _, file := range files {
		path := filepath.Join(stage, file.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		if err := setDirectory(filepath.Dir(path)); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, metadata.mode)
		if err != nil {
			return err
		}
		if metadata.ownershipKnown {
			err = f.Chown(metadata.uid, metadata.gid)
		}
		if err == nil {
			_, err = f.WriteString(file.Content)
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for _, path := range []string{"inbounds", "outbounds", "."} {
		f, err := os.Open(filepath.Join(stage, path))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		err = f.Sync()
		_ = f.Close()
		if err != nil {
			return err
		}
	}
	if err := root.Rename(filepath.Base(stage), name); err != nil {
		return fmt.Errorf("publish configuration bundle: %w", err)
	}
	return syncRootDirectory(root)
}
