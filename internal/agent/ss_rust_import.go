package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Import resources must live inside the Agent's existing writable config
// namespace; core StateDirectory paths are read-only in the Agent sandbox.
var ssRustImportStateRoot = "/etc/qagent/shadowsocks-rust"

func existingSSRustACLArg(engine core.Engine, argv []string) string {
	if engine == core.EngineShadowsocksRust && len(argv) == 5 && argv[3] == "--acl" {
		return argv[4]
	}
	return ""
}

// Planning is read-only: the content-addressed ACL paths make the saved
// snapshot sensitive to ACL changes as well as JSON changes. Files are only
// staged inside the migration transaction, before the original service stops.
func prepareSSRustImport(existing EngineSpec, content string) (coreImportPlan, error) {
	if existing.ConfigDirectory != "" || existing.WorkingDirectory != "" {
		return coreImportPlan{}, errors.New("SS Rust import supports a single configuration file only")
	}
	var root map[string]any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if !json.Valid([]byte(content)) || decoder.Decode(&root) != nil || root == nil {
		return coreImportPlan{}, errors.New("SS Rust import requires a JSON object")
	}
	plan := coreImportPlan{managedContent: content, stateRoot: ssRustImportStateRoot}
	allowedACL := filepath.Join(filepath.Dir(existing.ConfigPath), "block_cn.acl")
	globalACL, err := ssRustImportACL(root)
	if err != nil {
		return coreImportPlan{}, err
	}
	if existing.ACLPath != "" {
		globalACL = existing.ACLPath
	}
	listKey := "servers"
	if _, hasServers := root["servers"]; hasServers {
		if _, hasAlias := root["shadowsocks"]; hasAlias {
			return coreImportPlan{}, errors.New("SS Rust import cannot combine servers and shadowsocks aliases")
		}
	}
	entries, extended := root[listKey].([]any)
	if value, exists := root[listKey]; exists && (!extended || value == nil) {
		return coreImportPlan{}, errors.New("SS Rust servers must be an array")
	}
	if !extended {
		listKey = "shadowsocks"
		entries, extended = root[listKey].([]any)
		if value, exists := root[listKey]; exists && (!extended || value == nil) {
			return coreImportPlan{}, errors.New("SS Rust shadowsocks must be an array")
		}
	}
	if extended {
		_, hasPrimary := root["server_port"]
		_, hasPrimaryAddress := root["server"]
		if hasPrimary || hasPrimaryAddress {
			return coreImportPlan{}, errors.New("SS Rust import cannot combine a primary server and extended servers; normalize the source first")
		}
		if len(entries) == 0 {
			return coreImportPlan{}, errors.New("SS Rust import requires at least one server")
		}
	}
	if !extended {
		entries = []any{root}
	}
	needsACL := globalACL != ""
	for _, value := range entries {
		entry, ok := value.(map[string]any)
		if !ok {
			return coreImportPlan{}, errors.New("SS Rust servers must contain objects")
		}
		acl, err := ssRustImportACL(entry)
		if err != nil {
			return coreImportPlan{}, err
		}
		if acl != "" && acl != allowedACL {
			return coreImportPlan{}, fmt.Errorf("unsupported SS Rust ACL path %q", acl)
		}
		needsACL = needsACL || acl != ""
	}
	if globalACL != "" && globalACL != allowedACL {
		return coreImportPlan{}, fmt.Errorf("unsupported SS Rust ACL path %q", globalACL)
	}
	if !needsACL {
		return plan, nil
	}
	aclContent, err := readConfigurationFile(allowedACL)
	if err != nil {
		return coreImportPlan{}, fmt.Errorf("read protected SS Rust ACL: %w", err)
	}
	if strings.TrimSpace(aclContent) == "" {
		return coreImportPlan{}, errors.New("SS Rust ACL is empty")
	}
	plan.resourceRoot = filepath.Join(plan.stateRoot, "install-ss-rust", coreMigrationConfigDigest(content+"\x00"+aclContent))
	destination := filepath.Join(plan.resourceRoot, "block_cn.acl")
	plan.resources = []coreImportResource{{source: allowedACL, destination: destination, digest: coreMigrationConfigDigest(aclContent)}}
	if !extended {
		entry := make(map[string]any)
		for _, key := range []string{"server", "server_port", "method", "password", "mode", "timeout", "dns", "outbound_bind_addr"} {
			if value, exists := root[key]; exists {
				entry[key] = value
			}
		}
		for _, key := range []string{"server", "server_port", "method", "password"} {
			delete(root, key)
		}
		entries = []any{entry}
		listKey = "servers"
	}
	for _, value := range entries {
		entry := value.(map[string]any)
		acl, _ := ssRustImportACL(entry)
		if acl != "" || globalACL != "" {
			// A per-server ACL takes precedence over the managed service's
			// global --acl argument, preserving the script's outbound policy.
			entry["acl"] = destination
		}
	}
	delete(root, "acl")
	if globalACL != "" {
		// Keep the inherited policy discoverable when the preset adds ports.
		root["acl"] = destination
	}
	root[listKey] = entries
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return coreImportPlan{}, err
	}
	plan.managedContent = string(encoded) + "\n"
	return plan, nil
}

func ssRustImportACL(entry map[string]any) (string, error) {
	if plugin, ok := entry["plugin"]; ok && plugin != "" && plugin != nil {
		return "", errors.New("SS Rust plugin processes are not supported by protected import")
	}
	value, present := entry["acl"]
	if !present {
		return "", nil
	}
	path, ok := value.(string)
	if !ok || path == "" {
		return "", errors.New("SS Rust ACL must be a non-empty absolute path")
	}
	return path, nil
}
