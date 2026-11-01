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

// Older automatic Nginx deployments used Caddy only for certificates. Retire
// that helper on upgrade too, without starting a stopped frontend or touching
// a Caddy still serving another active managed inbound.
func (m *tlsFrontendManager) retireCaddyCertificateHelper(ctx context.Context, deployments tlsDeployments) error {
	active, err := activeTLSFrontends(deployments)
	if err != nil {
		return err
	}
	needed := false
	for _, f := range active {
		if f.Mode == "caddy" {
			return nil
		}
		needed = needed || f.AutomaticCertificate
	}
	if !needed {
		return nil
	}
	service, unit, _ := m.serviceDefinition("caddy", false)
	if _, err = os.Stat(unit); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	status, err := m.services.status(ctx, service)
	if err != nil {
		return err
	}
	if status == "inactive" {
		return setServiceEnabled(ctx, service, false, m.services)
	}
	snapshots, err := m.snapshotRuntime(ctx)
	if err != nil {
		return err
	}
	if err = m.ensureNginxCertificates(ctx, active, false); err != nil {
		return err
	}
	if _, err = m.services.command(ctx, service, core.ActionStop); err == nil {
		err = setServiceEnabled(ctx, service, false, m.services)
	}
	if err == nil {
		err = m.upgradeHTTPChallengeRoutes(ctx, deployments)
	}
	if err != nil {
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return errors.Join(err, m.restoreRuntime(recovery, deployments, snapshots))
	}
	return nil
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
		certificate, key, err := m.storedNginxCertificate(f.Domain, roots...)
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

// Validation must leave the installed pair unchanged. Otherwise a later
// renewal check can mistake staged files for a certificate already reloaded
// by the running Nginx workers.
func (m *tlsFrontendManager) withAutomaticNginxCertificates(frontends []core.TLSFrontend, allowMissing bool, validate func() error, roots ...*x509.CertPool) (result error) {
	files, err := m.copyAutomaticNginxCertificates(frontends, allowMissing, roots...)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, m.restoreNginxCertificates(files)) }()
	return validate()
}

// The Agent issues/renews independently of panel connectivity and Caddy, then
// installs verified renewals and gracefully reloads only an active Nginx.
func (m *tlsFrontendManager) refreshNginxCertificates(ctx context.Context, deployments tlsDeployments, overrides ...nginxACMEOptions) error {
	options := nginxACMEDefaults()
	if len(overrides) > 0 {
		options = overrides[0]
	}
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
	// A failed renewal must not prevent another domain's pending reload.
	renewalErr := m.ensureNginxCertificates(ctx, active, true, options)
	files, err := m.copyAutomaticNginxCertificates(active, false, options.roots)
	if err != nil || len(files) == 0 {
		return errors.Join(renewalErr, err)
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
	return renewalErr
}
