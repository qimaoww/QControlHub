package serverconfig

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

const DefaultRealityServerName = "www.amazon.com"

var realityServerNamePresets = []string{
	DefaultRealityServerName,
	"www.samsung.com",
	"www.oracle.com",
	"www.ibm.com",
	"www.intel.com",
}

// RealityServerNamePresets returns a copy so callers cannot change the
// application defaults. Every submitted target is still probed before save;
// the preset list alone is never treated as proof that a target is usable.
func RealityServerNamePresets() []string {
	return append([]string(nil), realityServerNamePresets...)
}

type RealityTarget struct {
	ServerName string
	CNAME      string
	Address    netip.Addr
	ALPN       string
}

type realityResolver interface {
	LookupCNAME(context.Context, string) (string, error)
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type realityProbeOptions struct {
	resolver          realityResolver
	dialContext       func(context.Context, string, string) (net.Conn, error)
	rootCAs           *x509.CertPool
	totalTimeout      time.Duration
	connectionTimeout time.Duration
}

// ProbeRealityTarget validates a Reality TLS camouflage target without making
// an HTTP request. DNS is resolved once, all returned addresses are checked,
// and the TLS connection is then pinned to one of those public addresses to
// prevent DNS rebinding and private-network SSRF.
func ProbeRealityTarget(ctx context.Context, serverName string) (RealityTarget, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: -1}
	return probeRealityTarget(ctx, serverName, realityProbeOptions{
		resolver:          net.DefaultResolver,
		dialContext:       dialer.DialContext,
		totalTimeout:      10 * time.Second,
		connectionTimeout: 5 * time.Second,
	})
}

func probeRealityTarget(ctx context.Context, serverName string, options realityProbeOptions) (RealityTarget, error) {
	normalized, err := normalizeRealityServerName(serverName)
	if err != nil {
		return RealityTarget{}, err
	}
	if options.resolver == nil || options.dialContext == nil {
		return RealityTarget{}, errors.New("Reality SNI 校验器未正确初始化")
	}
	if options.totalTimeout <= 0 {
		options.totalTimeout = 10 * time.Second
	}
	if options.connectionTimeout <= 0 {
		options.connectionTimeout = 5 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, options.totalTimeout)
	defer cancel()

	cname, err := options.resolver.LookupCNAME(ctx, normalized)
	if err != nil {
		return RealityTarget{}, fmt.Errorf("Reality SNI DNS CNAME 校验失败：%w", err)
	}
	cname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(cname)), ".")
	if cname == "" {
		cname = normalized
	}
	if isCloudflareName(normalized) || isCloudflareName(cname) {
		return RealityTarget{}, errors.New("Reality SNI 指向 Cloudflare 域名或 CNAME，已拒绝")
	}

	addresses, err := options.resolver.LookupNetIP(ctx, "ip", normalized)
	if err != nil {
		return RealityTarget{}, fmt.Errorf("Reality SNI DNS 解析失败：%w", err)
	}
	if len(addresses) == 0 {
		return RealityTarget{}, errors.New("Reality SNI 没有可用的 DNS 地址")
	}
	if len(addresses) > 16 {
		return RealityTarget{}, errors.New("Reality SNI 返回了过多 DNS 地址，已拒绝")
	}

	unique := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !isPublicRealityAddress(address) {
			return RealityTarget{}, fmt.Errorf("Reality SNI 解析到非公网地址 %s，已拒绝", address)
		}
		if isCloudflareAddress(address) {
			return RealityTarget{}, fmt.Errorf("Reality SNI 解析到 Cloudflare 地址 %s，已拒绝", address)
		}
		if _, ok := seen[address]; !ok {
			seen[address] = struct{}{}
			unique = append(unique, address)
		}
	}

	var lastErr error
	for _, address := range unique {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, options.connectionTimeout)
		target, probeErr := probeRealityAddress(attemptCtx, normalized, address, options)
		attemptCancel()
		if probeErr == nil {
			target.CNAME = cname
			return target, nil
		}
		lastErr = probeErr
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = ctx.Err()
	}
	return RealityTarget{}, fmt.Errorf("Reality SNI TLS 1.3 实时校验失败：%w", lastErr)
}

func probeRealityAddress(ctx context.Context, serverName string, address netip.Addr, options realityProbeOptions) (RealityTarget, error) {
	endpoint := net.JoinHostPort(address.String(), "443")
	connection, err := options.dialContext(ctx, "tcp", endpoint)
	if err != nil {
		return RealityTarget{}, fmt.Errorf("连接 %s：%w", endpoint, err)
	}
	defer connection.Close()

	tlsConnection := tls.Client(connection, &tls.Config{
		ServerName: serverName,
		MinVersion: tls.VersionTLS13,
		RootCAs:    options.rootCAs,
		NextProtos: []string{"h2", "http/1.1"},
	})
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		return RealityTarget{}, err
	}
	state := tlsConnection.ConnectionState()
	if state.Version != tls.VersionTLS13 || len(state.VerifiedChains) == 0 {
		return RealityTarget{}, errors.New("目标未通过 TLS 1.3 证书验证")
	}
	return RealityTarget{ServerName: serverName, Address: address, ALPN: state.NegotiatedProtocol}, nil
}

func normalizeRealityServerName(value string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(value))
	name = strings.TrimSuffix(name, ".")
	if name == "" || len(name) > 253 || net.ParseIP(name) != nil || strings.ContainsAny(name, ":/\\@?#") {
		return "", errors.New("Reality ServerName 必须是合法的公网域名，不能填写 IP、端口或 URL")
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return "", errors.New("Reality ServerName 必须是完整公网域名")
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("Reality ServerName 域名格式无效")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", errors.New("Reality ServerName 仅支持 ASCII 域名")
			}
		}
	}
	if isRetiredRealityName(name) {
		return "", errors.New("Microsoft 域名已不再作为可用的 Reality SNI，已拒绝")
	}
	if isCloudflareName(name) {
		return "", errors.New("Reality SNI 不能使用 Cloudflare 域名，已拒绝")
	}
	return name, nil
}

func isRetiredRealityName(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	for _, suffix := range []string{"microsoft.com"} {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			return true
		}
	}
	return false
}

func isCloudflareName(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	for _, suffix := range []string{
		"cloudflare.com",
		"cloudflare.net",
		"cloudflare-dns.com",
		"cloudflareaccess.com",
		"cloudflareresolve.com",
	} {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			return true
		}
	}
	return false
}

// Published by Cloudflare at https://www.cloudflare.com/ips/.
var cloudflareRealityPrefixes = mustParsePrefixes([]string{
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
})

func isPublicRealityAddress(address netip.Addr) bool {
	return netpolicy.IsPublicAddress(address)
}

func isCloudflareAddress(address netip.Addr) bool {
	for _, prefix := range cloudflareRealityPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func mustParsePrefixes(values []string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefixes = append(prefixes, netip.MustParsePrefix(value))
	}
	return prefixes
}
