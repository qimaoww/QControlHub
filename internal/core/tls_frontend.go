package core

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

const AgentFeatureManagedTLS = "managed-tls-v1"

// TLSFrontend is delivered only in the encrypted execution snapshot. PrivateKey
// must never be returned by panel task APIs or included in client exports.
type TLSFrontend struct {
	Tag         string `json:"tag"`
	Mode        string `json:"mode"`
	Domain      string `json:"domain"`
	PublicPort  int    `json:"public_port"`
	BackendPort int    `json:"backend_port"`
	Transport   string `json:"transport"`
	Path        string `json:"path"`
	Certificate string `json:"certificate,omitempty"`
	PrivateKey  string `json:"private_key,omitempty"`
}

func (f TLSFrontend) Validate() error {
	if f.Mode != "caddy" && f.Mode != "nginx" {
		return fmt.Errorf("unsupported managed TLS provider %q", f.Mode)
	}
	if f.PublicPort < 1 || f.PublicPort > 65535 || f.BackendPort < 1 || f.BackendPort > 65535 || f.PublicPort == f.BackendPort {
		return fmt.Errorf("公网端口与内部端口必须是不同的有效端口")
	}
	if f.Tag == "" || f.Domain == "" || len(f.Domain) > 253 || strings.ContainsAny(f.Domain, " \t\r\n/:;{}\"'`$\\") && net.ParseIP(f.Domain) == nil {
		return fmt.Errorf("TLS 域名无效")
	}
	for _, label := range strings.Split(f.Domain, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("TLS 域名无效")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return fmt.Errorf("TLS 域名仅支持 ASCII 域名或 IPv4 地址")
			}
		}
	}
	if f.Mode == "caddy" && f.PublicPort == 80 {
		return fmt.Errorf("Caddy 自动证书需要独立 HTTP 验证端口 80，请使用其他公网 HTTPS 端口")
	}
	if f.Mode == "caddy" && (net.ParseIP(f.Domain) != nil || !strings.Contains(f.Domain, ".")) {
		return fmt.Errorf("Caddy 自动证书需要指向节点的公网域名")
	}
	if f.Transport != "websocket" && f.Transport != "httpupgrade" {
		return fmt.Errorf("托管 TLS 反代当前支持 WebSocket 和 HTTPUpgrade；其他传输请选择原生 TLS")
	}
	if !strings.HasPrefix(f.Path, "/") || len(f.Path) > 256 || strings.ContainsAny(f.Path, "\r\n\t \"'`$\\{};#?*%") {
		return fmt.Errorf("反代路径必须以 / 开头，不得包含查询参数、空白或配置控制字符")
	}
	if f.Mode == "nginx" && (f.Certificate == "" || f.PrivateKey == "") {
		return fmt.Errorf("Nginx 托管证书缺少证书或私钥")
	}
	return nil
}

type executionSnapshot struct {
	Version   int           `json:"qcontrolhub_deployment"`
	Content   string        `json:"content"`
	Frontends []TLSFrontend `json:"tls_frontends"`
}

func EncodeExecutionSnapshot(content string, frontends []TLSFrontend) (string, error) {
	if content == "" && len(frontends) == 0 {
		return "", nil
	}
	value, err := json.Marshal(executionSnapshot{1, content, frontends})
	return "qcontrolhub-deployment-v1\n" + string(value), err
}

func DecodeExecutionSnapshot(content string) (string, []TLSFrontend, error) {
	const prefix = "qcontrolhub-deployment-v1\n"
	if !strings.HasPrefix(content, prefix) {
		return content, nil, nil
	}
	content = strings.TrimPrefix(content, prefix)
	var snapshot executionSnapshot
	if err := json.Unmarshal([]byte(content), &snapshot); err != nil {
		return "", nil, err
	}
	if snapshot.Version != 1 {
		return "", nil, fmt.Errorf("unsupported deployment snapshot version")
	}
	for _, f := range snapshot.Frontends {
		if err := f.Validate(); err != nil {
			return "", nil, err
		}
	}
	return snapshot.Content, snapshot.Frontends, nil
}
