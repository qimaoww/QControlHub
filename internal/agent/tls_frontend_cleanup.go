package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func tlsDeploymentReferences(d tlsDeployment) []core.TLSFrontend {
	refs := append([]core.TLSFrontend{}, d.Frontends...)
	if d.NativeHandoff != nil {
		refs = append(refs, d.NativeHandoff.Frontends...)
	}
	return refs
}

// Persist retired references in the same atomic manifest as the replacement.
// Never discard them until cleanup succeeds, including after an Agent restart.
func retainRetiredTLS(previous, next tlsDeployments) {
	for service, old := range previous {
		d := next[service]
		refs := append(tlsDeploymentReferences(old), old.RetiredFrontends...)
		refs = append(refs, d.RetiredFrontends...)
		seen := map[[32]byte]bool{}
		d.RetiredFrontends = nil
		for _, f := range refs {
			data, _ := json.Marshal(f)
			key := sha256.Sum256(data)
			if !seen[key] {
				d.RetiredFrontends = append(d.RetiredFrontends, f)
				seen[key] = true
			}
		}
		if len(d.RetiredFrontends) > 0 {
			next[service] = d
		}
	}
}

// Delete through directory handles: the proxy account can write its state,
// but a symlink there must never redirect privileged cleanup outside the root.
func removeTLSFiles(directory string, names ...string) error {
	if err := validateProtectedDirectoryChain(filepath.Dir(directory)); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("不安全的反代清理目录")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range names {
		if err = root.RemoveAll(name); err != nil {
			return err
		}
	}
	return nil
}

func (m *tlsFrontendManager) removeUnusedTLSService(ctx context.Context, mode string) error {
	service, unit, modern := m.serviceDefinition(mode, false)
	stored, err := os.ReadFile(unit)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if exists {
		if err = validateManagedUnitFile(unit); err != nil {
			return err
		}
		_, _, legacy := m.serviceDefinition(mode, true)
		if string(stored) != modern && string(stored) != legacy {
			return fmt.Errorf("%s 已被用户修改，无法自动卸载", unit)
		}
		if m.services.Kind() == ServiceManagerSystemd {
			if err = validateManagedUnitDropIns(ctx, service); err != nil {
				return err
			}
		}
		if output, stopErr := serviceCommandAndVerifyWithManager(ctx, m.services, service, core.ActionStop); stopErr != nil {
			return fmt.Errorf("卸载 %s 前停止失败：%w；%s", service, stopErr, output)
		}
		if err = setServiceEnabled(ctx, service, false, m.services); err != nil {
			return err
		}
	}
	if err = removeTLSFiles(m.root, mode+".conf", mode+".staged.conf", mode+"-runtime.pending"); err != nil {
		return err
	}
	// Caddy's dedicated project binary is independent of system packages.
	// Nginx may serve unrelated sites, so never remove its system package.
	if mode == "caddy" && m.root == tlsFrontendRoot && m.state == tlsFrontendState {
		if err = validateProtectedDirectoryChain(filepath.Dir(managedCaddyBinary)); err != nil {
			return err
		}
		if err = removeOwnedCaddyBinary(); err != nil {
			return err
		}
	}
	if exists {
		if err = os.Remove(unit); err != nil {
			return err
		}
	}
	// Repeat on retry even if the prior attempt already removed the unit.
	if m.services.Kind() == ServiceManagerSystemd {
		if _, err = run(ctx, m.services.executable, "daemon-reload"); err != nil {
			return err
		}
	}
	return nil
}

func (m *tlsFrontendManager) cleanupRetiredTLS(ctx context.Context, deployments tlsDeployments) (resultErr error) {
	defer func() {
		if resultErr != nil {
			resultErr = fmt.Errorf("[阶段:反代资源清理] %w；待清理引用已保留，将自动重试", resultErr)
		}
	}()
	retired := tlsDeployments{}
	retiredModes := map[string]bool{}
	keepPages, keepKeys, keepDomains, modes := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	nginxAutomatic := false
	for service, d := range deployments {
		if len(d.RetiredFrontends) > 0 {
			retired[service] = tlsDeployment{Frontends: d.RetiredFrontends}
			for _, f := range d.RetiredFrontends {
				retiredModes[f.Mode] = true
			}
		}
		for _, f := range tlsDeploymentReferences(d) {
			f.StaticPageOwner = service
			keepPages[staticPageFile(f)] = true
			keepKeys[frontendKey(f)] = true
			keepDomains[strings.ToLower(f.Domain)] = true
			modes[f.Mode] = true
			nginxAutomatic = nginxAutomatic || f.AutomaticCertificate
		}
	}
	if len(retired) == 0 {
		return nil
	}
	// Stopped instances and a pending deployment's rollback references count.
	// Unit removal happens only after the last owner releases the provider.
	for _, mode := range []string{"caddy", "nginx"} {
		if retiredModes[mode] && !modes[mode] {
			if err := m.removeUnusedTLSService(ctx, mode); err != nil {
				return err
			}
		}
	}
	for service, d := range retired {
		for _, f := range d.Frontends {
			f.StaticPageOwner = service
			var names []string
			if f.StaticHTML != "" && !keepPages[staticPageFile(f)] {
				names = append(names, staticPageFile(f))
			}
			if !keepKeys[frontendKey(f)] {
				names = append(names, frontendKey(f)+".crt", frontendKey(f)+".key")
			}
			if !keepDomains[strings.ToLower(f.Domain)] {
				for _, kind := range []string{"certificate", "retry"} {
					names = append(names, filepath.Join("acme", filepath.Base(m.nginxACMEPath(kind, f.Domain))))
				}
			}
			if err := removeTLSFiles(m.root, names...); err != nil {
				return err
			}
		}
	}
	if retiredModes["nginx"] && !nginxAutomatic {
		if err := removeTLSFiles(m.root, "acme"); err != nil {
			return err
		}
	}
	if retiredModes["caddy"] && !modes["caddy"] {
		if err := removeTLSFiles(m.state, "caddy", "data", "config", "caddy-admin.sock", "caddy.log"); err != nil {
			return err
		}
	} else if err := m.cleanupCaddyDomains(retired, keepDomains); err != nil {
		return err
	}
	if retiredModes["nginx"] && !modes["nginx"] {
		if err := removeTLSFiles(m.state, "client_temp", "proxy_temp", "fastcgi_temp", "uwsgi_temp", "scgi_temp", "nginx.pid"); err != nil {
			return err
		}
	}
	if err := m.cleanupPanelFrontendCopies(deployments, retired); err != nil {
		return err
	}
	clean := tlsDeployments{}
	for service, d := range deployments {
		d.RetiredFrontends = nil
		if len(d.Frontends) > 0 || d.NativeHandoff != nil {
			clean[service] = d
		}
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return err
	}
	return m.writeConfig(filepath.Join(m.root, "deployments.json"), string(encoded), true)
}

// Panel originals stay in the certificate center. Only immutable copies owned
// by the retired instance are candidates; native listeners and held rollback
// frontends continue to protect the pair during a mode change.
func (m *tlsFrontendManager) cleanupPanelFrontendCopies(deployments, retired tlsDeployments) error {
	keep := map[string]bool{}
	for _, d := range deployments {
		for _, f := range tlsDeploymentReferences(d) {
			keep[f.CertificatePath], keep[f.PrivateKeyPath] = true, true
		}
	}
	for service, d := range retired {
		engine, spec, ok := managedSpecForService(service, m.services.Kind())
		if !ok {
			continue
		}
		var candidates []string
		root := filepath.Join(filepath.Dir(spec.ConfigPath), "panel-certificates")
		for _, f := range d.Frontends {
			rel, err := filepath.Rel(root, f.CertificatePath)
			parts := strings.Split(rel, string(filepath.Separator))
			if err != nil || len(parts) != 3 || !certificateIDPattern.MatchString(parts[0]) || len(parts[1]) < 2 || parts[1][0] != 'v' || parts[2] != "cert.pem" || f.PrivateKeyPath != filepath.Join(filepath.Dir(f.CertificatePath), "key.pem") {
				continue
			}
			valid := true
			for _, c := range parts[1][1:] {
				valid = valid && c >= '0' && c <= '9'
			}
			if valid {
				candidates = append(candidates, f.CertificatePath)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		content, err := readConfigurationFile(spec.ConfigPath)
		if err == nil {
			_, err = serverconfig.MapCertificateFiles(engine, content, func(path string) (string, error) { keep[path] = true; return path, nil })
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, path := range candidates {
			if keep[path] || keep[filepath.Join(filepath.Dir(path), "key.pem")] {
				continue
			}
			if err := removeTLSFiles(filepath.Dir(filepath.Dir(path)), filepath.Base(filepath.Dir(path))); err != nil {
				return err
			}
		}
	}
	return nil
}

// Caddy stores one certificate bundle per subject under each issuer. Restrict
// deletion to exact retired subjects, retaining shared subjects across owners.
func (m *tlsFrontendManager) cleanupCaddyDomains(retired tlsDeployments, keep map[string]bool) error {
	domains := map[string]bool{}
	for _, d := range retired {
		for _, f := range d.Frontends {
			if domain := strings.ToLower(f.Domain); !keep[domain] {
				domains[domain] = true
			}
		}
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(m.state)); err != nil {
		return err
	}
	root, err := os.OpenRoot(m.state)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	var remove []string
	visited := 0
	err = fs.WalkDir(root.FS(), "caddy/certificates", func(name string, entry fs.DirEntry, walkErr error) error {
		visited++
		if visited > 4096 {
			return errors.New("受管证书目录超过清理扫描限制")
		}
		if errors.Is(walkErr, os.ErrNotExist) && name == "caddy/certificates" {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && domains[entry.Name()] {
			remove = append(remove, name)
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, name := range remove {
		if err = root.RemoveAll(name); err != nil {
			return err
		}
	}
	return nil
}

// A preinstalled or subsequently replaced binary belongs to its operator.
func removeOwnedCaddyBinary() error { return removeOwnedCaddyBinaryAt(managedCaddyBinary) }

func removeOwnedCaddyBinaryAt(binary string) error {
	marker := binary + ".qch-owned"
	info, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != 64 {
		return errors.New("不安全的 Caddy 归属记录")
	}
	if err = validateOwner(info, "Caddy ownership"); err != nil {
		return err
	}
	owned, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	value, err := os.ReadFile(binary)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && fmt.Sprintf("%x", sha256.Sum256(value)) == string(owned) {
		if err = validatePrivilegedExecutable(binary); err != nil {
			return err
		}
		if err = os.Remove(binary); err != nil {
			return err
		}
	}
	return os.Remove(marker)
}
