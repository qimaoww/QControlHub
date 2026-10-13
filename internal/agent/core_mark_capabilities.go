package agent

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub"
)

func ensureOpenRCOutboundMarkCapability(service string) error {
	if service != "qagent-mihomo" && service != "qagent-shadowsocks-rust" && service != "qagent-sing-box" && service != "qagent-xray" {
		return errors.New("outbound marks require a managed proxy service")
	}
	path := filepath.Join(openRCInitRoot, service)
	if err := validateProtectedDirectoryChain(openRCInitRoot); err != nil {
		return err
	}
	if err := validatePrivilegedExecutable(path); err != nil {
		return err
	}
	if err := validateOpenRCCoreOverrides(service); err != nil {
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
	if !matchesAnyManagedDropIn(current, managedOpenRCScriptVariants(want)) {
		return errors.New("outbound mark upgrade refused an unrecognized OpenRC service")
	}
	// Preserve the previous exact script as a protected backup; no arbitrary
	// administrator script is modified and service enablement is unchanged.
	_, err = atomicDeployWithDefaultMetadata(path, string(want), fileMetadata{mode: 0o755})
	return err
}

func managedOpenRCScriptVariants(want []byte) [][]byte {
	legacyLog := bytes.ReplaceAll(want, []byte(managedSSRustLogFilter), []byte("info"))
	legacyCaps := func(value []byte) []byte {
		return bytes.ReplaceAll(value, []byte("^cap_net_bind_service,^cap_net_admin"), []byte("^cap_net_bind_service"))
	}
	variants := [][]byte{want, legacyCaps(want), legacyLog, legacyCaps(legacyLog)}
	for _, value := range append([][]byte(nil), variants...) {
		text := string(value)
		start, end := strings.Index(text, "\n# QCH core bounding policy begin\n"), strings.Index(text, "# QCH core bounding policy end\n")
		if start >= 0 && end >= start {
			text = text[:start] + text[end+len("# QCH core bounding policy end\n"):]
			variants = append(variants, []byte(text))
		}
	}
	for _, value := range append([][]byte(nil), variants...) {
		lines := strings.Split(string(value), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "export SAFE_PATHS=\"") {
				variants = append(variants, []byte(strings.Join(append(lines[:i:i], lines[i+1:]...), "\n")))
				break
			}
		}
	}
	return variants
}

func validateOpenRCCoreOverrides(service string) error {
	// openrc-run sources conf.d after the init script. Even an exact script
	// can therefore launch a different user or lose its capabilities.
	root := filepath.Join(filepath.Dir(openRCInitRoot), "conf.d")
	paths, err := filepath.Glob(filepath.Join(root, service+".*"))
	if err != nil {
		return err
	}
	paths = append(paths, filepath.Join(root, service))
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(contents), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				return fmt.Errorf("service %s has administrator OpenRC override %s; review command_user, capabilities, no_new_privs and directory before restoring the bundled policy", service, path)
			}
		}
	}
	return nil
}
