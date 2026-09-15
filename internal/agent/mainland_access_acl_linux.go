package agent

import (
	"fmt"
	"strings"
)

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
