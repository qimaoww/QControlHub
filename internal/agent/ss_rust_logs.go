package agent

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub"
)

// Update only the exact bundled OpenRC script's historical info-only variant.
// This stages logging for the next explicit core restart, never restarts a
// running proxy or rewrites an administrator's service/configuration.
func ensureOpenRCSSRustLogging(service string) error {
	if service != "qagent-shadowsocks-rust" {
		return errors.New("SS Rust logging requires the managed OpenRC service")
	}
	if err := validateProtectedDirectoryChain(openRCInitRoot); err != nil {
		return err
	}
	path := filepath.Join(openRCInitRoot, service)
	if err := validatePrivilegedExecutable(path); err != nil {
		return err
	}
	current, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/openrc/"+service)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(openRCInitRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	contents, err := fs.ReadFile(root.FS(), service)
	if err != nil || bytes.Equal(contents, current) {
		return err
	}
	legacy := bytes.Replace(current, []byte(managedSSRustLogFilter), []byte("info"), 1)
	if !bytes.Equal(contents, legacy) {
		return errors.New("SS Rust OpenRC logging upgrade refused an unrecognized service script")
	}
	name, err := randomCoreTempName(root)
	if err != nil {
		return err
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	_, writeErr := file.Write(current)
	if writeErr == nil {
		writeErr = file.Chmod(0o755)
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if err := validatePrivilegedExecutable(path); err != nil {
		return err
	}
	latest, err := fs.ReadFile(root.FS(), service)
	if err != nil || !bytes.Equal(contents, latest) {
		return errors.New("SS Rust service changed during logging upgrade")
	}
	return root.Rename(name, service)
}
