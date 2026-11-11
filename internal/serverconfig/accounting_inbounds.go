package serverconfig

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Normalize only listener identities and static Xray port lists. The accounting
// compiler remains responsible for routes, generated resources and exit checks.
// Work on the decoded candidate so a rejected conversion cannot partially save.
func normalizeAccountingInbounds(engine core.Engine, root map[string]any) error {
	if engine == core.EngineShadowsocksRust {
		return nil
	}
	sections, tagKey, portKey := []string{"inbounds"}, "tag", "port"
	if engine == core.EngineSingBox {
		sections, portKey = []string{"inbounds", "endpoints"}, "listen_port"
	}
	if engine == core.EngineMihomo {
		sections, tagKey = []string{"listeners"}, "name"
	}
	// Reserve references too: a synthesized name must not capture a dangling rule.
	used := map[string]bool{"qch-stat-api": true, "qch-stat-block": true}
	var reserve func(any)
	reserve = func(v any) {
		switch v := v.(type) {
		case string:
			used[v] = true
			if engine == core.EngineMihomo {
				for _, token := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '(' || r == ')' }) {
					used[strings.TrimSpace(token)] = true
				}
			}
		case []any:
			for _, x := range v {
				reserve(x)
			}
		case map[string]any:
			for _, x := range v {
				reserve(x)
			}
		}
	}
	reserve(root)
	counts := map[string]int{}
	internal := map[string]bool{}
	for _, section := range sections {
		entries, _ := root[section].([]any)
		for _, raw := range entries {
			in := mapValue(raw)
			tag := stringValue(in[tagKey])
			if tag != "" {
				counts[tag]++
			}
			if engine == core.EngineXray && xrayInternalAPIInbound(root, in) {
				internal[tag] = true
			}
		}
	}
	newTag := func(port int) string {
		base := fmt.Sprintf("qch-inbound-%d", port)
		tag := base
		for suffix := 2; used[tag]; suffix++ {
			tag = fmt.Sprintf("%s-%d", base, suffix)
		}
		used[tag] = true
		return tag
	}
	replacements := map[string][]string{}
	kept := map[string]bool{}
	seenPorts := map[int]string{}
	count := 0
	for _, section := range sections {
		rawEntries, exists := root[section]
		if !exists {
			continue
		}
		entries, ok := rawEntries.([]any)
		if !ok {
			return fmt.Errorf("%s 必须是入站数组", section)
		}
		result := make([]any, 0, len(entries))
		for index, raw := range entries {
			field := fmt.Sprintf("%s[%d]", section, index)
			in := mapValue(raw)
			if in == nil {
				return fmt.Errorf("%s 必须是入站对象", field)
			}
			tag := stringValue(in[tagKey])
			if value, exists := in[tagKey]; exists && value != "" {
				if _, ok := value.(string); !ok {
					return fmt.Errorf("%s.%s 必须是字符串", field, tagKey)
				}
			}
			if internal[tag] && counts[tag] > 1 {
				return fmt.Errorf("%s.%s %q 与管理 API 入站重名，无法确定路由归属", field, tagKey, tag)
			}
			if engine == core.EngineXray && (xrayInternalAPIInbound(root, in) || xhttpGroupOwner(root, tag) != "") {
				result = append(result, raw)
				continue
			}
			for _, key := range []string{"allocate", "detour", "listen_ports"} {
				if in[key] != nil {
					return fmt.Errorf("%s.%s 使用动态或重定向监听，现有按端口统计无法建立独立归属", field, key)
				}
			}
			ports, err := accountingListenerPorts(engine, in[portKey])
			if err != nil {
				return fmt.Errorf("%s.%s：%w", field, portKey, err)
			}
			for _, port := range ports {
				if previous := seenPorts[port]; previous != "" {
					return fmt.Errorf("%s.%s 与 %s 使用相同端口 %d；现有统计按端口归属，无法区分不同监听地址或协议", field, portKey, previous, port)
				}
				seenPorts[port] = field + "." + portKey
				count++
				if count > maxDiscoveredTrafficPorts {
					return fmt.Errorf("%s.%s：规范化后超过 %d 个统计端口", field, portKey, maxDiscoveredTrafficPorts)
				}
			}
			if tag == "" && accountingInboundReferenced(engine, root, tag) {
				return fmt.Errorf("%s.%s 为空且被规则引用，无法确定原始匹配语义", field, tagKey)
			}
			if counts[tag] > 1 && engine != core.EngineXray && accountingInboundReferenced(engine, root, tag) {
				return fmt.Errorf("%s.%s %q 重复且被规则引用，无法确定目标入站", field, tagKey, tag)
			}
			for i, port := range ports {
				next := tag
				if i > 0 || !tagPattern.MatchString(tag) || kept[tag] {
					next = newTag(port)
				}
				kept[next] = true
				entry := cloneAccountingObject(in)
				if engine == core.EngineMihomo {
					// YAML numeric options must retain their native scalar types.
					entry = in
				}
				entry[tagKey] = next
				entry[portKey] = port
				result = append(result, entry)
				if tag != "" {
					replacements[tag] = append(replacements[tag], next)
				}
			}
		}
		root[section] = result
	}
	return rewriteAccountingInboundReferences(engine, root, replacements)
}

func accountingListenerPorts(engine core.Engine, value any) ([]int, error) {
	if port := trafficPortNumber(value); port != 0 {
		return []int{port}, nil
	}
	if engine != core.EngineXray {
		return nil, fmt.Errorf("需要可识别的单个监听端口（1–65535）")
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return nil, fmt.Errorf("需要可识别的监听端口或静态端口列表（1–65535）")
	}
	ports := []int{}
	seen := map[int]bool{}
	for _, part := range strings.Split(text, ",") {
		bounds := strings.Split(part, "-")
		if len(bounds) > 2 {
			return nil, fmt.Errorf("无法识别静态端口列表")
		}
		first := trafficPortNumber(bounds[0])
		last := first
		if len(bounds) == 2 {
			last = trafficPortNumber(bounds[1])
		}
		if first == 0 || last == 0 || first > last {
			return nil, fmt.Errorf("端口列表或范围无效，需要 1–65535 内的递增范围")
		}
		if len(ports)+last-first+1 > maxDiscoveredTrafficPorts {
			return nil, fmt.Errorf("端口列表超过 %d 个统计端口", maxDiscoveredTrafficPorts)
		}
		for port := first; port <= last; port++ {
			if seen[port] {
				return nil, fmt.Errorf("端口列表重复包含 %d", port)
			}
			seen[port] = true
			ports = append(ports, port)
		}
	}
	return ports, nil
}

// A selector names a set. Xray permits repeated inbound tags; expanding that
// set when splitting/renaming preserves the scope and order of each rule.
func accountingInboundSelectors(engine core.Engine, root map[string]any, visit func(map[string]any, string)) {
	selector, sections := "inbound", []string{"route", "dns"}
	if engine == core.EngineXray {
		selector, sections = "inboundTag", []string{"routing"}
	}
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case []any:
			for _, x := range v {
				walk(x)
			}
		case map[string]any:
			if _, ok := v[selector]; ok {
				visit(v, selector)
			}
			walk(v["rules"])
		}
	}
	for _, section := range sections {
		walk(mapValue(root[section])["rules"])
	}
	if engine == core.EngineSingBox {
		if stats := mapValue(mapValue(mapValue(root["experimental"])["v2ray_api"])["stats"]); stats != nil {
			if _, ok := stats["inbounds"]; ok {
				visit(stats, "inbounds")
			}
		}
	}
}

func accountingInboundReferenced(engine core.Engine, root map[string]any, tag string) bool {
	found := false
	if engine == core.EngineMihomo {
		rules, _ := root["rules"].([]any)
		for _, raw := range rules {
			if mihomoInboundSelector(tag).MatchString(stringValue(raw)) {
				return true
			}
		}
		return false
	}
	accountingInboundSelectors(engine, root, func(rule map[string]any, key string) {
		if rule[key] == tag {
			found = true
		}
		if values, ok := rule[key].([]any); ok {
			for _, value := range values {
				if value == tag {
					found = true
				}
			}
		}
	})
	return found
}

func mihomoInboundSelector(tag string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[,(])(\s*IN-NAME\s*,\s*)` + regexp.QuoteMeta(tag) + `(\s*[,)]|\s*$)`)
}

func rewriteAccountingInboundReferences(engine core.Engine, root map[string]any, replacements map[string][]string) error {
	if engine == core.EngineXray {
		// DNS clients inject their own inbound tags. Renaming a proxy listener
		// must retain selectors for DNS requests that share its original tag.
		retainDNS := func(tag string) {
			names := replacements[tag]
			if len(names) == 0 {
				return
			}
			for _, name := range names {
				if name == tag {
					return
				}
			}
			replacements[tag] = append(names, tag)
		}
		dns := mapValue(root["dns"])
		retainDNS(stringValue(dns["tag"]))
		servers, _ := dns["servers"].([]any)
		for _, server := range servers {
			retainDNS(stringValue(mapValue(server)["tag"]))
		}
	}
	if engine == core.EngineMihomo {
		rules, _ := root["rules"].([]any)
		for tag, names := range replacements {
			if len(names) != 1 || names[0] == tag {
				continue
			}
			if strings.ContainsAny(tag, ",()") && accountingInboundReferenced(engine, root, tag) {
				return fmt.Errorf("listeners.name %q 包含规则分隔符，无法安全调整 IN-NAME 引用", tag)
			}
			pattern := mihomoInboundSelector(tag)
			for i, raw := range rules {
				rules[i] = pattern.ReplaceAllStringFunc(stringValue(raw), func(match string) string {
					parts := pattern.FindStringSubmatch(match)
					return parts[1] + parts[2] + names[0] + parts[3]
				})
			}
		}
		return nil
	}
	accountingInboundSelectors(engine, root, func(rule map[string]any, key string) {
		switch values := rule[key].(type) {
		case string:
			if names := replacements[values]; len(names) > 0 && (len(names) != 1 || names[0] != values) {
				if len(names) == 1 {
					rule[key] = names[0]
				} else {
					rule[key] = stringsToAny(names)
				}
			}
		case []any:
			updated := []any{}
			changed := false
			for _, value := range values {
				tag, ok := value.(string)
				if names := replacements[tag]; ok && len(names) > 0 {
					for _, name := range names {
						updated = append(updated, name)
					}
					changed = changed || len(names) != 1 || names[0] != tag
				} else {
					updated = append(updated, value)
				}
			}
			if changed {
				rule[key] = updated
			}
		}
	})
	return nil
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for i, v := range values {
		result[i] = v
	}
	return result
}
