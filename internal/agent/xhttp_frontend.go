package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func renderNginxXHTTP(body *strings.Builder, f core.TLSFrontend) {
	// The owned target is loopback only. Reality forwards unauthenticated TLS
	// here; authenticated plaintext HTTP falls back to the same XHTTP backend.
	fmt.Fprintf(body, " server {\n  listen 127.0.0.1:%d ssl http2;\n  server_name %s %s;\n  ssl_protocols TLSv1.2 TLSv1.3;\n  ssl_certificate %s;\n  ssl_certificate_key %s;\n  client_max_body_size 0;\n  location ^~ %s {\n   grpc_pass grpc://127.0.0.1:%d;\n   grpc_set_header Host $http_host;\n   grpc_set_header X-Forwarded-For $remote_addr;\n   grpc_socket_keepalive on;\n   grpc_read_timeout 86400s;\n   grpc_send_timeout 86400s;\n  }\n  location / { return 404; }\n }\n", f.PublicPort, f.Domain, strings.Join(append([]string{f.RealityName}, f.RealityNames...), " "), f.CertificatePath, f.PrivateKeyPath, strings.TrimSuffix(f.Path, "/")+"/", f.BackendPort)
}

func validateXHTTPCertificate(ctx context.Context, f core.TLSFrontend) error {
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return err
	}
	for _, path := range []string{f.CertificatePath, f.PrivateKeyPath} {
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr != nil {
			return fmt.Errorf("XHTTP 组合证书引用 %s：%w", path, resolveErr)
		}
		for _, hidden := range []string{"/root", "/home", "/run/user", "/tmp", "/var/tmp"} {
			if pathWithin(resolved, hidden) {
				return fmt.Errorf("XHTTP 组合证书 %s 位于 ProtectHome / PrivateTmp 隔离目录；请移至 /etc/qcontrolhub/tls 并授权服务组读取", path)
			}
		}
		if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
			return fmt.Errorf("XHTTP 组合证书目录权限 %s：%w", path, err)
		}
		if _, err := runInDirectoryWithEnvironmentAndIdentity(ctx, "", "", "/usr/bin/test", &identity, "-r", path); err != nil {
			return fmt.Errorf("XHTTP 组合证书文件权限：非 root 服务组无法读取 %s：%w", path, err)
		}
	}
	pair, err := tls.LoadX509KeyPair(f.CertificatePath, f.PrivateKeyPath)
	if err != nil {
		return fmt.Errorf("XHTTP 组合证书 / 私钥：%w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	for _, name := range append([]string{f.Domain, f.RealityName}, f.RealityNames...) {
		if err := leaf.VerifyHostname(name); err != nil {
			return fmt.Errorf("组合证书须同时覆盖 TLS SNI 与 Reality 本地目标 SNI (%s)：%w", name, err)
		}
	}
	if now := time.Now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return fmt.Errorf("组合证书尚未生效或已过期，请更新证书")
	}
	info, err := os.Stat(f.PrivateKeyPath)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0007 != 0 {
		return fmt.Errorf("组合私钥对其他用户开放，请改为 0640 并仅授权 qcontrolhub-core 组")
	}
	return nil
}
