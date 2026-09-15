package api

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

func (s *Server) availableSubStoreProfiles(ctx context.Context, selections []core.SubStoreSyncSelection) ([]subStoreSyncProfile, error) {
	entries, err := s.clientAccessEntries(ctx)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]core.SubStoreSyncSelection, len(selections))
	for _, selection := range selections {
		selected[selection.Key()] = selection
	}
	profiles := make([]subStoreSyncProfile, 0)
	available := make(map[string]struct{})
	for _, entry := range entries {
		displayName := entry.AgentName
		for _, item := range entry.Profiles {
			if !item.Profile.SubscriptionCompatible {
				continue
			}
			profile := subStoreSyncProfile{
				AgentID: entry.AgentID, ConfigID: entry.ConfigID, AgentName: displayName, AgentStatus: entry.AgentStatus, Engine: entry.Engine,
				ProfileTag: item.Tag, Protocol: item.Protocol, Port: item.Port, URI: item.Profile.URI, Available: true,
				Mihomo: item.Profile.Mihomo, MihomoError: item.Profile.MihomoError,
				AddressMode: core.SubStoreAddressModeAuto,
				DefaultName: strings.TrimSpace(displayName + " · " + item.Tag),
			}
			if item.NameOverridden {
				profile.DefaultName = item.ClientName
				if profile.DefaultName == "" {
					profile.DefaultName = item.Tag
				}
			}
			for _, option := range entry.AddressOptions {
				for _, candidate := range option.Profiles {
					if candidate.Tag == item.Tag {
						profile.Addresses = append(profile.Addresses, subStoreSyncAddress{
							Address: option.Address, Source: option.Source, Family: option.Family, URI: candidate.Profile.URI, Mihomo: candidate.Profile.Mihomo,
						})
						break
					}
				}
			}
			if len(profile.Addresses) == 0 {
				// A manual address is not one of the automatic candidates, so this
				// endpoint stays reachable only through its own pinned address.
				source := entry.Source
				if item.AddressOverridden {
					source = "手动设置"
				}
				profile.Addresses = []subStoreSyncAddress{{
					Address: item.Address, Source: source, Family: clientAddressFamily(item.Address), URI: item.Profile.URI, Mihomo: item.Profile.Mihomo,
				}}
			}
			if selection, ok := selected[profile.key()]; ok {
				profile.Selected = true
				profile.CustomName = selection.CustomName
				profile.AddressMode, _ = core.NormalizeSubStoreAddressMode(selection.AddressMode)
			}
			available[profile.key()] = struct{}{}
			profiles = append(profiles, profile)
		}
	}
	for _, selection := range selections {
		if _, ok := available[selection.Key()]; ok {
			continue
		}
		profiles = append(profiles, subStoreSyncProfile{
			AgentID: selection.AgentID, ConfigID: selection.ConfigID, Engine: selection.Engine, ProfileTag: selection.ProfileTag,
			DefaultName: selection.CustomName, CustomName: selection.CustomName, AddressMode: selection.AddressMode,
			Addresses: []subStoreSyncAddress{}, Selected: true, Available: false,
		})
	}
	sort.SliceStable(profiles, func(left, right int) bool {
		if profiles[left].AgentName != profiles[right].AgentName {
			return profiles[left].AgentName < profiles[right].AgentName
		}
		if profiles[left].Engine != profiles[right].Engine {
			return profiles[left].Engine < profiles[right].Engine
		}
		return profiles[left].ProfileTag < profiles[right].ProfileTag
	})
	return profiles, nil
}

func subStoreProfileAddress(profile subStoreSyncProfile, family string) (subStoreSyncAddress, bool) {
	for _, address := range profile.Addresses {
		if address.Family == family {
			return address, true
		}
	}
	return subStoreSyncAddress{}, false
}

func subStoreProfileSupportsMode(profile subStoreSyncProfile, mode string) bool {
	switch mode {
	case core.SubStoreAddressModeAuto:
		return strings.TrimSpace(profile.URI) != ""
	case core.SubStoreAddressModeIPv4, core.SubStoreAddressModeIPv6:
		_, available := subStoreProfileAddress(profile, mode)
		return available
	case core.SubStoreAddressModeBoth:
		_, ipv4 := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv4)
		_, ipv6 := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv6)
		return ipv4 && ipv6
	default:
		return false
	}
}

func subStoreIPv6NodeName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(strings.ToLower(name), " v6") {
		return name
	}
	const suffix = " v6"
	runes := []rune(name)
	if len(runes)+utf8.RuneCountInString(suffix) > 100 {
		runes = runes[:100-utf8.RuneCountInString(suffix)]
		name = strings.TrimSpace(string(runes))
	}
	return name + suffix
}

func subStoreNodesForSelection(profile subStoreSyncProfile, selection core.SubStoreSyncSelection, formats ...string) ([]string, error) {
	format := core.SubStoreSyncFormatURL
	if len(formats) > 0 {
		var valid bool
		format, valid = core.NormalizeSubStoreSyncFormat(formats[0])
		if !valid {
			return nil, errors.New("Sub-Store 同步格式必须是 url 或 mihomo")
		}
	}
	if format == core.SubStoreSyncFormatMihomo {
		if profile.MihomoError != "" {
			return nil, errors.New(profile.MihomoError)
		}
		profile.URI = profile.Mihomo
		profile.Addresses = append([]subStoreSyncAddress(nil), profile.Addresses...)
		for index := range profile.Addresses {
			profile.Addresses[index].URI = profile.Addresses[index].Mihomo
		}
	}
	mode, valid := core.NormalizeSubStoreAddressMode(selection.AddressMode)
	if !valid || !subStoreProfileSupportsMode(profile, mode) {
		return nil, errors.New("所选 IP 地址已不可用，请重新选择地址模式")
	}
	type candidate struct {
		uri  string
		name string
	}
	candidates := make([]candidate, 0, 2)
	switch mode {
	case core.SubStoreAddressModeAuto:
		candidates = append(candidates, candidate{uri: profile.URI, name: selection.CustomName})
	case core.SubStoreAddressModeIPv4:
		address, _ := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv4)
		candidates = append(candidates, candidate{uri: address.URI, name: selection.CustomName})
	case core.SubStoreAddressModeIPv6:
		address, _ := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv6)
		candidates = append(candidates, candidate{uri: address.URI, name: subStoreIPv6NodeName(selection.CustomName)})
	case core.SubStoreAddressModeBoth:
		ipv4, _ := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv4)
		ipv6, _ := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv6)
		candidates = append(candidates,
			candidate{uri: ipv4.URI, name: selection.CustomName},
			candidate{uri: ipv6.URI, name: subStoreIPv6NodeName(selection.CustomName)},
		)
	}
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		renamed, err := renameSubStoreNode(candidate.uri, candidate.name)
		if err != nil {
			return nil, err
		}
		result = append(result, renamed)
	}
	return result, nil
}

func renameSubStoreNode(rawValue, name string) (string, error) {
	rawValue = strings.TrimSpace(rawValue)
	name = strings.TrimSpace(name)
	if rawValue == "" || strings.ContainsAny(rawValue, "\r\n") {
		return "", errors.New("客户端分享值无效")
	}
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsAny(name, "#\r\n") {
		return "", errors.New("节点名称不能为空、不能超过 100 个字符且不能包含 #")
	}
	if config, ok := subStoreSurgeConfig(rawValue); ok {
		name = strings.TrimSpace(strings.NewReplacer("=", "", ",", "").Replace(name))
		if name == "" {
			return "", errors.New("Surge 节点名称删除保留符号后不能为空")
		}
		return name + " = " + config, nil
	}
	if document, nameNode, ok := subStoreMihomoNode(rawValue); ok {
		nameNode.Value = name
		nameNode.Tag = "!!str"
		setSubStoreYAMLFlowStyle(document)
		encoded, err := yaml.Marshal(document)
		if err != nil {
			return "", errors.New("无法编码 Mihomo 节点配置")
		}
		return strings.TrimSpace(string(encoded)), nil
	}
	parsed, err := url.Parse(rawValue)
	if err != nil || parsed.Scheme == "" {
		return "", errors.New("Sub-Store 无法识别客户端分享值")
	}
	if fragment := strings.IndexByte(rawValue, '#'); fragment >= 0 {
		rawValue = rawValue[:fragment]
	}
	return rawValue + "#" + name, nil
}

func subStoreSurgeConfig(rawValue string) (string, bool) {
	_, config, found := strings.Cut(rawValue, "=")
	if !found {
		return "", false
	}
	config = strings.TrimSpace(config)
	proxyType, _, found := strings.Cut(config, ",")
	return config, found && strings.EqualFold(strings.TrimSpace(proxyType), "snell")
}

func subStoreMihomoNode(rawValue string) (*yaml.Node, *yaml.Node, bool) {
	rawValue = strings.TrimSpace(rawValue)
	if len(rawValue) > 64<<10 || !strings.HasPrefix(rawValue, "{") || !strings.HasSuffix(rawValue, "}") {
		return nil, nil, false
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(rawValue), &document); err != nil || len(document.Content) != 1 {
		return nil, nil, false
	}
	mapping := document.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil, nil, false
	}
	var nameNode, typeNode *yaml.Node
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		switch mapping.Content[index].Value {
		case "name":
			nameNode = mapping.Content[index+1]
		case "type":
			typeNode = mapping.Content[index+1]
		}
	}
	if nameNode == nil || typeNode == nil || strings.TrimSpace(typeNode.Value) == "" {
		return nil, nil, false
	}
	return &document, nameNode, true
}

func setSubStoreYAMLFlowStyle(node *yaml.Node) {
	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode {
		node.Style |= yaml.FlowStyle
	}
	for _, child := range node.Content {
		setSubStoreYAMLFlowStyle(child)
	}
}
