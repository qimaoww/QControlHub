package serverconfig

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

const ChinaRoutesURL = "https://raw.githubusercontent.com/misakaio/chnroutes2/master/chnroutes.txt"
const ChinaRoutesIPv6URL = "https://gaoyifan.github.io/china-operator-ip/china6.txt"

var chinaRoutesCache struct {
	sync.Mutex
	prefixes  []string
	refreshed time.Time
}

// LoadChinaRoutes downloads and validates the fixed chnroutes2 IPv4 and BGP
// IPv6 feeds. The combined result is cached for one hour. A last known good
// copy remains usable during a transient refresh failure.
func LoadChinaRoutes(ctx context.Context) ([]string, error) {
	chinaRoutesCache.Lock()
	defer chinaRoutesCache.Unlock()
	if len(chinaRoutesCache.prefixes) > 0 && time.Since(chinaRoutesCache.refreshed) < time.Hour {
		return slices.Clone(chinaRoutesCache.prefixes), nil
	}
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	ipv4, err := fetchChinaRoutes(ctx, client, ChinaRoutesURL)
	if err != nil {
		if len(chinaRoutesCache.prefixes) > 0 {
			return slices.Clone(chinaRoutesCache.prefixes), nil
		}
		return nil, errors.New("获取 chnroutes2 大陆路由表失败")
	}
	ipv6, err := fetchChinaRoutes(ctx, client, ChinaRoutesIPv6URL)
	if err != nil {
		if len(chinaRoutesCache.prefixes) > 0 {
			return slices.Clone(chinaRoutesCache.prefixes), nil
		}
		return nil, errors.New("获取大陆 IPv6 路由表失败")
	}
	prefixes := append(ipv4, ipv6...)
	if err := validateChinaRoutePrefixes(prefixes); err != nil {
		if len(chinaRoutesCache.prefixes) > 0 {
			return slices.Clone(chinaRoutesCache.prefixes), nil
		}
		return nil, err
	}
	chinaRoutesCache.prefixes = prefixes
	chinaRoutesCache.refreshed = time.Now()
	return slices.Clone(prefixes), nil
}

func fetchChinaRoutes(ctx context.Context, client *http.Client, url string) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("大陆路由表响应状态无效")
	}
	limited := &io.LimitedReader{R: response.Body, N: 512<<10 + 1}
	content, err := io.ReadAll(limited)
	if err != nil {
		return nil, errors.New("读取大陆路由表失败")
	}
	if len(content) > 512<<10 {
		return nil, errors.New("大陆路由表超过大小限制")
	}
	prefixes, err := parseChinaRoutes(strings.NewReader(string(content)))
	if err != nil {
		return nil, fmt.Errorf("解析大陆路由表：%w", err)
	}
	return prefixes, nil
}

func parseChinaRoutes(reader io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	prefixes := make([]string, 0, 4096)
	seen := make(map[netip.Prefix]struct{}, 4096)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		prefix, err := netip.ParsePrefix(line)
		if err != nil || !prefix.IsValid() || prefix != prefix.Masked() {
			return nil, errors.New("包含无效 CIDR")
		}
		if _, exists := seen[prefix]; exists {
			continue
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix.String())
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("读取失败")
	}
	if len(prefixes) < 1000 || len(prefixes) > 20000 {
		return nil, errors.New("条目数量异常")
	}
	return prefixes, nil
}

func validateChinaRoutePrefixes(prefixes []string) error {
	if len(prefixes) == 0 || len(prefixes) > 20000 {
		return errors.New("大陆路由表条目数量异常")
	}
	for _, value := range prefixes {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || !prefix.IsValid() || prefix != prefix.Masked() || prefix.String() != value {
			return errors.New("大陆路由表包含无效 CIDR")
		}
	}
	return nil
}
