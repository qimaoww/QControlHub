package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Xray external geodata paths are relative to the executable's asset root,
// not to its configuration. Keep the core's ext rule and selector intact.
func importXrayAssetReferences(root map[string]any, existing, managed EngineSpec) ([]importFileReference, error) {
	var refs []importFileReference
	var visit func(any, string) error
	visit = func(value any, field string) error {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if key == "lua" || key == "script" || key == "scriptFile" {
					if child != nil && !emptyImportString(child) {
						return fmt.Errorf("%s.%s: executable dependency requires a verified runtime mapping", field, key)
					}
				}
				if err := visit(child, strings.TrimPrefix(field+"."+key, ".")); err != nil {
					return err
				}
			}
		case []any:
			for i, child := range node {
				text, ok := child.(string)
				relevant := field == "routing.domain" || strings.HasSuffix(field, ".domain") || strings.HasSuffix(field, ".domains") || strings.HasSuffix(field, ".ip") || strings.HasSuffix(field, ".source") || strings.HasSuffix(field, ".sourceIP") || strings.HasSuffix(field, ".sourceIPs") || strings.HasSuffix(field, ".expectIPs") || strings.HasSuffix(field, ".skipFallback")
				if ok && relevant {
					prefix := ""
					rest := text
					for strings.HasPrefix(rest, "!") {
						prefix += "!"
						rest = strings.TrimPrefix(rest, "!")
					}
					var file, selector string
					switch {
					case strings.HasPrefix(rest, "geoip:"):
						file = "geoip.dat"
						selector = strings.TrimPrefix(rest, "geoip:")
					case strings.HasPrefix(rest, "geosite:"):
						file = "geosite.dat"
						selector = strings.TrimPrefix(rest, "geosite:")
					case strings.HasPrefix(rest, "ext:") || strings.HasPrefix(rest, "ext-ip:") || strings.HasPrefix(rest, "ext-domain:") || strings.HasPrefix(rest, "ext-site:"):
						_, tail, _ := strings.Cut(rest, ":")
						file, selector, ok = strings.Cut(tail, ":")
						if !ok {
							return fmt.Errorf("%s[%d]: malformed external asset selector", field, i)
						}
					}
					if file != "" {
						if !filepath.IsLocal(file) {
							return fmt.Errorf("%s[%d]: asset path escapes its service asset directory", field, i)
						}
						path := filepath.Join(filepath.Dir(existing.Binary), file)
						// Xray's asset resolver tests the executable directory and
						// then its documented shared-data locations, in this order.
						for _, root := range []string{filepath.Dir(existing.Binary), "/usr/local/share/xray", "/usr/share/xray", "/opt/share/xray"} {
							candidate := filepath.Join(root, file)
							if _, err := os.Stat(candidate); err == nil {
								path = candidate
								break
							} else if !os.IsNotExist(err) {
								return fmt.Errorf("%s[%d]: inspect asset: %w", field, i, err)
							}
						}
						index := i
						rulePrefix := prefix
						ruleSelector := selector
						refs = append(refs, importFileReference{path: path, field: fmt.Sprintf("%s[%d]", field, i), set: func(destination string) {
							relative, _ := filepath.Rel(filepath.Dir(managed.Binary), destination)
							node[index] = rulePrefix + "ext:" + filepath.ToSlash(relative) + ":" + ruleSelector
						}})
					}
				}
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

func rewriteXrayPreviewAssets(root map[string]any, paths map[string]string) {
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for _, child := range node {
				visit(child)
			}
		case []any:
			for i, child := range node {
				if text, ok := child.(string); ok {
					prefix := text[:len(text)-len(strings.TrimLeft(text, "!"))]
					rule := strings.TrimLeft(text, "!")
					if strings.HasPrefix(rule, "ext:") {
						file, selector, ok := strings.Cut(strings.TrimPrefix(rule, "ext:"), ":")
						if ok {
							for original, target := range paths {
								if strings.HasSuffix(filepath.ToSlash(original), "/"+file) {
									node[i] = prefix + "ext:" + filepath.Base(target) + ":" + selector
									break
								}
							}
						}
					}
				}
				visit(child)
			}
		}
	}
	visit(root)
}
