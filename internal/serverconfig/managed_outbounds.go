package serverconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// Outbound fragments are full native objects. Unrelated raw JSON numbers and
// YAML nodes survive an edit; neither list position nor remote port is identity.
func ManagedOutboundFragment(engine core.Engine, source, section, tag string) (string, error) {
	if inbound, ok := managedSections(engine)[section]; !ok || inbound {
		return "", errors.New("出站配置区域无效")
	}
	root, err := managedDocument(engine, source)
	if err != nil {
		return "", err
	}
	values, _ := root[section].([]any)
	found := -1
	for i, value := range values {
		if managedTag(engine, value) == tag {
			if found >= 0 {
				return "", errors.New("出站标签不唯一")
			}
			found = i
		}
	}
	if found < 0 || managedInternalTag(tag) {
		return "", errors.New("出站不存在或由系统管理")
	}
	fragment, _, err := configschema.Fragment(engine, source, section)
	if err != nil {
		return "", err
	}
	if engine == core.EngineMihomo {
		var document yaml.Node
		if err = yaml.Unmarshal([]byte(fragment), &document); err != nil {
			return "", err
		}
		encoded, err := yaml.Marshal(document.Content[0].Content[found])
		return string(encoded), err
	}
	var entries []json.RawMessage
	if err = json.Unmarshal([]byte(fragment), &entries); err != nil {
		return "", err
	}
	return string(entries[found]), nil
}

func MutateManagedOutbound(engine core.Engine, source, section, original, fragment, operation string, state ManagedState) (string, ManagedState, error) {
	fail := func(err error) (string, ManagedState, error) { return "", ManagedState{}, err }
	if inbound, ok := managedSections(engine)[section]; !ok || inbound {
		return fail(errors.New("出站配置区域无效"))
	}
	if operation != "add" && operation != "modify" && operation != "delete" {
		return fail(errors.New("出站操作无效"))
	}
	var err error
	if strings.Contains(source, accountingPrefix) {
		source, err = PresetAccountingSource(engine, source)
		if err != nil {
			return fail(err)
		}
	}
	root, err := managedDocument(engine, source)
	if err != nil {
		return fail(err)
	}
	values, _ := root[section].([]any)
	index := -1
	for i, value := range values {
		if managedTag(engine, value) == original {
			if index >= 0 {
				return fail(errors.New("出站标签不唯一"))
			}
			index = i
		}
	}
	if operation != "add" && (index < 0 || managedInternalTag(original)) {
		return fail(errors.New("出站已变化，请重新读取"))
	}
	var next map[string]any
	nextTag := ""
	if operation != "delete" {
		next, err = managedDocument(engine, fragment)
		if err != nil {
			return fail(err)
		}
		nextTag = managedTag(engine, next)
		if nextTag == "" || nextTag != strings.TrimSpace(nextTag) || managedInternalTag(nextTag) {
			return fail(errors.New("请填写唯一出站名称，不能使用系统名称"))
		}
		for _, key := range []string{"outbounds", "proxies", "proxy-groups"} {
			values, _ := root[key].([]any)
			for i, value := range values {
				if managedTag(engine, value) == nextTag && (key != section || i != index || operation == "add") {
					return fail(errors.New("出站名称已存在"))
				}
			}
		}
	}
	if operation == "delete" {
		// Deletion must retain an explicit replacement path for every consumer.
		if _, err = CompileManagedConfig(engine, source, ManagedState{Disabled: []ManagedResource{{section, original}}}); err != nil {
			return fail(err)
		}
	}
	if operation != "add" && operation != "delete" && nextTag == original {
		// A shared exit edit affects every consumer; return the affected identity
		// to the caller's confirmation instead of selecting a fallback exit.
		if engine == core.EngineXray && stringValue(next["protocol"]) == "" || engine == core.EngineSingBox && stringValue(next["type"]) == "" {
			return fail(errors.New("出站协议不能为空"))
		}
	}
	raw, present, err := configschema.Fragment(engine, source, section)
	if err != nil {
		return fail(err)
	}
	if !present {
		raw = "[]"
	}
	var updated string
	if engine == core.EngineMihomo {
		var document yaml.Node
		if err = yaml.Unmarshal([]byte(raw), &document); err != nil {
			return fail(err)
		}
		if len(document.Content) != 1 || document.Content[0].Kind != yaml.SequenceNode {
			return fail(errors.New("出站区域必须是列表"))
		}
		entries := document.Content[0]
		var replacement yaml.Node
		if operation != "delete" {
			if err = yaml.Unmarshal([]byte(fragment), &replacement); err != nil {
				return fail(err)
			}
		}
		switch operation {
		case "add":
			entries.Content = append(entries.Content, replacement.Content[0])
		case "modify":
			entries.Content[index] = replacement.Content[0]
		case "delete":
			entries.Content = append(entries.Content[:index], entries.Content[index+1:]...)
		}
		encoded, err := yaml.Marshal(entries)
		if err != nil {
			return fail(err)
		}
		updated = string(encoded)
	} else {
		var entries []json.RawMessage
		if err = json.Unmarshal([]byte(raw), &entries); err != nil {
			return fail(err)
		}
		switch operation {
		case "add":
			entries = append(entries, json.RawMessage(fragment))
		case "modify":
			entries[index] = json.RawMessage(fragment)
		case "delete":
			entries = append(entries[:index], entries[index+1:]...)
		}
		if entries == nil {
			entries = []json.RawMessage{}
		}
		encoded, err := json.Marshal(entries)
		if err != nil {
			return fail(err)
		}
		updated = string(encoded)
	}
	result, err := configschema.MergeFragment(engine, source, section, updated, false)
	if err != nil {
		return fail(err)
	}
	if operation == "modify" && nextTag != original {
		result, err = RewriteOutboundReferences(engine, result, original, nextTag)
		if err != nil {
			return fail(err)
		}
	}
	remapped := ManagedState{Disabled: []ManagedResource{}}
	for _, ref := range state.Disabled {
		if ref.Section == section && ref.Tag == original && operation != "add" {
			if operation == "delete" {
				continue
			}
			ref.Tag = nextTag
		}
		remapped.Disabled = append(remapped.Disabled, ref)
	}
	if _, err = CompileManagedConfig(engine, result, remapped); err != nil {
		return fail(fmt.Errorf("出站状态校验失败: %w", err))
	}
	return result, remapped, nil
}

func managedInternalTag(tag string) bool {
	return strings.HasPrefix(tag, accountingPrefix) || strings.HasPrefix(tag, "qch-stat-")
}
