package core

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// APIKey exposes metadata only. The verification digest never leaves the store.
type APIKey struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Prefix      string       `json:"prefix"`
	CreatorID   string       `json:"-"`
	CreatorType string       `json:"-"`
	OwnerID     string       `json:"-"`
	Permissions []Permission `json:"permissions"`
	IPAllowlist []string     `json:"ip_allowlist"`
	Disabled    bool         `json:"disabled"`
	CreatedAt   time.Time    `json:"created_at"`
	ExpiresAt   *time.Time   `json:"expires_at"`
	LastUsedAt  *time.Time   `json:"last_used_at"`
	RevokedAt   *time.Time   `json:"revoked_at"`
	Revision    int64        `json:"revision"`
	Status      string       `json:"status"`
}

type APIKeyInput struct {
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions"`
	IPAllowlist []string     `json:"ip_allowlist"`
	ExpiresAt   *time.Time   `json:"expires_at"`
	Disabled    bool         `json:"disabled"`
	Revision    int64        `json:"revision"`
}

func DiagnosticPermissions() []Permission {
	return []Permission{PermissionOverviewRead, PermissionAgentsRead, PermissionDeploymentsRead, PermissionCatalogsRead, PermissionAgentConfigRead, PermissionConfigsRead, PermissionTasksRead, PermissionSettingsRead, PermissionAuditRead, PermissionMetricsRead, PermissionPanelMetricsRead, PermissionCoreLogsRead, PermissionTrafficRead, PermissionTemplatesRead}
}

func NormalizeIPAllowlist(values []string) ([]string, error) {
	if len(values) > 100 {
		return nil, fmt.Errorf("IP 白名单最多 100 条")
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		var canonical string
		if strings.Contains(raw, "/") {
			p, err := netip.ParsePrefix(raw)
			if err != nil || p.Addr().Zone() != "" {
				return nil, fmt.Errorf("IP 白名单包含无效 CIDR")
			}
			if p.Addr().Is4In6() {
				if p.Bits() < 96 {
					return nil, fmt.Errorf("映射 IPv4 CIDR 的前缀长度不得小于 96")
				}
				p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
			}
			p = p.Masked()
			if p.Bits() == p.Addr().BitLen() {
				canonical = p.Addr().String()
			} else {
				canonical = p.String()
			}
		} else {
			a, err := netip.ParseAddr(raw)
			if err != nil || a.Zone() != "" {
				return nil, fmt.Errorf("IP 白名单包含无效地址")
			}
			canonical = a.Unmap().String()
		}
		if !seen[canonical] {
			result = append(result, canonical)
			seen[canonical] = true
		}
	}
	return result, nil
}

func IPAllowed(values []string, source string) bool {
	if len(values) == 0 {
		return true
	}
	a, err := netip.ParseAddr(source)
	if err != nil || a.Zone() != "" {
		return false
	}
	a = a.Unmap()
	for _, raw := range values {
		if p, err := netip.ParsePrefix(raw); err == nil && p.Contains(a) {
			return true
		}
		if ip, err := netip.ParseAddr(raw); err == nil && ip.Unmap() == a {
			return true
		}
	}
	return false
}
