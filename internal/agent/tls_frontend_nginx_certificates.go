package agent

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Older managed Nginx configurations may own the optional panel HTTP-01
// listener. Hand only that listener to Caddy, retaining committed HTTPS routes.
// The caller's runtime snapshot restores it if certificate preparation fails.
func (m *tlsFrontendManager) releaseNginxChallengePort(ctx context.Context, routes []core.TLSFrontend) error {
	path := filepath.Join(m.root, "nginx.conf")
	stored, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(stored), "# qch-managed-http-challenges-v1") {
		return nil
	}
	service := "qagent-tls-nginx"
	if m.services.Kind() == ServiceManagerSystemd {
		service += ".service"
	}
	status, _ := m.services.status(ctx, service)
	if status != "active" {
		return nil
	}
	if err = m.writeConfig(path, renderNginxFrontends(routes, m.root, m.state), false); err != nil {
		return err
	}
	if _, err = run(ctx, managedNginxBinary(), "-p", m.state+"/", "-c", path, "-s", "reload"); err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		listener, err := net.Listen("tcp", ":80")
		if err == nil {
			listener.Close()
			return nil
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("Nginx 未释放 HTTP 验证端口 80：%w", wait.Err())
		case <-ticker.C:
		}
	}
}

type nginxCertificateFile struct {
	path, previous, next string
	existed              bool
}

func (m *tlsFrontendManager) restoreNginxCertificates(files []nginxCertificateFile) error {
	var result error
	for _, file := range files {
		var err error
		if file.existed {
			err = m.writeConfig(file.path, file.previous, false)
		} else {
			err = os.Remove(file.path)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		}
		result = errors.Join(result, err)
	}
	return result
}

// Certificate material stays on the node. Never put ACME private keys in the
// panel metadata or copy an unverified/incomplete storage pair to Nginx.
func (m *tlsFrontendManager) copyAutomaticNginxCertificates(frontends []core.TLSFrontend, allowMissing bool, roots ...*x509.CertPool) ([]nginxCertificateFile, error) {
	var files []nginxCertificateFile
	seen := map[string]bool{}
	for _, f := range frontends {
		if !f.AutomaticCertificate || seen[f.Domain] {
			continue
		}
		seen[f.Domain] = true
		certificate, key, err := m.storedCertificate(f.Domain, roots...)
		if err != nil {
			if allowMissing {
				continue
			}
			return nil, err
		}
		for suffix, content := range map[string]string{".crt": certificate, ".key": key} {
			path := filepath.Join(m.root, frontendKey(f)+suffix)
			old, err := os.ReadFile(path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			if string(old) != content {
				files = append(files, nginxCertificateFile{path, string(old), content, err == nil})
			}
		}
	}
	for _, file := range files {
		if err := m.writeConfig(file.path, file.next, false); err != nil {
			return nil, errors.Join(err, m.restoreNginxCertificates(files))
		}
	}
	return files, nil
}

// Caddy renews independently of panel connectivity. The Agent periodically
// installs verified renewals and gracefully reloads only an active Nginx.
func (m *tlsFrontendManager) refreshNginxCertificates(ctx context.Context, deployments tlsDeployments, roots ...*x509.CertPool) error {
	active, err := activeTLSFrontends(deployments)
	if err != nil {
		return err
	}
	needed := false
	for _, f := range active {
		needed = needed || f.AutomaticCertificate
	}
	if !needed {
		return nil
	}
	service := "qagent-tls-nginx"
	if m.services.Kind() == ServiceManagerSystemd {
		service += ".service"
	}
	status, _ := m.services.status(ctx, service)
	if status != "active" {
		return nil
	}
	files, err := m.copyAutomaticNginxCertificates(active, false, roots...)
	if err != nil || len(files) == 0 {
		return err
	}
	reload := func(ctx context.Context) error {
		_, err := run(ctx, managedNginxBinary(), "-p", m.state+"/", "-c", filepath.Join(m.root, "nginx.conf"), "-s", "reload")
		return err
	}
	identity, identityErr := managedCoreServiceIdentity()
	err = identityErr
	if err == nil {
		_, err = runInDirectoryWithEnvironmentAndIdentity(ctx, m.state, "", managedNginxBinary(), &identity, "-t", "-p", m.state+"/", "-c", filepath.Join(m.root, "nginx.conf"))
	}
	if err == nil {
		err = reload(ctx)
	}
	if err != nil {
		recovery, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		restoreErr := m.restoreNginxCertificates(files)
		reloadErr := reload(recovery)
		return fmt.Errorf("Nginx 续期证书加载失败，已尝试恢复原证书：%w", errors.Join(err, restoreErr, reloadErr))
	}
	return nil
}
