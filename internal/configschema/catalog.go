// Package configschema describes the configuration surface exposed by the
// supported cores. Each engine catalog lives in its own module.
package configschema

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Field is a top-level configuration key. Complex values are edited as a
// complete YAML/JSON fragment so nested and newly introduced keys are never
// discarded by a smaller local struct.
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Docs        string `json:"docs"`
}

type Topic struct {
	Label string `json:"label"`
	Docs  string `json:"docs"`
}

type TopicGroup struct {
	Name   string  `json:"name"`
	Topics []Topic `json:"topics"`
}

type Catalog struct {
	Engine      core.Engine  `json:"engine"`
	Name        string       `json:"name"`
	Format      string       `json:"format"`
	Source      string       `json:"source"`
	SourceLabel string       `json:"source_label"`
	Fields      []Field      `json:"fields"`
	TopicGroups []TopicGroup `json:"topic_groups"`
	TopicCount  int          `json:"topic_count"`
}

// CatalogFor returns a catalog assembled from first-party documentation
// indexes reviewed on 2026-07-31.
func CatalogFor(engine core.Engine) (Catalog, error) {
	var catalog Catalog
	switch engine {
	case core.EngineMihomo:
		catalog = Catalog{
			Engine: engine, Name: "Mihomo", Format: "YAML",
			Source: "https://wiki.metacubex.one/config/", SourceLabel: "Mihomo 官方 Wiki",
			Fields: mihomoFields(), TopicGroups: buildTopics("https://wiki.metacubex.one/config/", mihomoTopics),
		}
	case core.EngineXray:
		catalog = Catalog{
			Engine: engine, Name: "Xray", Format: "JSON",
			Source: "https://xtls.github.io/config/", SourceLabel: "Xray-core 官方配置文档",
			Fields: xrayFields(), TopicGroups: buildTopics("https://xtls.github.io/config/", xrayTopics),
		}
	case core.EngineSingBox:
		catalog = Catalog{
			Engine: engine, Name: "sing-box", Format: "JSON",
			Source: "https://sing-box.sagernet.org/configuration/", SourceLabel: "sing-box 官方配置文档与 Schema",
			Fields: singBoxFields(), TopicGroups: buildTopics("https://sing-box.sagernet.org/configuration/", singBoxTopics),
		}
	case core.EngineShadowsocksRust:
		catalog = Catalog{
			Engine: engine, Name: "Shadowsocks Rust", Format: "JSON",
			Source:      "https://github.com/shadowsocks/shadowsocks-rust/blob/master/crates/shadowsocks-service/src/config.rs",
			SourceLabel: "Shadowsocks Rust 官方配置定义",
			Fields:      shadowsocksRustFields(),
			TopicGroups: buildTopics("https://shadowsocks.org/doc/", shadowsocksRustTopics),
		}
	default:
		return Catalog{}, fmt.Errorf("unsupported engine %q", engine)
	}
	for _, group := range catalog.TopicGroups {
		catalog.TopicCount += len(group.Topics)
	}
	return catalog, nil
}

func field(key, label, kind, description, docs string) Field {
	return Field{Key: key, Label: label, Kind: kind, Description: description, Docs: docs}
}

func buildTopics(base, raw string) []TopicGroup {
	groups := make(map[string][]Topic)
	for _, item := range strings.Fields(raw) {
		item = strings.Trim(item, "/")
		if item == "" {
			continue
		}
		parts := strings.Split(item, "/")
		group := topicLabel(parts[0])
		label := topicLabel(path.Base(item))
		if len(parts) == 1 {
			label = "概览"
		}
		docPath := item + "/"
		if strings.HasSuffix(item, ".html") {
			docPath = item
		}
		groups[group] = append(groups[group], Topic{Label: label, Docs: base + docPath})
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]TopicGroup, 0, len(names))
	for _, name := range names {
		result = append(result, TopicGroup{Name: name, Topics: groups[name]})
	}
	return result
}

func topicLabel(value string) string {
	value = strings.TrimSuffix(value, ".html")
	known := map[string]string{
		"dns": "DNS", "ntp": "NTP", "tls": "TLS", "api": "API", "tun": "TUN",
		"inbound": "入站", "inbounds": "入站", "outbound": "出站", "outbounds": "出站",
		"proxies": "代理协议", "proxy-groups": "策略组", "proxy-providers": "代理提供者",
		"rule-providers": "规则提供者", "rules": "路由规则", "route": "路由",
		"transports": "传输", "transport": "传输", "listeners": "监听器",
		"experimental": "实验功能", "shared": "共享选项", "service": "服务",
		"endpoint": "端点", "network-namespace": "网络命名空间", "features": "功能详解",
	}
	if label, ok := known[value]; ok {
		return label
	}
	return strings.ToUpper(strings.ReplaceAll(value, "-", " "))
}
