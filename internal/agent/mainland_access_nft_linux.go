package agent

import (
	"context"
	"fmt"
	"strings"
)

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
