package cnip

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

// Resolve and dial the same public address to prevent DNS rebinding. Source
// URLs are supplied by individual accounts, never trusted as network policy.
func Fetch(ctx context.Context, source core.CNIPSource) ([]string, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	transport := &http.Transport{TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, errors.New("无法解析 CN IP 数据源")
			}
			for _, ip := range ips {
				if !netpolicy.IsPublicAddress(ip) {
					return nil, errors.New("CN IP 数据源不能指向内网或保留地址")
				}
			}
			for _, ip := range ips {
				conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
			}
			return nil, errors.New("无法连接 CN IP 数据源")
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("CN IP 重定向过多")
		}
		return (core.CNIPSource{URL: req.URL.String(), Format: source.Format}).Validate()
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("下载 CN IP 数据源失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("CN IP 数据源返回非成功状态")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return nil, errors.New("读取 CN IP 数据源失败")
	}
	return Parse(data, source.Format)
}

// IPv4Ranges is also used by the ssserver native ACL/firewall adapter.
func IPv4Ranges(prefixes []string) []string {
	result := []string{}
	for _, v := range prefixes {
		p, e := netip.ParsePrefix(v)
		if e == nil && p.Addr().Is4() {
			result = append(result, v)
		}
	}
	return result
}

const DefaultIPv4URL = "https://raw.githubusercontent.com/misakaio/chnroutes2/master/chnroutes.txt"
const DefaultIPv6URL = "https://gaoyifan.github.io/china-operator-ip/china6.txt"

// Resolve each family independently. Reuse the same download when a source
// contains both families; a missing family is an error, never silently omitted.
func FetchRoutes(ctx context.Context, source core.CNIPSource) ([]string, error) {
	return fetchFamilies(ctx, source, Fetch)
}
func fetchFamilies(ctx context.Context, source core.CNIPSource, fetch func(context.Context, core.CNIPSource) ([]string, error)) ([]string, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	v4, v6 := source.URL, source.IPv6URL
	if v4 == "" {
		v4 = DefaultIPv4URL
	}
	if v6 == "" {
		v6 = DefaultIPv6URL
	}
	cache := map[string][]string{}
	result := []string{}
	for i, url := range []string{v4, v6} {
		values, ok := cache[url]
		if !ok {
			var err error
			values, err = fetch(ctx, core.CNIPSource{URL: url, Format: "auto"})
			if err != nil {
				return nil, err
			}
			cache[url] = values
		}
		before := len(result)
		for _, value := range values {
			p, err := netip.ParsePrefix(value)
			if err != nil {
				return nil, errors.New("CN IP 含无效网段")
			}
			if p.Addr().Is4() == (i == 0) {
				result = append(result, value)
			}
		}
		if len(result) == before {
			if i == 0 {
				return nil, errors.New("IPv4 数据源没有 IPv4 网段")
			}
			return nil, errors.New("IPv6 数据源没有 IPv6 网段")
		}
	}
	if len(result) > MaxPrefixes {
		return nil, errors.New("CN IP 双栈网段数量过多")
	}
	return result, nil
}
