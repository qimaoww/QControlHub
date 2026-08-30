package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	mainlandFirewallTable   = "qcontrolhub_mainland"
	mainlandIPv4URL         = "https://raw.githubusercontent.com/misakaio/chnroutes2/master/chnroutes.txt"
	mainlandDomainURL       = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/refs/heads/meta/geo/geosite/geolocation-cn.list"
	mainlandRouteCacheName  = "mainland-cn-ipv4.ranges"
	mainlandDomainCacheName = "mainland-cn-domains.acl"
	mainlandStateName       = "mainland-access-state.json"
	shadowsocksRustACLPath  = "/etc/qagent/shadowsocks-rust/qch-mainland-block.acl"
)

var mainlandTagPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// MainlandAccessManager applies Shadowsocks Rust access policies without
// bloating its JSON configuration. Destination blocking uses ssserver's native
// ACL file while mainland-source blocking uses an isolated nftables table keyed
// by listening port. No qagent or unrelated ports are touched.
type MainlandAccessManager struct {
	mu            sync.Mutex
	backend       *nftBackend
	cachePath     string
	domainPath    string
	aclPath       string
	statePath     string
	current       []core.MainlandAccessPolicy
	routesLoader  func(context.Context) ([]string, error)
	domainsLoader func(context.Context) ([]string, error)
	aclWriter     func(string) error
}

func NewMainlandAccessManager(statePath string, serviceManager *ServiceManager) *MainlandAccessManager {
	manager := &MainlandAccessManager{
		backend:    newNFTBackendForServiceManager(serviceManager),
		cachePath:  filepath.Join(filepath.Dir(statePath), mainlandRouteCacheName),
		domainPath: filepath.Join(filepath.Dir(statePath), mainlandDomainCacheName),
		aclPath:    shadowsocksRustACLPath,
		statePath:  filepath.Join(filepath.Dir(statePath), mainlandStateName),
	}
	manager.routesLoader = manager.loadRoutes
	manager.domainsLoader = manager.loadDomains
	manager.aclWriter = manager.writeACL
	return manager
}

func (manager *MainlandAccessManager) Restore(ctx context.Context, agentID string) error {
	if manager == nil {
		return nil
	}
	contents, err := os.ReadFile(manager.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || len(contents) > 256<<10 {
		return errors.New("read saved mainland access state")
	}
	var policies []core.MainlandAccessPolicy
	if err := json.Unmarshal(contents, &policies); err != nil {
		return fmt.Errorf("parse saved mainland access state: %w", err)
	}
	if err := manager.Apply(ctx, policies, agentID); err != nil {
		return err
	}
	manager.mu.Lock()
	manager.current = append([]core.MainlandAccessPolicy(nil), policies...)
	manager.mu.Unlock()
	return nil
}

func (manager *MainlandAccessManager) Snapshot() []core.MainlandAccessPolicy {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return append([]core.MainlandAccessPolicy(nil), manager.current...)
}

// Deploy changes the kernel rules and persists the exact applied policy set.
// If state persistence fails, the previous kernel rules are restored so a
// reboot can never silently resurrect a different policy.
func (manager *MainlandAccessManager) Deploy(ctx context.Context, policies []core.MainlandAccessPolicy, agentID string) error {
	previous := manager.Snapshot()
	if err := manager.Apply(ctx, policies, agentID); err != nil {
		if rollbackErr := manager.Apply(ctx, previous, agentID); rollbackErr != nil {
			return fmt.Errorf("%v; restore previous mainland access policy: %w", err, rollbackErr)
		}
		return err
	}
	encoded, err := json.Marshal(policies)
	if err == nil {
		_, err = atomicDeploy(manager.statePath, string(encoded)+"\n")
	}
	if err != nil {
		_ = manager.Apply(ctx, previous, agentID)
		return fmt.Errorf("persist mainland access state: %w", err)
	}
	manager.mu.Lock()
	manager.current = append([]core.MainlandAccessPolicy(nil), policies...)
	manager.mu.Unlock()
	return nil
}

func (manager *MainlandAccessManager) Apply(ctx context.Context, policies []core.MainlandAccessPolicy, agentID string) error {
	if manager == nil || manager.backend == nil {
		return errors.New("mainland access manager is unavailable")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	ports := make([]int, 0, len(policies))
	seen := make(map[int]struct{})
	destinationEnabled := false
	needsFirewallUpdate := false
	needsACLUpdate := false
	for _, policy := range manager.current {
		if policy.Engine == core.EngineShadowsocksRust && policy.BlockMainlandDestination {
			needsACLUpdate = true
		}
		if policy.Engine == core.EngineShadowsocksRust && policy.BlockMainlandSource {
			needsFirewallUpdate = true
			break
		}
	}
	for _, policy := range policies {
		if policy.AgentID != "" && policy.AgentID != agentID {
			return errors.New("control plane returned a mainland policy for another Agent")
		}
		if policy.ConfigVersion < 1 {
			return errors.New("control plane returned a mainland policy without a configuration version")
		}
		if policy.Engine != core.EngineShadowsocksRust || (!policy.BlockMainlandDestination && !policy.BlockMainlandSource) {
			continue
		}
		if !mainlandTagPattern.MatchString(policy.Tag) || policy.Port < 1 || policy.Port > 65535 {
			return errors.New("control plane returned an invalid ss-rust mainland policy")
		}
		if policy.BlockMainlandDestination {
			destinationEnabled = true
		}
		if !policy.BlockMainlandSource {
			continue
		}
		if _, exists := seen[policy.Port]; !exists {
			seen[policy.Port] = struct{}{}
			ports = append(ports, policy.Port)
		}
	}
	sort.Ints(ports)
	needsFirewallUpdate = needsFirewallUpdate || len(ports) > 0
	needsACLUpdate = needsACLUpdate || destinationEnabled
	var ranges []string
	if len(ports) > 0 || destinationEnabled {
		loader := manager.routesLoader
		if loader == nil {
			loader = manager.loadRoutes
		}
		var err error
		ranges, err = loader(ctx)
		if err != nil {
			return err
		}
	}
	var domains []string
	if destinationEnabled {
		loader := manager.domainsLoader
		if loader == nil {
			loader = manager.loadDomains
		}
		var err error
		domains, err = loader(ctx)
		if err != nil {
			return err
		}
	}
	if needsACLUpdate {
		writer := manager.aclWriter
		if writer == nil {
			writer = manager.writeACL
		}
		if err := writer(renderShadowsocksRustACL(destinationEnabled, ranges, domains)); err != nil {
			return err
		}
	}
	if !needsFirewallUpdate {
		return nil
	}
	return manager.replace(ctx, ranges, ports)
}

func (manager *MainlandAccessManager) replace(ctx context.Context, ranges []string, ports []int) error {
	if err := manager.backend.ensureAvailable(ctx); err != nil {
		return err
	}
	var script strings.Builder
	if _, err := manager.backend.run(ctx, nil, "-j", "list", "table", "inet", mainlandFirewallTable); err == nil {
		fmt.Fprintf(&script, "delete table inet %s\n", mainlandFirewallTable)
	}
	if len(ports) == 0 {
		if strings.TrimSpace(script.String()) == "" {
			return nil
		}
	} else {
		fmt.Fprintf(&script, "add table inet %s\n", mainlandFirewallTable)
		fmt.Fprintf(&script, "add set inet %s cn_ipv4 { type ipv4_addr; flags interval; elements = { %s } }\n", mainlandFirewallTable, strings.Join(ranges, ", "))
		fmt.Fprintf(&script, "add chain inet %s input { type filter hook input priority -5; policy accept; }\n", mainlandFirewallTable)
		for _, port := range ports {
			fmt.Fprintf(&script, "add rule inet %s input ip saddr @cn_ipv4 tcp dport %d reject comment %q\n", mainlandFirewallTable, port, "qch:mainland:ss-rust:"+fmt.Sprint(port)+":tcp")
			fmt.Fprintf(&script, "add rule inet %s input ip saddr @cn_ipv4 udp dport %d reject comment %q\n", mainlandFirewallTable, port, "qch:mainland:ss-rust:"+fmt.Sprint(port)+":udp")
		}
	}
	contents := []byte(script.String())
	if _, err := manager.backend.run(ctx, contents, "-c", "-f", "-"); err != nil {
		return fmt.Errorf("validate mainland nftables rules: %w", err)
	}
	if _, err := manager.backend.run(ctx, contents, "-f", "-"); err != nil {
		return fmt.Errorf("apply mainland nftables rules: %w", err)
	}
	return nil
}

func (manager *MainlandAccessManager) loadRoutes(ctx context.Context) ([]string, error) {
	if cached, err := readMainlandRouteCache(manager.cachePath); err == nil && len(cached) > 0 {
		return cached, nil
	}
	fallback := func(cause error) ([]string, error) {
		if cached, err := readMainlandRouteCacheIgnoringAge(manager.cachePath); err == nil && len(cached) > 0 {
			return cached, nil
		}
		return nil, cause
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, mainlandIPv4URL, nil)
	if err != nil {
		return fallback(err)
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return fallback(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fallback(fmt.Errorf("mainland route feed returned HTTP %d", response.StatusCode))
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, 512<<10+1))
	if err != nil || len(contents) > 512<<10 {
		return fallback(errors.New("mainland route feed is unavailable or too large"))
	}
	ranges, err := parseMainlandIPv4Ranges(strings.NewReader(string(contents)))
	if err != nil {
		return fallback(err)
	}
	if err := os.MkdirAll(filepath.Dir(manager.cachePath), 0o700); err == nil {
		_ = os.WriteFile(manager.cachePath, []byte(strings.Join(ranges, "\n")+"\n"), 0o600)
	}
	return ranges, nil
}

func (manager *MainlandAccessManager) loadDomains(ctx context.Context) ([]string, error) {
	if cached, err := readMainlandDomainCache(manager.domainPath); err == nil && len(cached) > 0 {
		return cached, nil
	}
	fallback := func(cause error) ([]string, error) {
		if cached, err := readMainlandDomainCacheIgnoringAge(manager.domainPath); err == nil && len(cached) > 0 {
			return cached, nil
		}
		return nil, cause
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, mainlandDomainURL, nil)
	if err != nil {
		return fallback(err)
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return fallback(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fallback(fmt.Errorf("mainland domain feed returned HTTP %d", response.StatusCode))
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, 512<<10+1))
	if err != nil || len(contents) > 512<<10 {
		return fallback(errors.New("mainland domain feed is unavailable or too large"))
	}
	domains, err := parseMainlandDomains(strings.NewReader(string(contents)))
	if err != nil {
		return fallback(err)
	}
	if err := os.MkdirAll(filepath.Dir(manager.domainPath), 0o700); err == nil {
		_ = os.WriteFile(manager.domainPath, []byte(strings.Join(domains, "\n")+"\n"), 0o600)
	}
	return domains, nil
}

func (manager *MainlandAccessManager) writeACL(content string) error {
	metadata, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, manager.aclPath)
	if err != nil {
		return fmt.Errorf("prepare Shadowsocks Rust ACL access: %w", err)
	}
	if _, err := atomicDeployWithDefaultMetadata(manager.aclPath, content, metadata); err != nil {
		return fmt.Errorf("write Shadowsocks Rust mainland ACL: %w", err)
	}
	return nil
}

func renderShadowsocksRustACL(enabled bool, ranges, domains []string) string {
	var result strings.Builder
	result.WriteString("# QControlHub managed Shadowsocks Rust mainland ACL.\n")
	result.WriteString("# Sources: misakaio/chnroutes2 and MetaCubeX/meta-rules-dat.\n\n")
	result.WriteString("[outbound_block_list]\n")
	if !enabled {
		return result.String()
	}
	result.WriteByte('\n')
	for _, value := range ranges {
		result.WriteString(value)
		result.WriteByte('\n')
	}
	result.WriteByte('\n')
	for _, value := range domains {
		result.WriteString(value)
		result.WriteByte('\n')
	}
	return result.String()
}

func readMainlandRouteCache(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 512<<10 || time.Since(info.ModTime()) > 24*time.Hour {
		return nil, errors.New("mainland route cache is stale")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseMainlandIPv4Ranges(file)
}

func readMainlandRouteCacheIgnoringAge(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 512<<10 {
		return nil, errors.New("mainland route cache is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseMainlandIPv4Ranges(file)
}

func readMainlandDomainCache(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 512<<10 || time.Since(info.ModTime()) > 24*time.Hour {
		return nil, errors.New("mainland domain cache is stale")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseMainlandACLDomainLines(file)
}

func readMainlandDomainCacheIgnoringAge(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 512<<10 {
		return nil, errors.New("mainland domain cache is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseMainlandACLDomainLines(file)
}

func parseMainlandIPv4Ranges(reader io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	result := make([]string, 0, 4096)
	seen := make(map[string]struct{}, 4096)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		prefix, err := netip.ParsePrefix(line)
		if err != nil || !prefix.IsValid() || !prefix.Addr().Is4() || prefix != prefix.Masked() {
			return nil, errors.New("mainland route feed contains an invalid IPv4 CIDR")
		}
		value := prefix.String()
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) < 1000 || len(result) > 20000 {
		return nil, errors.New("mainland route feed entry count is outside the safe range")
	}
	return result, nil
}

func parseMainlandDomains(reader io.Reader) ([]string, error) {
	return parseMainlandDomainLines(reader, false)
}

func parseMainlandACLDomainLines(reader io.Reader) ([]string, error) {
	return parseMainlandDomainLines(reader, true)
}

func parseMainlandDomainLines(reader io.Reader, aclFormat bool) ([]string, error) {
	scanner := bufio.NewScanner(reader)
	result := make([]string, 0, 6000)
	seen := make(map[string]struct{}, 6000)
	domainPattern := regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+$`)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		value := ""
		if aclFormat {
			domain := ""
			if strings.HasPrefix(line, "||") && !strings.HasPrefix(line, "|||") {
				domain = strings.TrimPrefix(line, "||")
			} else if strings.HasPrefix(line, "|") && !strings.HasPrefix(line, "||") {
				domain = strings.TrimPrefix(line, "|")
			}
			if domainPattern.MatchString(domain) {
				value = line
			}
		} else if strings.HasPrefix(line, "+.") && domainPattern.MatchString(strings.TrimPrefix(line, "+.")) {
			value = "||" + strings.TrimPrefix(line, "+.")
		} else if domainPattern.MatchString(line) {
			value = "|" + line
		}
		if value == "" {
			return nil, errors.New("mainland domain feed contains an invalid domain")
		}
		value = strings.ToLower(value)
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) < 1000 || len(result) > 50000 {
		return nil, errors.New("mainland domain feed entry count is outside the safe range")
	}
	return result, nil
}
