package agent

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub"
)

func ensureOpenRCOutboundMarkCapability(service string) error {
	if service != "qagent-mihomo" && service != "qagent-shadowsocks-rust" && service != "qagent-sing-box" {
		return errors.New("outbound marks require a managed proxy service")
	}
	path := filepath.Join(openRCInitRoot, service)
	if err := validateProtectedDirectoryChain(openRCInitRoot); err != nil {
		return err
	}
	if err := validatePrivilegedExecutable(path); err != nil {
		return err
	}
	want, err := fs.ReadFile(qcontrolhub.CoreInstallAssets(), "deploy/openrc/"+service)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if bytes.Equal(current, want) {
		return nil
	}
	legacy := bytes.ReplaceAll(want, []byte("^cap_net_bind_service,^cap_net_admin"), []byte("^cap_net_bind_service"))
	if !bytes.Equal(current, legacy) {
		return errors.New("outbound mark upgrade refused an unrecognized OpenRC service")
	}
	// Preserve the previous exact script as a protected backup; no arbitrary
	// administrator script is modified and service enablement is unchanged.
	_, err = atomicDeployWithDefaultMetadata(path, string(want), fileMetadata{mode: 0o755})
	return err
}
