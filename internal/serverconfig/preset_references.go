package serverconfig

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

// Renaming/deleting a preset must not leave inbound selectors pointing at the
// old identity. Removing the last selector removes its rule, never its scope
// alone (which would accidentally turn a restricted rule into a global rule).
func ReconcilePresetInboundReferences(engine core.Engine, content, previous, next string) (string, error) {
	if previous == "" || previous == next || engine == core.EngineShadowsocksRust {
		return content, nil
	}
	var root map[string]any
	if engine == core.EngineMihomo {
		if err := yaml.Unmarshal([]byte(content), &root); err != nil {
			return "", err
		}
	} else {
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&root); err != nil {
			return "", err
		}
	}
	keys, selector := []string{"route", "dns"}, "inbound"
	if engine == core.EngineXray {
		keys, selector = []string{"routing"}, "inboundTag"
	}
	if engine == core.EngineMihomo {
		keys = []string{"rules"}
	}
	inboundReference := regexp.MustCompile(`(?:^|[,(])\s*IN-NAME\s*,\s*` + regexp.QuoteMeta(previous) + `\s*[,)]`)
	containsName := func(value any) bool {
		switch names := value.(type) {
		case string:
			return names == previous
		case []any:
			for _, name := range names {
				if name == previous {
					return true
				}
			}
		}
		return false
	}
	if engine == core.EngineMihomo {
		if subrules, present := root["sub-rules"]; present {
			data, _ := json.Marshal(subrules)
			if inboundReference.Match(data) {
				return "", fmt.Errorf("入站 %s 被子规则引用，请在完整源码中同时调整", previous)
			}
		}
	}
	for _, key := range keys {
		section := mapValue(root[key])
		rules, _ := section["rules"].([]any)
		if engine == core.EngineMihomo {
			rules, _ = root[key].([]any)
		}
		changed := false
		kept := []any{}
		for _, raw := range rules {
			if engine == core.EngineMihomo {
				rule := stringValue(raw)
				parts := strings.Split(rule, ",")
				if len(parts) >= 3 && strings.TrimSpace(parts[0]) == "IN-NAME" && strings.TrimSpace(parts[1]) == previous {
					changed = true
					if next == "" {
						continue
					}
					parts[1] = next
					raw = strings.Join(parts, ",")
				} else if inboundReference.MatchString(rule) {
					return "", fmt.Errorf("入站 %s 被复合路由引用，请在完整源码中同时调整名称与路由", previous)
				}
			} else {
				rule := mapValue(raw)
				value, present := rule[selector]
				if present && containsName(value) {
					if rule["invert"] == true && next == "" {
						return "", fmt.Errorf("入站 %s 被反向匹配规则引用，请在完整源码中同时调整", previous)
					}
					switch names := value.(type) {
					case string:
						changed = true
						if next == "" {
							continue
						}
						rule[selector] = next
					case []any:
						// An empty list is already unscoped; leave it unchanged.
						if len(names) > 0 {
							updated := []any{}
							for _, name := range names {
								if name == previous {
									changed = true
									if next != "" {
										updated = append(updated, next)
									}
								} else {
									updated = append(updated, name)
								}
							}
							if len(updated) == 0 {
								continue
							}
							rule[selector] = updated
						}
					}
				}
				// Logical DNS selectors have different AND/OR/invert semantics.
				// Keep unrelated logical rules but refuse ambiguous rewrites.
				if nested, exists := rule["rules"]; exists {
					var contains func(any) bool
					contains = func(value any) bool {
						switch value := value.(type) {
						case []any:
							for _, child := range value {
								if contains(child) {
									return true
								}
							}
						case map[string]any:
							if names, ok := value[selector]; ok && containsName(names) {
								return true
							}
							return contains(value["rules"])
						}
						return false
					}
					if contains(nested) {
						return "", fmt.Errorf("入站 %s 被复合 DNS/路由引用，请在完整源码中同时调整", previous)
					}
				}
			}
			kept = append(kept, raw)
		}
		if !changed {
			continue
		}
		var value any = kept
		if engine != core.EngineMihomo {
			section["rules"] = kept
			value = section
		}
		var data []byte
		var err error
		if engine == core.EngineMihomo {
			data, err = yaml.Marshal(value)
		} else {
			data, err = json.Marshal(value)
		}
		if err != nil {
			return "", err
		}
		content, err = configschema.MergeFragment(engine, content, key, string(data), false)
		if err != nil {
			return "", err
		}
	}
	return content, nil
}
