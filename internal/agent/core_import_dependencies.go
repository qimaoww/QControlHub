package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// A reference records its configuration location, rather than guessing that
// every string resembling a pathname is an input file.
type importFileReference struct {
	path, field string
	set         func(string)
}

func importJSONDocument(content string) (map[string]any, error) {
	value, err := decodeExtendedJSON(content)
	if err != nil {
		return nil, err
	}
	root, ok := value.(map[string]any)
	if !ok || root == nil {
		return nil, errors.New("configuration must be an object")
	}
	return root, nil
}

func importJSONReferences(engine core.Engine, root map[string]any) ([]importFileReference, error) {
	refs := []importFileReference{}
	add := func(node map[string]any, field string, keys ...string) error {
		for _, key := range keys {
			value, exists := node[key]
			if !exists || value == nil {
				continue
			}
			if list, ok := value.([]any); ok {
				for i, v := range list {
					path, ok := v.(string)
					if !ok {
						return fmt.Errorf("%s.%s[%d] must be a file path", field, key, i)
					}
					if path != "" {
						refs = append(refs, importFileReference{path, fmt.Sprintf("%s.%s[%d]", field, key, i), func(p string) { list[i] = p }})
					}
				}
			} else {
				path, ok := value.(string)
				if !ok {
					return fmt.Errorf("%s.%s must be a file path", field, key)
				}
				if path != "" {
					refs = append(refs, importFileReference{path, strings.TrimPrefix(field+"."+key, "."), func(p string) { node[key] = p }})
				}
			}
		}
		return nil
	}
	var visit func(any, string) error
	visit = func(value any, field string) error {
		switch node := value.(type) {
		case map[string]any:
			if plugin, ok := node["plugin"].(string); ok && plugin != "" {
				return fmt.Errorf("%s.plugin: auxiliary executable %q requires a verified managed runtime mapping", field, plugin)
			}
			if engine == core.EngineSingBox {
				parent := field
				if index := strings.LastIndex(parent, "."); index >= 0 {
					parent = parent[index+1:]
				}
				if index := strings.Index(parent, "["); index >= 0 {
					parent = parent[:index]
				}
				nodeType, _ := node["type"].(string)
				fields := append([]singBoxResourceField{}, singBoxResourceFieldsByParent[parent]...)
				fields = append(fields, singBoxTypeResourceFields(nodeType)...)
				fields = append(fields, singBoxGlobalResourceFields...)
				known := map[string]bool{}
				for _, resource := range fields {
					key := resource.key
					known[key] = true
					if value, present := node[key]; present && value != nil && !emptyImportString(value) {
						if resource.allowLogSpecial {
							continue
						}
						if key == "protect_path" || key == "executable_path" {
							return fmt.Errorf("%s.%s: socket or auxiliary executable requires a verified managed runtime mapping", field, key)
						}
						if resource.kind == singBoxDirectoryResource || key == "cache_file" || key == "cache_path" || key == "usages_path" || parent == "cache_file" {
							return fmt.Errorf("%s.%s: directory or mutable state requires a verified managed runtime mapping", field, key)
						}
						if err := add(node, field, key); err != nil {
							return err
						}
					}
				}
				for key, value := range node {
					if known[key] || value == nil || emptyImportString(value) {
						continue
					}
					if _, ok := singBoxResourceFieldForName(key); ok {
						return fmt.Errorf("%s.%s: unclassified resource requires a verified managed runtime mapping", field, key)
					}
				}
				if masq, ok := node["masquerade"].(string); ok && strings.HasPrefix(masq, "file:") {
					return fmt.Errorf("%s.masquerade: file URL directory requires a verified managed runtime mapping", field)
				}
			}
			if engine == core.EngineXray {
				if strings.Contains(field, ".certificates[") {
					if err := add(node, field, "certificateFile", "keyFile"); err != nil {
						return err
					}
				}
			}
			// Sort object keys to make manifests and diagnostics deterministic. Array
			// order remains untouched, including route and tag relationships.
			keys := make([]string, 0, len(node))
			for key := range node {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				childField := key
				if field != "" {
					childField = field + "." + key
				}
				if err := visit(node[key], childField); err != nil {
					return err
				}
			}
		case []any:
			for i, child := range node {
				if err := visit(child, fmt.Sprintf("%s[%d]", field, i)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(root, ""); err != nil {
		return nil, err
	}
	return refs, nil
}

func prepareJSONDependencyImport(engine core.Engine, existing, managed EngineSpec, content string) (coreImportPlan, error) {
	root, err := importJSONDocument(content)
	if err != nil {
		return coreImportPlan{}, err
	}
	refs, err := importJSONReferences(engine, root)
	if err != nil {
		return coreImportPlan{}, err
	}
	if engine == core.EngineXray {
		assets, assetErr := importXrayAssetReferences(root, existing, managed)
		if assetErr != nil {
			return coreImportPlan{}, assetErr
		}
		refs = append(refs, assets...)
	}
	return prepareFileDependencyImport(engine, existing, managed, content, refs, func() (string, error) {
		encoded, err := json.MarshalIndent(root, "", "  ")
		return string(encoded) + "\n", err
	})
}

func prepareFileDependencyImport(engine core.Engine, existing, managed EngineSpec, content string, refs []importFileReference, encode func() (string, error)) (coreImportPlan, error) {
	plan := coreImportPlan{managedContent: content, stateRoot: filepath.Dir(managed.ConfigPath)}
	if len(refs) == 0 {
		return plan, nil
	}
	if len(refs) > 256 {
		return plan, errors.New("configuration references more than 256 import resources")
	}
	base := existing.WorkingDirectory
	if engine == core.EngineXray {
		// Xray GetCertLocation resolves relative certificate paths against the
		// executable, regardless of its configuration or working directory.
		base = filepath.Dir(existing.Binary)
	}
	if base == "" {
		base = filepath.Dir(existing.ConfigPath)
	}
	seen := map[string]bool{}
	resolvedRefs := make([]string, len(refs))
	total := 0
	contents := map[string][]byte{}
	// Use both path and content identity: equal basenames never collide, while
	// repeated references resolve to exactly one destination.
	for index, ref := range refs {
		path := ref.path
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		data, resolved, err := readExternalImportSource(path, maxReleaseAssetSize)
		if err != nil {
			return plan, fmt.Errorf("%s (%s): %w", ref.field, path, err)
		}
		if err := validateImportResourceAuthorization(path, resolved); err != nil {
			return plan, fmt.Errorf("%s: %w", ref.field, err)
		}
		contents[ref.path] = data
		total += len(data)
		if total > 256<<20 {
			return plan, errors.New("import resources exceed the combined 256 MiB limit")
		}
		digest := coreMigrationConfigDigest(string(data))
		resolvedRefs[index] = resolved
		if !seen[resolved] {
			seen[resolved] = true
			identity, err := importFileIdentity(resolved)
			if err != nil {
				return plan, err
			}
			plan.resources = append(plan.resources, coreImportResource{source: resolved, field: ref.field, digest: digest, limit: maxReleaseAssetSize, identity: identity})
		}
	}
	if err := validateImportCertificateMaterials(engine, refs, contents); err != nil {
		return plan, err
	}
	// One transaction directory, including all resource identities, permits
	// bounded rollback cleanup without touching resources from another import.
	var fingerprint strings.Builder
	fingerprint.WriteString(content)
	for _, r := range plan.resources {
		fingerprint.WriteString("\x00" + r.source + "\x00" + r.digest)
	}
	plan.resourceRoot = filepath.Join(plan.stateRoot, "imports", coreMigrationConfigDigest(fingerprint.String()))
	if engine == core.EngineXray {
		plan.stateRoot = filepath.Join(filepath.Dir(managed.Binary), "imports")
		plan.resourceRoot = filepath.Join(plan.stateRoot, coreMigrationConfigDigest(fingerprint.String()))
	}
	destinations := map[string]string{}
	for i := range plan.resources {
		r := &plan.resources[i]
		r.destination = filepath.Join(plan.resourceRoot, coreMigrationConfigDigest(r.source)+filepath.Ext(r.source))
		destinations[r.source] = r.destination
	}
	for i, ref := range refs {
		destination := destinations[resolvedRefs[i]]
		ref.set(destination)
		source := ref.path
		if !filepath.IsAbs(source) {
			source = filepath.Join(base, source)
		}
		plan.dependencies = append(plan.dependencies, core.ImportDependency{Field: ref.field, Source: source, Resolved: resolvedRefs[i], Destination: destination, Kind: "input"})
	}
	encoded, err := encode()
	plan.managedContent = encoded
	if err != nil {
		return plan, err
	}
	return plan, nil
}

func emptyImportString(value any) bool { text, ok := value.(string); return ok && text == "" }
