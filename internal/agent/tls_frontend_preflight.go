package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

// RunTLSNetworkPreflight runs in a child with the Caddy service identity and
// environment. The services use the host network (no PrivateNetwork setting).
func RunTLSNetworkPreflight(ctx context.Context, domain string, fallback bool) error {
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", domain)
	if err != nil {
		return fmt.Errorf("DNS：%s 的 A/AAAA 查询失败：%w；检查域名解析", domain, err)
	}
	if err = validateTLSAddresses(addresses); err != nil {
		return err
	}
	local, _ := net.InterfaceAddrs()
	fmt.Printf("[信息] DNS %s A/AAAA=%v；节点接口=%v。接口地址与 DNS 不同可能来自 NAT/CDN/外部反代，请核对回源拓扑。\n", domain, addresses, local)
	for _, address := range addresses {
		if netpolicy.IsCloudflareAddress(address) {
			fmt.Println("[告警] 检测到 Cloudflare 代理；HTTP-01 路径必须直达节点 TCP 80，不得缓存或阻止挑战。可改为 DNS only，或使用已有证书和外部反代。")
		}
	}
	client := &http.Client{Timeout: 6 * time.Second, Transport: &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 3 * time.Second,
	}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	endpoints := []string{letsEncryptDirectory}
	if fallback {
		endpoints = append(endpoints, zeroSSLDirectory)
	}
	available := false
	for _, endpoint := range endpoints {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		response, requestErr := client.Do(request)
		if requestErr == nil {
			var directory struct {
				NewNonce   string `json:"newNonce"`
				NewAccount string `json:"newAccount"`
				NewOrder   string `json:"newOrder"`
			}
			requestErr = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&directory)
			response.Body.Close()
			if response.StatusCode != 200 || directory.NewNonce == "" || directory.NewAccount == "" || directory.NewOrder == "" {
				requestErr = fmt.Errorf("无效 ACME directory（HTTP %d）", response.StatusCode)
			}
		}
		if requestErr == nil {
			available = true
			fmt.Printf("[信息] 节点出站 DNS/TCP/TLS/ACME directory 通过：%s\n", endpoint)
		} else {
			fmt.Printf("[告警] 节点出站 ACME %s：%v\n", endpoint, requestErr)
		}
	}
	fmt.Println("[告警] 本机端口检查与节点出站检查不证明公网挑战可达；请放行公网 TCP 80 和 HTTPS 端口，核对 A/AAAA、CDN 回源及 NAT 转发。")
	if !fallback {
		fmt.Println("[信息] 未配置 ACME 邮箱：仅尝试 Let's Encrypt；系统设置可填写账号默认邮箱，预设可覆盖。")
	}
	if !available {
		return fmt.Errorf("ACME 网络检查：所有已启用机构均不可达；检查 DNS、节点出站 TCP 443/TLS，或选择已有证书 / 外部反代")
	}
	return nil
}

func validateTLSAddresses(addresses []netip.Addr) error {
	if len(addresses) == 0 {
		return fmt.Errorf("DNS：域名没有 A/AAAA 记录；先配置公网解析")
	}
	for _, address := range addresses {
		if !netpolicy.IsPublicAddress(address) {
			return fmt.Errorf("DNS：记录 %s 不是公网地址；自动证书需要公网 A/AAAA，可改用手动证书或外部反代", address)
		}
	}
	return nil
}

func (m *tlsFrontendManager) preflight(ctx context.Context, frontends []core.TLSFrontend) error {
	seen := map[string]bool{}
	for _, f := range frontends {
		if f.Mode != "caddy" || seen[f.Domain] {
			continue
		}
		seen[f.Domain] = true
		if err := m.checkCaddyAccess(ctx); err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		identity, err := managedCoreServiceIdentity()
		if err != nil {
			return err
		}
		check, cancel := context.WithTimeout(ctx, 20*time.Second)
		output, err := runTLSCommand(check, m.state, m.caddyEnvironment(), executable, &identity, "tls-network-preflight", f.Domain, fmt.Sprint(f.ACMEEmail != ""))
		cancel()
		fmt.Println(output)
		if err != nil && (!strings.Contains(output, "ACME 网络检查：所有已启用机构均不可达") || !m.hasValidCertificate(f.Domain)) {
			return fmt.Errorf("[阶段:依赖和环境检查] %w；%s", err, output)
		}
		if err != nil {
			fmt.Println("[告警] ACME 检查失败，复用已有有效证书；续期仍需修复网络。")
		}
	}
	return nil
}

// No certificate contents or email addresses are printed in preflight reports.
func tlsPhaseError(phase string, err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "[阶段:") {
		return err
	}
	return fmt.Errorf("[阶段:%s] %w", phase, err)
}
