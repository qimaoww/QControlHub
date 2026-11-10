package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

func importResourceLimit(resource coreImportResource) int64 {
	if resource.limit > 0 {
		return resource.limit
	}
	return ouSBResourceLimit
}

func (plan coreImportPlan) verifySources() error {
	for _, dependency := range plan.dependencies {
		current, err := resolveImportSource(dependency.Source)
		if err != nil || current != dependency.Resolved {
			return fmt.Errorf("%s: source link changed after snapshot planning", dependency.Field)
		}
	}
	for _, resource := range plan.resources {
		if resource.identity != "" {
			identity, err := importFileIdentity(resource.source)
			if err != nil || identity != resource.identity {
				return errors.Join(err, fmt.Errorf("import resource %s identity changed after snapshot planning", resource.source))
			}
		}
		data, _, err := readImportSource(resource.source, importResourceLimit(resource))
		if err != nil {
			return fmt.Errorf("recheck import resource %s: %w", resource.source, err)
		}
		if coreMigrationConfigDigest(string(data)) != resource.digest {
			return fmt.Errorf("import resource %s changed after snapshot planning", resource.source)
		}
	}
	return nil
}

func rewriteImportResourcePaths(engine core.Engine, content string, paths map[string]string) (string, error) {
	if engine == core.EngineMihomo {
		var document yaml.Node
		if err := yaml.Unmarshal([]byte(content), &document); err != nil {
			return "", err
		}
		refs, err := importYAMLReferences(&document)
		if err != nil {
			return "", err
		}
		for _, ref := range refs {
			if target, ok := paths[ref.path]; ok {
				ref.set(target)
			}
		}
		data, err := yaml.Marshal(&document)
		return string(data), err
	}
	root, err := importJSONDocument(content)
	if err != nil {
		return "", err
	}
	refs, err := importJSONReferences(engine, root)
	if err != nil {
		return "", err
	}
	if engine == core.EngineShadowsocksRust {
		var visit func(any)
		visit = func(value any) {
			switch node := value.(type) {
			case map[string]any:
				if p, ok := node["acl"].(string); ok {
					if target, ok := paths[p]; ok {
						node["acl"] = target
					}
				}
				for _, child := range node {
					visit(child)
				}
			case []any:
				for _, child := range node {
					visit(child)
				}
			}
		}
		visit(root)
	}
	for _, ref := range refs {
		if target, ok := paths[ref.path]; ok {
			ref.set(target)
		}
	}
	if engine == core.EngineXray {
		rewriteXrayPreviewAssets(root, paths)
	}
	data, err := json.MarshalIndent(root, "", "  ")
	return string(data) + "\n", err
}

// Preview uses disposable copies. It cannot create the final import resources
// or change the source service, and it validates the exact mapped relationships.
func (e *Executor) validateImportPlanSnapshot(ctx context.Context, engine core.Engine, spec EngineSpec, plan coreImportPlan) error {
	if !plan.active() {
		_, err := e.validateSnapshot(ctx, engine, spec, plan.managedContent)
		return err
	}
	directory := filepath.Dir(spec.ConfigPath)
	if engine == core.EngineMihomo {
		spec.commandEnv = "SAFE_PATHS=" + directory
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := validateProtectedDirectoryChain(directory); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(directory, ".import-preview-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	preview := plan
	preview.stateRoot = directory
	preview.resourceRoot = filepath.Join(temporary, "resources")
	preview.resources = append([]coreImportResource(nil), plan.resources...)
	preview.createdResources = nil
	preview.createdDirectory = false
	paths := map[string]string{}
	for i := range preview.resources {
		r := &preview.resources[i]
		next := filepath.Join(preview.resourceRoot, filepath.Base(r.destination))
		paths[r.destination] = next
		r.destination = next
	}
	content, err := rewriteImportResourcePaths(engine, plan.managedContent, paths)
	if err != nil {
		return err
	}
	identity := commandIdentity{uid: uint32(os.Geteuid()), gid: uint32(os.Getegid())}
	// The preview is private to the Agent, while the enclosing managed
	// directory already belongs to the core group. Preserve that group.
	if info, err := os.Stat(directory); err == nil {
		if _, gid, known := fileOwnership(info); known {
			identity.gid = uint32(gid)
		}
	} else {
		return err
	}
	if err := preview.stage(identity); err != nil {
		return err
	}
	if engine == core.EngineXray {
		spec.commandEnv = "XRAY_LOCATION_ASSET=" + preview.resourceRoot
	}
	if _, err := e.validateSnapshot(ctx, engine, spec, content); err != nil {
		return err
	}
	if err := preview.verifySources(); err != nil {
		return err
	}
	for _, resource := range preview.resources {
		data, err := os.ReadFile(resource.destination)
		if err != nil || coreMigrationConfigDigest(string(data)) != resource.digest {
			return errors.Join(err, errors.New("preview resource changed during validation"))
		}
	}
	return nil
}

func importPlanFingerprint(plan coreImportPlan) string {
	var value strings.Builder
	value.WriteString(plan.managedContent)
	for _, resource := range plan.resources {
		value.WriteString("\x00" + resource.source + "\x00" + resource.digest + "\x00" + resource.identity)
	}
	return coreMigrationConfigDigest(value.String())
}
