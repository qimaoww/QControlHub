package agent

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

func importYAMLReferences(document *yaml.Node) ([]importFileReference, error) {
	var refs []importFileReference
	visiting := map[*yaml.Node]bool{}
	nodes := 0
	var visit func(*yaml.Node, string) error
	visit = func(node *yaml.Node, field string) error {
		nodes++
		if nodes > 16384 {
			return errors.New("YAML import exceeds 16384 nodes")
		}
		if visiting[node] {
			return fmt.Errorf("%s: cyclic YAML alias", field)
		}
		visiting[node] = true
		defer delete(visiting, node)
		if node.Kind == yaml.AliasNode {
			return visit(node.Alias, field)
		}
		if node.Kind == yaml.MappingNode {
			values := map[string]*yaml.Node{}
			for i := 0; i+1 < len(node.Content); i += 2 {
				key := node.Content[i].Value
				if _, ok := values[key]; ok {
					return fmt.Errorf("%s.%s: duplicate YAML key", field, key)
				}
				values[key] = node.Content[i+1]
			}
			kind := ""
			if value := values["type"]; value != nil {
				kind = value.Value
			}
			for key, value := range values {
				location := strings.TrimPrefix(field+"."+key, ".")
				file := false
				switch key {
				case "certificate", "private-key", "client-auth-cert", "ca", "ca-file", "ech-key":
					file = value.Kind == yaml.ScalarNode && value.Value != "" && !strings.Contains(value.Value, "-----BEGIN ")
				case "path":
					if strings.HasPrefix(field, "proxy-providers.") || strings.HasPrefix(field, "rule-providers.") {
						if kind != "file" {
							return fmt.Errorf("%s: provider cache requires a managed writable resource mapping", location)
						}
						file = true
					}
				case "external-ui":
					if value.Value != "" {
						return fmt.Errorf("%s: directory resource requires a managed directory mapping", location)
					}
				case "external-controller-unix", "external-controller-pipe", "external-doh-server", "tun":
					if value.Value != "" || len(value.Content) > 0 {
						return fmt.Errorf("%s: runtime listener or device requires a verified managed mapping", location)
					}
				case "geo-auto-update":
					if value.Value == "true" {
						return fmt.Errorf("%s: geodata updater requires a managed writable asset mapping", location)
					}
				case "plugin", "script", "include":
					if value.Value != "" || len(value.Content) > 0 {
						return fmt.Errorf("%s: executable or included configuration requires a verified dependency mapping", location)
					}
				}
				if file {
					if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
						return fmt.Errorf("%s: expected file path", location)
					}
					target := value
					refs = append(refs, importFileReference{path: value.Value, field: location, set: func(path string) { target.Value = path; target.Style = yaml.DoubleQuotedStyle }})
				}
			}
			for i := 0; i+1 < len(node.Content); i += 2 {
				if err := visit(node.Content[i+1], strings.TrimPrefix(field+"."+node.Content[i].Value, ".")); err != nil {
					return err
				}
			}
		} else {
			if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
				upper := strings.ToUpper(node.Value)
				if (strings.HasPrefix(field, "rules[") || strings.HasPrefix(field, "sub-rules.") || strings.HasPrefix(field, "payload[")) && (strings.Contains(upper, "GEOIP,") || strings.Contains(upper, "GEOSITE,") || strings.Contains(upper, "IP-ASN,")) {
					return fmt.Errorf("%s: implicit geodata requires a verified managed home and update mapping", field)
				}
			}
			for i, child := range node.Content {
				if err := visit(child, fmt.Sprintf("%s[%d]", field, i)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	root := document
	if document.Kind == yaml.DocumentNode && len(document.Content) == 1 {
		root = document.Content[0]
	}
	if err := visit(root, ""); err != nil {
		return nil, err
	}
	return refs, nil
}

func prepareMihomoDependencyImport(existing, managed EngineSpec, content string) (coreImportPlan, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return coreImportPlan{}, err
	}
	refs, err := importYAMLReferences(&document)
	if err != nil {
		return coreImportPlan{}, err
	}
	// File providers can themselves contain certificate or executable inputs.
	// Do not treat an opaque provider copy as a complete dependency graph.
	for _, ref := range refs {
		if !strings.HasPrefix(ref.field, "proxy-providers.") && !strings.HasPrefix(ref.field, "rule-providers.") {
			continue
		}
		path := ref.path
		if !filepath.IsAbs(path) {
			base := existing.WorkingDirectory
			if base == "" {
				base = filepath.Dir(existing.ConfigPath)
			}
			path = filepath.Join(base, path)
		}
		data, _, err := readImportSource(path, ouSBResourceLimit)
		if err != nil {
			return coreImportPlan{}, fmt.Errorf("%s: %w", ref.field, err)
		}
		var provider yaml.Node
		if err := yaml.Unmarshal(data, &provider); err != nil {
			return coreImportPlan{}, fmt.Errorf("%s: provider data could not be interpreted", ref.field)
		}
		nested, err := importYAMLReferences(&provider)
		if err != nil {
			return coreImportPlan{}, fmt.Errorf("%s: %w", ref.field, err)
		}
		if len(nested) > 0 {
			return coreImportPlan{}, fmt.Errorf("%s: nested resource %s requires a provider rewrite mapping", ref.field, nested[0].field)
		}
	}
	return prepareFileDependencyImport(core.EngineMihomo, existing, managed, content, refs, func() (string, error) {
		var buffer bytes.Buffer
		encoder := yaml.NewEncoder(&buffer)
		encoder.SetIndent(2)
		if err := encoder.Encode(&document); err != nil {
			return "", err
		}
		return buffer.String(), encoder.Close()
	})
}
