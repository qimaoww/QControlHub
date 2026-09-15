package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/qimaoww/qcontrolhub/internal/cnip"
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
	if err != nil || len(contents) > 32<<20 {
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
	if len(encoded) > 32<<20 {
		err = errors.New("mainland access state exceeds 32 MiB")
	}
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
		if !core.ValidSSRustTag(policy.Tag) || policy.Port < 1 || policy.Port > 65535 {
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
		var custom []string
		for _, policy := range policies {
			if len(policy.CNIPPrefixes) > 0 {
				custom = policy.CNIPPrefixes
				break
			}
		}
		if len(custom) > 0 {
			validated, parseErr := cnip.Parse([]byte(strings.Join(custom, "\n")), "txt")
			if parseErr != nil {
				return parseErr
			}
			ranges = cnip.IPv4Ranges(validated)
			if len(ranges) == 0 {
				return errors.New("ss-rust CN IP 源需要包含 IPv4 网段")
			}
		} else {
			ranges, err = loader(ctx)
		}
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
