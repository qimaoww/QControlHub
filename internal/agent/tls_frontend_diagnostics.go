package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const letsEncryptDirectory = "https://acme-v02.api.letsencrypt.org/directory"
const zeroSSLDirectory = "https://acme.zerossl.com/v2/DV90"

func (m *tlsFrontendManager) withFrontendDiagnostics(cause error, frontend core.TLSFrontend, since time.Time) error {
	if frontend.Mode != "caddy" {
		return cause
	}
	// The deployment deadline may already have expired. Capture the actual
	// issuer failure before rollback stops Caddy, with a separate short budget.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	log := caddyFailureLog(m.state, since)
	if log == "" && m.services.Kind() == ServiceManagerSystemd {
		value, _ := run(ctx, journalctlPath, "--unit=qagent-tls-caddy.service", "--since="+since.UTC().Format(time.RFC3339), "--lines=50", "--no-pager", "--output=cat")
		log = summarizeCaddyFailure(value, since)
	}
	if log == "" {
		log = "未取得本次签发日志；查看 /var/lib/qcontrolhub-tls/caddy.log"
	}
	account := "已启用 Let’s Encrypt → ZeroSSL 回退"
	if frontend.ACMEEmail == "" {
		account = "未设置 ACME 邮箱，ZeroSSL 无法自动绑定账户；请在预设中填写邮箱后重试"
	}
	return fmt.Errorf("%w；%s；证书机构日志：\n%s", cause, account, log)
}

func caddyFailureLog(state string, since time.Time) string {
	root, err := os.OpenRoot(state)
	if err != nil {
		return ""
	}
	defer root.Close()
	info, err := root.Lstat("caddy.log")
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	file, err := root.Open("caddy.log")
	if err != nil {
		return ""
	}
	defer file.Close()
	if info.Size() > 64<<10 {
		if _, err = file.Seek(-(64 << 10), io.SeekEnd); err != nil {
			return ""
		}
	}
	data, _ := io.ReadAll(io.LimitReader(file, 64<<10))
	return summarizeCaddyFailure(string(data), since)
}

func summarizeCaddyFailure(value string, since time.Time) string {
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		var entry struct {
			Level      string  `json:"level"`
			Time       float64 `json:"ts"`
			Message    string  `json:"msg"`
			Error      string  `json:"error"`
			Identifier string  `json:"identifier"`
			Issuer     string  `json:"issuer"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || (entry.Level != "error" && entry.Level != "warn") || entry.Time < float64(since.UnixMilli())/1000 {
			continue
		}
		message := entry.Message
		if entry.Identifier != "" {
			message += " (" + entry.Identifier + ")"
		}
		if entry.Issuer != "" {
			message += " [" + entry.Issuer + "]"
		}
		if entry.Error != "" {
			message += ": " + entry.Error
		}
		lines = append(lines, message)
	}
	output := strings.Join(lines, "\n")
	if len(output) > 6000 {
		output = strings.ToValidUTF8(output[len(output)-6000:], "�")
	}
	return output
}
