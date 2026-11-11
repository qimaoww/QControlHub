package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

type TLSFrontendConflict struct {
	Reason string
	Port   int
	Detail string
}

func (e *TLSFrontendConflict) Error() string { return e.Detail }
func frontendConflict(reason string, port int, format string, args ...any) error {
	return &TLSFrontendConflict{reason, port, fmt.Sprintf(format, args...)}
}

// ValidateTLSFrontendRoutes is shared by scheduling and runtime rendering.
// Paths remain unique across a port, including across different SNI names.
func ValidateTLSFrontendRoutes(result []TLSFrontend) error {
	owners := map[int]string{}
	localPorts := map[int]bool{}
	localBackends := map[int]bool{}
	paths := map[string]bool{}
	backends := map[int]bool{}
	siteOwners := map[string]string{}
	var routes []TLSFrontend
	emails := map[string]string{}
	certificateModes := map[string]bool{}
	panelCertificates := map[string]string{}
	for _, f := range result {
		service := f.StaticPageOwner
		if err := f.Validate(); err != nil {
			return err
		}
		if owners[f.PublicPort] != "" && (f.LocalOnly || localPorts[f.PublicPort]) {
			return frontendConflict("backend_port_conflict", f.PublicPort, "本地 TLS 端口 %d 已被其他入口占用", f.PublicPort)
		}
		if backends[f.BackendPort] && (f.LocalOnly || localBackends[f.BackendPort]) {
			return frontendConflict("backend_port_conflict", f.PublicPort, "组合内部端口 %d 已被其他入站占用", f.BackendPort)
		}
		localPorts[f.PublicPort] = localPorts[f.PublicPort] || f.LocalOnly
		localBackends[f.BackendPort] = localBackends[f.BackendPort] || f.LocalOnly
		if f.UsesACME() && f.ACMEEmail != "" {
			if email := emails[strings.ToLower(f.Domain)]; email != "" && email != f.ACMEEmail {
				return frontendConflict("tls_certificate_conflict", f.PublicPort, "域名 %s 的 ACME 邮箱不一致，请使用同一证书账户", strings.ToLower(f.Domain))
			}
			emails[strings.ToLower(f.Domain)] = f.ACMEEmail
		}
		if mode := owners[f.PublicPort]; mode != "" && mode != f.Mode {
			return frontendConflict("proxy_type_conflict", f.PublicPort, "公网端口 %d 已用于 %s，不能同时用于 %s", f.PublicPort, mode, f.Mode)
		}
		if f.Mode == "nginx" {
			identity := strconv.Itoa(f.PublicPort) + "/" + strings.ToLower(f.Domain)
			if automatic, exists := certificateModes[identity]; exists && automatic != f.AutomaticCertificate {
				return frontendConflict("tls_certificate_conflict", f.PublicPort, "域名 %s 在端口 %d 上的 Nginx 证书方式不一致，请统一为自动证书或使用不同域名", strings.ToLower(f.Domain), f.PublicPort)
			}
			certificateModes[identity] = f.AutomaticCertificate
		}
		// Caddy selects certificates by SNI across all its listeners. Nginx
		// also needs one unambiguous pair per virtual host. Immutable copies
		// in different instances may refer to the same certificate version.
		identity := f.Mode + "/" + strings.ToLower(f.Domain)
		if f.Mode == "nginx" {
			identity += "/" + strconv.Itoa(f.PublicPort)
		}
		certificate := "automatic"
		if f.Mode == "nginx" && !f.UsesACME() && !f.UsesPanelCertificate() && !f.LocalOnly {
			sum := sha256.Sum256([]byte(f.Certificate + f.PrivateKey))
			certificate = hex.EncodeToString(sum[:])
		}
		if f.UsesPanelCertificate() {
			certificate = strings.Split(strings.SplitN(f.CertificatePath, "/panel-certificates/", 2)[1], "/")[0]
		}
		if previous, exists := panelCertificates[identity]; exists && previous != certificate {
			return frontendConflict("tls_certificate_conflict", f.PublicPort, "域名 %s 的反代证书不一致，请选择同一面板证书", strings.ToLower(f.Domain))
		}
		panelCertificates[identity] = certificate
		siteKey := fmt.Sprintf("%d/%s", f.PublicPort, strings.ToLower(f.Domain))
		if f.StaticHTML != "" {
			if owner := siteOwners[siteKey]; owner != "" && owner != service+"/"+f.Tag {
				return frontendConflict("static_page_conflict", f.PublicPort, "同域名静态页面已由其他入站维护")
			}
			siteOwners[siteKey] = service + "/" + f.Tag
		}
		for _, old := range routes {
			if old.PublicPort == f.PublicPort && (old.Transport == "xhttp" || f.Transport == "xhttp") {
				a, b := strings.TrimSuffix(old.Path, "/")+"/", strings.TrimSuffix(f.Path, "/")+"/"
				if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
					return frontendConflict("route_conflict", f.PublicPort, "XHTTP 路径与其他入站重叠")
				}
			}
		}
		routes = append(routes, f)
		route := strconv.Itoa(f.PublicPort) + f.Path
		if paths[route] {
			return frontendConflict("route_conflict", f.PublicPort, "公网端口 %d 上的路径 %s 已被其他入站使用", f.PublicPort, f.Path)
		}
		owners[f.PublicPort] = f.Mode
		paths[route] = true
		backends[f.BackendPort] = true
	}
	for port := range owners {
		if backends[port] {
			return frontendConflict("backend_port_conflict", port, "公网反代端口 %d 与内部端口冲突", port)
		}
	}
	for _, f := range result {
		if f.UsesACME() && owners[80] != "" {
			return frontendConflict("backend_port_conflict", 80, "端口 80 已用于 TLS 反代，Caddy HTTP 证书验证无法使用")
		}
	}
	return nil
}
