package serverconfig

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

// Resolve only a visible, uniquely named inbound in this authorized source.
// Native fragments retain custom users, transport fields and exact JSON numbers.
func InboundSource(engine core.Engine, source, tag string) (string, string, error) {
	objects, err := ManagedObjects(engine, source, ManagedState{})
	if err != nil {
		return "", "", err
	}
	section := ""
	for _, object := range objects {
		if object.Inbound && object.Tag == tag {
			if section != "" {
				return "", "", errors.New("入站名称不唯一")
			}
			section = object.Section
		}
	}
	if section == "" {
		return "", "", errors.New("入站不存在或由系统管理")
	}
	root, err := managedDocument(engine, source)
	if err != nil {
		return "", "", err
	}
	if engine == core.EngineShadowsocksRust && root["server_port"] != nil {
		var formatted bytes.Buffer
		err := json.Indent(&formatted, []byte(source), "", "  ")
		return formatted.String(), section, err
	}
	values, _ := root[section].([]any)
	index := -1
	for i, value := range values {
		name := managedTag(engine, value)
		if engine == core.EngineShadowsocksRust {
			name = shadowsocksRustEntryTag(mapValue(value), i)
		}
		if name == tag {
			index = i
			break
		}
	}
	if index < 0 {
		return "", "", errors.New("入站已变化，请重新读取")
	}
	fragment, _, err := configschema.Fragment(engine, source, section)
	if err != nil {
		return "", "", err
	}
	if engine == core.EngineMihomo {
		var doc yaml.Node
		if err = yaml.Unmarshal([]byte(fragment), &doc); err != nil {
			return "", "", err
		}
		value, err := yaml.Marshal(doc.Content[0].Content[index])
		return string(value), section, err
	}
	var entries []json.RawMessage
	if err = json.Unmarshal([]byte(fragment), &entries); err != nil {
		return "", "", err
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, entries[index], "", "  "); err != nil {
		return "", "", err
	}
	return formatted.String(), section, nil
}

// A fragment edits one existing identity, without reconstructing it as a preset.
// Renaming is a separate coordinated source edit so policies and metadata stay bound.
func ReplaceInboundSource(engine core.Engine, source, tag, fragment string) (string, error) {
	_, section, err := InboundSource(engine, source, tag)
	if err != nil {
		return "", err
	}
	next, err := managedDocument(engine, fragment)
	if err != nil {
		return "", err
	}
	name := managedTag(engine, next)
	if engine == core.EngineShadowsocksRust && name == "" {
		name = tag
	}
	if name != tag {
		return "", errors.New("入站名称必须与当前选择一致；重命名请使用完整源码编辑")
	}
	root, err := managedDocument(engine, source)
	if err != nil {
		return "", err
	}
	if engine == core.EngineShadowsocksRust && root["server_port"] != nil {
		if next["server_port"] == nil {
			return "", errors.New("请保留当前单端口配置结构")
		}
		return fragment, nil
	}
	values, _ := root[section].([]any)
	index := -1
	for i, value := range values {
		name := managedTag(engine, value)
		if engine == core.EngineShadowsocksRust {
			name = shadowsocksRustEntryTag(mapValue(value), i)
		}
		if name == tag {
			index = i
			break
		}
	}
	if index < 0 {
		return "", errors.New("入站已变化，请重新读取")
	}
	if engine == core.EngineShadowsocksRust && !managedExplicitSSRustTag(next) {
		next["id"] = tag
		encoded, err := json.Marshal(next)
		if err != nil {
			return "", err
		}
		fragment = string(encoded)
	}
	raw, _, err := configschema.Fragment(engine, source, section)
	if err != nil {
		return "", err
	}
	var updated []byte
	if engine == core.EngineMihomo {
		var doc, replacement yaml.Node
		if err = yaml.Unmarshal([]byte(raw), &doc); err != nil {
			return "", err
		}
		if err = yaml.Unmarshal([]byte(fragment), &replacement); err != nil {
			return "", err
		}
		doc.Content[0].Content[index] = replacement.Content[0]
		updated, err = yaml.Marshal(&doc)
	} else {
		var entries []json.RawMessage
		if err = json.Unmarshal([]byte(raw), &entries); err != nil {
			return "", err
		}
		entries[index] = json.RawMessage(fragment)
		updated, err = json.Marshal(entries)
	}
	if err != nil {
		return "", err
	}
	return configschema.MergeFragment(engine, source, section, string(updated), false)
}
