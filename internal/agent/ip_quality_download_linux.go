//go:build linux

package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/githubfetch"
)

const ipQualityDownloadLimit = 1 << 20

func ipQualityHTTPClient() *http.Client {
	// Keep task credentials, environment proxies and user curl configuration out
	// of the public detector download channel. Apply IPv6 policy on redirects too.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: ipQualityRootCAs()}
	return githubfetch.Wrap(&http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || !trustedIPQualityURL(req.URL) {
			return errors.New("拒绝跳转到非官方 IPQuality 来源")
		}
		return nil
	}})
}

// Reload after package installation; x509.SystemCertPool caches the initially
// empty root store in a process that started on a minimal host.
func ipQualityRootCAs() *x509.CertPool {
	pool := x509.NewCertPool()
	found := false
	for _, path := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/ca-bundle.pem", "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", "/var/lib/ca-certificates/ca-bundle.pem", "/etc/ssl/cert.pem"} {
		if content, err := os.ReadFile(path); err == nil {
			found = pool.AppendCertsFromPEM(content) || found
		}
	}
	if !found {
		return nil
	}
	return pool
}

func trustedIPQualityURL(value *url.URL) bool {
	if value == nil || value.Scheme != "https" || value.User != nil || value.Port() != "" || value.RawQuery != "" || value.Fragment != "" {
		return false
	}
	if strings.EqualFold(value.Host, "ip.check.place") && value.Path == "" {
		return true
	}
	if strings.EqualFold(value.Host, "ip.check.place") && value.Path == "/" {
		return true
	}
	if value.Host == "raw.githubusercontent.com" {
		return strings.HasPrefix(value.Path, "/xykt/IPQuality/main/")
	}
	if value.Host == "github.com" {
		return strings.HasPrefix(value.Path, "/xykt/IPQuality/raw/main/") || strings.HasPrefix(value.Path, "/xykt/IPQuality/raw/refs/heads/main/")
	}
	return false
}

// fetchIPQualityContent verifies status, bounds, transport completeness and
// content before execution. Upstream provides no signed digest for this moving
// script: HTTPS/source checks and a recorded SHA-256 are not a signature.
func fetchIPQualityContent(ctx context.Context, client *http.Client, source string) ([]byte, error) {
	parsed, err := url.Parse(source)
	if err != nil || !trustedIPQualityURL(parsed) {
		return nil, errors.New("拒绝非官方 IPQuality 下载来源")
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		request, _ := http.NewRequestWithContext(attemptCtx, http.MethodGet, source, nil)
		request.Header.Set("User-Agent", "QControlHub-Agent")
		response, requestErr := client.Do(request)
		retry := true
		var content []byte
		if requestErr != nil {
			lastErr = requestErr
		} else {
			if response.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("HTTP %d", response.StatusCode)
				retry = response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode >= 500
			} else {
				content, lastErr = io.ReadAll(io.LimitReader(response.Body, ipQualityDownloadLimit+1))
				if lastErr == nil && response.ContentLength >= 0 && int64(len(content)) != response.ContentLength {
					lastErr = errors.New("下载长度不完整")
				}
				if lastErr == nil && (len(content) == 0 || len(content) > ipQualityDownloadLimit || !utf8.Valid(content)) {
					lastErr, retry = errors.New("下载为空、过大或非 UTF-8 内容"), false
				}
				if lastErr == nil && (strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") || bytes.HasPrefix(bytes.TrimSpace(content), []byte("<"))) {
					lastErr, retry = errors.New("下载返回 HTML/错误页面"), false
				}
			}
			response.Body.Close()
		}
		cancel()
		if lastErr == nil {
			return content, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !retry || attempt == 2 {
			break
		}
		if err := waitIPQualityRetry(ctx, time.Duration(attempt+1)*time.Second); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("下载 %s 失败：%s；检查此主机的 DNS/HTTPS/IPv4/IPv6 连通性后重试", parsed.Hostname(), sanitizeIPQualityDiagnostic(lastErr.Error()))
}

func downloadIPQualityScript(ctx context.Context, directory string, progress func(string)) error {
	content, err := fetchIPQualityContent(ctx, ipQualityHTTPClient(), ipQualityScriptURL)
	if err != nil {
		return fmt.Errorf("下载脚本：%w", err)
	}
	if !bytes.HasPrefix(content, []byte("#!/bin/bash\n")) || !bytes.Contains(content, []byte("script_version=")) || !bytes.Contains(content, []byte("install_dependencies()")) {
		return errors.New("下载脚本：内容不是预期的官方 IPQuality Bash 脚本；请检查上游是否变更")
	}
	path := filepath.Join(directory, "detector.sh")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("下载脚本：写入失败：%w", err)
	}
	bash, err := findIPQualityExecutable("bash")
	if err != nil {
		return err
	}
	output, err := runIPQualityCommand(ctx, bash, []string{"--noprofile", "--norc", "-n", path})
	if err != nil {
		return fmt.Errorf("下载脚本：Bash 完整性/语法校验失败；退出状态 %v；%s", err, sanitizeIPQualityDiagnostic(output))
	}
	progress(fmt.Sprintf("下载脚本：HTTP/内容/语法校验完成；SHA-256=%x", sha256.Sum256(content)))
	return nil
}

// FetchIPQualityResource serves upstream's public reference downloads through
// the same Go transport as the main script. Convert its fixed CDN fallback to
// the original official Raw source so every network uses the project policy.
func FetchIPQualityResource(ctx context.Context, source string) ([]byte, error) {
	original, err := ipQualityReferenceSource(source)
	if err != nil {
		return nil, fmt.Errorf("下载检测资源：%w", err)
	}
	content, err := fetchIPQualityContent(ctx, ipQualityHTTPClient(), original)
	if err != nil {
		return nil, fmt.Errorf("下载检测资源：%w", err)
	}
	if strings.HasSuffix(original, ".json") && !json.Valid(content) {
		return nil, errors.New("下载检测资源：损坏的 JSON")
	}
	return content, nil
}

func ipQualityReferenceSource(source string) (string, error) {
	if strings.ContainsAny(source, "\\ \t\r\n") {
		return "", errors.New("不安全的资源 URL")
	}
	source = strings.Replace(source, "https://testingcf.jsdelivr.net/gh/xykt/IPQuality@", "https://raw.githubusercontent.com/xykt/IPQuality/", 1)
	source = strings.Replace(source, "https://github.com/xykt/IPQuality/raw/", "https://raw.githubusercontent.com/xykt/IPQuality/", 1)
	parsed, err := url.Parse(source)
	if err != nil || !trustedIPQualityURL(parsed) || parsed.Host != "raw.githubusercontent.com" || !strings.HasPrefix(parsed.Path, "/xykt/IPQuality/main/ref/") || strings.Contains(parsed.Path, "..") {
		return "", errors.New("拒绝非官方 IPQuality 参考资源")
	}
	return source, nil
}

func ipQualityResourceWrapper(executable string) string {
	// Upstream currently uses GET for these public references. Detection API
	// calls retain curl's own address-family and request flags. Do not export
	// this wrapper to unrelated package manager processes.
	return `curl() {
  local argument
  for argument in "$@"; do
    case "$argument" in
      https://github.com/xykt/IPQuality/raw/*|https://raw.githubusercontent.com/xykt/IPQuality/*|https://testingcf.jsdelivr.net/gh/xykt/IPQuality@*)
        ` + "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'" + ` fetch-ip-quality-resource "$argument"
        return $?
        ;;
    esac
  done
  command curl -q --show-error --connect-timeout 5 --max-time 20 "$@"
}
export -f curl
`
}
