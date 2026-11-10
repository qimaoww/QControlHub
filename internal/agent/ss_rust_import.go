package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

var ssRustImportStateRoot = "/etc/qagent/shadowsocks-rust"

func existingSSRustACLArg(engine core.Engine, argv []string) string {
	if engine != core.EngineShadowsocksRust {
		return ""
	}
	value := ""
	for i := 1; i < len(argv); i++ {
		if argv[i] == "--acl" && i+1 < len(argv) {
			i++
			value = argv[i]
		} else if strings.HasPrefix(argv[i], "--acl=") {
			value = strings.TrimPrefix(argv[i], "--acl=")
		}
	}
	return value
}

// Preserve every field while mapping each ACL independently. Per-server ACLs
// take precedence over the inherited command-line ACL, including when two
// servers use different files with the same basename.
func prepareSSRustImport(existing EngineSpec, content string, targets ...EngineSpec) (coreImportPlan, error) {
	if existing.ConfigDirectory != "" {
		return coreImportPlan{}, errors.New("SS Rust configuration directories are unsupported")
	}
	normalized, err := normalizeImportedSSRustLogDestinations(content)
	if err != nil {
		return coreImportPlan{}, err
	}
	root, err := importJSONDocument(normalized)
	if err != nil {
		return coreImportPlan{}, err
	}
	if value, present := root["manager_address"]; present && value != nil && value != "" {
		return coreImportPlan{}, errors.New("SS Rust manager_address requires a verified auxiliary manager and socket mapping")
	}
	global, err := ssRustImportACL(root)
	if err != nil {
		return coreImportPlan{}, err
	}
	if existing.ACLPath != "" {
		global = existing.ACLPath
	}
	key := "servers"
	entries, extended := root[key].([]any)
	if value, present := root[key]; present && (!extended || value == nil) {
		return coreImportPlan{}, errors.New("SS Rust servers must be an array")
	}
	if !extended {
		key = "shadowsocks"
		entries, extended = root[key].([]any)
		if value, present := root[key]; present && (!extended || value == nil) {
			return coreImportPlan{}, errors.New("SS Rust shadowsocks must be an array")
		}
	}
	if _, a := root["servers"]; a {
		if _, b := root["shadowsocks"]; b {
			return coreImportPlan{}, errors.New("SS Rust servers aliases cannot be combined")
		}
	}
	if extended {
		if len(entries) == 0 {
			return coreImportPlan{}, errors.New("SS Rust import requires at least one server")
		}
		if _, present := root["server"]; present {
			return coreImportPlan{}, errors.New("SS Rust primary and extended servers cannot be combined")
		}
		if _, present := root["server_port"]; present {
			return coreImportPlan{}, errors.New("SS Rust primary and extended servers cannot be combined")
		}
	} else {
		entries = []any{root}
	}
	plan := coreImportPlan{managedContent: normalized, stateRoot: ssRustImportStateRoot}
	if len(targets) > 0 {
		plan.stateRoot = filepath.Dir(targets[0].ConfigPath)
	}
	bySource := map[string]int{}
	var selected []string
	var fingerprint strings.Builder
	fingerprint.WriteString(normalized)
	for entryIndex, value := range entries {
		entry, ok := value.(map[string]any)
		if !ok {
			return plan, errors.New("SS Rust servers must contain objects")
		}
		path, err := ssRustImportACL(entry)
		if err != nil {
			return plan, err
		}
		if path == "" || !extended {
			path = global
		}
		if path == "" {
			selected = append(selected, "")
			continue
		}
		if !filepath.IsAbs(path) {
			base := existing.WorkingDirectory
			if base == "" {
				base = filepath.Dir(existing.ConfigPath)
			}
			path = filepath.Join(base, path)
		}
		data, resolved, err := readExternalImportSource(path, ouSBResourceLimit)
		if err != nil {
			return plan, fmt.Errorf("SS Rust acl (%s): %w", path, err)
		}
		if err := validateImportResourceAuthorization(path, resolved); err != nil {
			return plan, err
		}
		if strings.TrimSpace(string(data)) == "" {
			return plan, errors.New("SS Rust ACL is empty")
		}
		selected = append(selected, resolved)
		if _, ok := bySource[resolved]; !ok {
			bySource[resolved] = len(plan.resources)
			digest := coreMigrationConfigDigest(string(data))
			identity, err := importFileIdentity(resolved)
			if err != nil {
				return plan, err
			}
			plan.resources = append(plan.resources, coreImportResource{source: resolved, digest: digest, identity: identity, limit: ouSBResourceLimit, field: fmt.Sprintf("%s[%d].acl", key, entryIndex)})
			fingerprint.WriteString("\x00" + resolved + "\x00" + digest)
		}
		plan.dependencies = append(plan.dependencies, core.ImportDependency{Field: fmt.Sprintf("%s[%d].acl", key, entryIndex), Source: path, Resolved: resolved, Kind: "input"})
	}
	if len(plan.resources) == 0 {
		return plan, nil
	}
	if len(plan.resources) > 256 {
		return plan, errors.New("SS Rust import exceeds 256 ACL files")
	}
	plan.resourceRoot = filepath.Join(plan.stateRoot, "install-ss-rust", coreMigrationConfigDigest(fingerprint.String()))
	for i := range plan.resources {
		r := &plan.resources[i]
		r.destination = filepath.Join(plan.resourceRoot, coreMigrationConfigDigest(r.source)+".acl")
	}
	for i := range plan.dependencies {
		plan.dependencies[i].Destination = plan.resources[bySource[plan.dependencies[i].Resolved]].destination
	}
	if !extended {
		entry := make(map[string]any, len(root))
		for k, v := range root {
			entry[k] = v
		}
		for _, k := range []string{"server", "server_port", "method", "password", "plugin", "plugin_opts", "plugin_mode", "acl"} {
			delete(root, k)
		}
		entries = []any{entry}
		key = "servers"
	}
	for i, value := range entries {
		if selected[i] != "" {
			value.(map[string]any)["acl"] = plan.resources[bySource[selected[i]]].destination
		}
	}
	delete(root, "acl")
	if global != "" { // Preserve the inherited policy for managed preset additions.
		path := global
		if !filepath.IsAbs(path) {
			base := existing.WorkingDirectory
			if base == "" {
				base = filepath.Dir(existing.ConfigPath)
			}
			path = filepath.Join(base, path)
		}
		resolved, err := resolveImportSource(path)
		if err != nil {
			return plan, err
		}
		if index, ok := bySource[resolved]; ok {
			root["acl"] = plan.resources[index].destination
		}
	}
	root[key] = entries
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return plan, err
	}
	plan.managedContent = string(encoded) + "\n"
	return plan, nil
}

func ssRustImportACL(entry map[string]any) (string, error) {
	if plugin, ok := entry["plugin"]; ok && plugin != "" && plugin != nil {
		return "", errors.New("SS Rust plugin requires a verified auxiliary executable and managed startup mapping")
	}
	value, present := entry["acl"]
	if !present {
		return "", nil
	}
	path, ok := value.(string)
	if !ok || path == "" {
		return "", errors.New("SS Rust ACL must be a non-empty file path")
	}
	return path, nil
}

func existingACLFromCommand(engine core.Engine, command string) string {
	argv, err := splitServiceWords(command)
	if err != nil {
		return ""
	}
	return existingSSRustACLArg(engine, argv)
}
