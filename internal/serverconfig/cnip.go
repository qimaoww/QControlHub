package serverconfig

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

// ApplyCNIPPrefixes only replaces QCH-owned rule resources. Preserve JSON
// numbers verbatim during decoding, including identifiers above 2^53.
func ApplyCNIPPrefixes(engine core.Engine, content string, prefixes []string) (string, error) {
	var root map[string]any
	if engine == core.EngineMihomo {
		if err := yaml.Unmarshal([]byte(content), &root); err != nil {
			return "", err
		}
	} else {
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&root); err != nil {
			if engine != core.EngineXray || yaml.Unmarshal([]byte(content), &root) != nil {
				return "", err
			}
		}
	}
	changed := false
	switch engine {
	case core.EngineXray:
		routing := mapValue(root["routing"])
		rules, _ := routing["rules"].([]any)
		for _, v := range rules {
			r := mapValue(v)
			tag := stringValue(r["ruleTag"])
			if strings.HasPrefix(tag, mainlandXraySourcePrefix) && mainlandXrayManagedRule(r, strings.TrimPrefix(tag, mainlandXraySourcePrefix), true) {
				r["source"] = prefixes
				if len(prefixes) == 0 {
					r["source"] = []string{"geoip:cn"}
				}
				changed = true
			}
			if strings.HasPrefix(tag, mainlandXrayDestinationPrefix) && mainlandXrayManagedRule(r, strings.TrimPrefix(tag, mainlandXrayDestinationPrefix), false) {
				r["ip"] = prefixes
				if len(prefixes) == 0 {
					r["ip"] = []string{"geoip:cn"}
				}
				changed = true
			}
		}
	case core.EngineMihomo:
		providers := mapValue(root["rule-providers"])
		for _, tag := range []string{mainlandMihomoProviderTag, mainlandMihomoIPv6ProviderTag} {
			p := mapValue(providers[tag])
			if mainlandMihomoManagedProvider(p) || mainlandMihomoManagedIPv6Provider(p) {
				providers[tag] = map[string]any{"type": "inline", "behavior": "ipcidr", "payload": prefixes}
				if len(prefixes) == 0 {
					url, path, interval := ChinaRoutesURL, "./ruleset/qch-chnroutes2-cn.txt", 3600
					if tag == mainlandMihomoIPv6ProviderTag {
						url, path, interval = ChinaRoutesIPv6URL, "./ruleset/qch-china-cn-ipv6.txt", 86400
					}
					providers[tag] = map[string]any{"type": "http", "behavior": "ipcidr", "format": "text", "url": url, "path": path, "interval": interval}
				}
				changed = true
			}
		}
	case core.EngineSingBox:
		route := mapValue(root["route"])
		sets, _ := route["rule_set"].([]any)
		for i, v := range sets {
			set := mapValue(v)
			if mainlandSingBoxManagedRuleSet(set) {
				sets[i] = map[string]any{"type": "inline", "tag": set["tag"], "rules": []any{map[string]any{"ip_cidr": prefixes}}}
				if len(prefixes) == 0 {
					for _, v := range mainlandSingBoxRuleSets() {
						if mapValue(v)["tag"] == set["tag"] {
							sets[i] = v
						}
					}
				}
				changed = true
			}
		}
	case core.EngineShadowsocksRust:
		return content, nil
	}
	if !changed {
		return content, nil
	}
	var out []byte
	var err error
	if engine == core.EngineMihomo {
		out, err = yaml.Marshal(root)
	} else {
		out, err = json.MarshalIndent(root, "", "  ")
	}
	if len(out) > 2<<20 {
		return "", errors.New("应用 CN IP 后配置超过 2 MiB，请使用更精简的网段源")
	}
	return string(out), err
}
