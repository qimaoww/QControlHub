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
		account = "未设置 ACME 邮箱，ZeroSSL 无法自动绑定账户；请在系统设置的证书申请或预设中填写邮箱后重试"
	}
	var errorsOnly []string
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "[错误]") {
			errorsOnly = append(errorsOnly, line)
		}
	}
	reason, remedy := classifyTLSFailure(strings.Join(errorsOnly, "\n") + "\n" + cause.Error())
	return fmt.Errorf("%w；[原因:%s] %s；%s；证书机构日志：\n%s", cause, reason, remedy, account, log)
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
		label := "[错误] "
		if entry.Level == "warn" {
			label = "[告警] "
		}
		message := label + entry.Message
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

func classifyTLSFailure(value string) (string, string) {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "permission denied"), strings.Contains(value, "read-only file system"):
		return "存储权限", "升级 Agent 修复受管目录；检查服务账号和 XDG 目录权限"
	case strings.Contains(value, "ratelimited"), strings.Contains(value, "rate limit"), strings.Contains(value, "too many requests"):
		return "机构限流", "按机构 Retry-After 等待，避免反复提交；可使用已有证书或外部反代"
	case strings.Contains(value, "no such host"), strings.Contains(value, "nxdomain"), strings.Contains(value, "dns problem"):
		return "DNS", "检查域名 A/AAAA 及节点 DNS；IPv4/IPv6 记录均应指向可提供挑战的入口"
	case strings.Contains(value, "timeout"), strings.Contains(value, "timed out"), strings.Contains(value, "deadline exceeded"):
		return "网络超时", "检查节点到 ACME 的 DNS/TCP 443/TLS；核对公网 TCP 80 挑战、CDN 回源和 NAT"
	case strings.Contains(value, "eab"), strings.Contains(value, "account"), strings.Contains(value, "email"):
		return "证书账户", "检查账号默认邮箱或预设覆盖值及 ZeroSSL EAB 绑定条件"
	case strings.Contains(value, "challenge"), strings.Contains(value, "unauthorized"), strings.Contains(value, "connection refused"):
		return "挑战失败", "检查 A/AAAA、公网 TCP 80、防火墙、CDN 和反代回源；可改用已有证书或外部反代"
	default:
		return "TLS未就绪", "查看本次机构错误，检查证书域名、信任和代理路径"
	}
}
