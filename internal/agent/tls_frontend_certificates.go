package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (m *tlsFrontendManager) hasValidCertificate(domain string) bool {
	_, _, err := m.storedCertificate(domain)
	return err == nil
}

// Use the newest unexpired, hostname-verified chain. Caddy may retain an older
// issuer's certificate after failover; a storage filename alone is not trust.
func (m *tlsFrontendManager) storedCertificate(domain string, roots ...*x509.CertPool) (string, string, error) {
	root, err := os.OpenRoot(filepath.Join(m.state, "caddy", "certificates"))
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	var certificate, privateKey string
	var newest time.Time
	visited := 0
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		visited++
		if visited > 4096 {
			return fmt.Errorf("受管证书目录超过扫描限制")
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(path, ".crt") {
			return nil
		}
		read := func(name string) ([]byte, error) {
			file, err := root.Open(name)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
				return nil, fmt.Errorf("无效证书文件")
			}
			return io.ReadAll(io.LimitReader(file, 1<<20))
		}
		cert, err := read(path)
		if err != nil {
			return nil
		}
		key, err := read(strings.TrimSuffix(path, ".crt") + ".key")
		if err != nil {
			return nil
		}
		pair, err := tls.X509KeyPair(cert, key)
		if err != nil || len(pair.Certificate) == 0 {
			return nil
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil || !leaf.NotAfter.After(newest) {
			return nil
		}
		options := x509.VerifyOptions{DNSName: domain, Intermediates: x509.NewCertPool()}
		if len(roots) > 0 {
			options.Roots = roots[0]
		}
		for _, data := range pair.Certificate[1:] {
			if c, err := x509.ParseCertificate(data); err == nil {
				options.Intermediates.AddCert(c)
			}
		}
		if _, err = leaf.Verify(options); err == nil {
			certificate, privateKey, newest = string(cert), string(key), leaf.NotAfter
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	if certificate == "" {
		return "", "", fmt.Errorf("%s 尚无有效公开可信证书", domain)
	}
	return certificate, privateKey, nil
}

// While obtaining certificates, retain every old route (including this user's
// route). New HTTPS listeners wait until application. Business config is untouched.
func certificatePreparationRoutes(content string, previous []core.TLSFrontend, state string) (string, error) {
	old, err := renderCaddyFrontends(previous, state)
	if err != nil {
		return "", err
	}
	var nextRoot, oldRoot map[string]any
	if err = json.Unmarshal([]byte(content), &nextRoot); err != nil {
		return "", err
	}
	if err = json.Unmarshal([]byte(old), &oldRoot); err != nil {
		return "", err
	}
	servers := func(root map[string]any) map[string]any {
		return root["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	}
	// HTTP-01 does not need a new HTTPS listener. Reuse only committed
	// Caddy listeners so an outgoing Nginx can keep serving the same port.
	nextRoot["apps"].(map[string]any)["http"].(map[string]any)["servers"] = servers(oldRoot)
	data, err := json.MarshalIndent(nextRoot, "", "  ")
	return string(data), err
}

func (m *tlsFrontendManager) prepareCertificates(ctx context.Context, previous, next tlsDeployments, service string) error {
	needed := false
	for _, f := range next[service].Frontends {
		needed = needed || f.UsesACME() && !m.hasValidCertificate(f.Domain)
	}
	if !needed {
		return nil
	}
	snapshots, err := m.snapshotRuntime(ctx)
	if err != nil {
		return err
	}
	started := time.Now()
	err = m.syncWithFallback(ctx, next, nil, previous)
	if err == nil {
		err = m.waitReady(ctx, next, service, previous)
	}
	if err != nil {
		for _, f := range next[service].Frontends {
			if f.UsesACME() {
				err = m.withFrontendDiagnostics(err, f, started)
				break
			}
		}
	}
	// Certificates/accounts remain in storage; routes and service state return
	// to the committed manifest even when issuance or the parent deadline fails.
	recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	restoreErr := m.restoreRuntime(recovery, previous, snapshots)
	if restoreErr != nil {
		return fmt.Errorf("[阶段:证书准备] 原始失败：%v；反代恢复失败：%w", err, restoreErr)
	}
	if err != nil {
		return fmt.Errorf("[阶段:证书准备] %w；已恢复原反代状态；业务配置与服务未替换", err)
	}
	return nil
}

func (m *tlsFrontendManager) prepareStagedCertificates(ctx context.Context, service string, frontends []core.TLSFrontend) error {
	tlsFrontendMutex.Lock()
	defer tlsFrontendMutex.Unlock()
	previous, err := m.load()
	if err != nil {
		return err
	}
	next := tlsDeployments{}
	for name, deployment := range previous {
		next[name] = deployment
	}
	next[service] = tlsDeployment{Frontends: frontends}
	if _, err := activeTLSFrontends(next); err != nil {
		return err
	}
	if err := m.prepareCertificates(ctx, previous, next, service); err != nil {
		return err
	}
	active, err := activeTLSFrontends(next)
	if err != nil {
		return err
	}
	if _, err = m.copyAutomaticNginxCertificates(active, false); err != nil {
		return err
	}
	return m.validateConfigs(ctx, active)
}

func (m *tlsFrontendManager) waitStoredCertificate(ctx context.Context, domain string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if m.hasValidCertificate(domain) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s 证书尚未写入受管 storage：%w", domain, ctx.Err())
		case <-ticker.C:
		}
	}
}
