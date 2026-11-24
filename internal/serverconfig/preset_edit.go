package serverconfig

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"go.yaml.in/yaml/v4"
)

// A preset edits the fields its parser represents. Apply the delta between
// canonical forms to the native object, retaining everything the form did not
// change, including additional accounts, nested options and exact integers.
func mutatePresetFields(engine core.Engine, source, generated, original string) (string, bool, error) {
	var before Input
	found := false
	for _, input := range ParseAll(engine, source) {
		if input.Tag == original {
			if found {
				return "", true, fmt.Errorf("入站名称不唯一")
			}
			before = input
			found = true
		}
	}
	after, ok := Parse(engine, generated)
	if !found || !ok || before.Protocol != after.Protocol || engine == core.EngineShadowsocksRust || isXHTTPGroup(before.Protocol) {
		return "", false, nil
	}
	canonical, err := Generate(engine, before)
	if err != nil {
		// Native source can omit panel-only client keys or frontend TLS metadata.
		// Build its comparison projection without requiring those fields again.
		// Only the new candidate goes through Generate validation.
		switch engine {
		case core.EngineXray:
			canonical, err = generateXray(before)
		case core.EngineSingBox:
			canonical, err = generateSingBox(before)
		case core.EngineMihomo:
			canonical, err = generateMihomo(before)
		}
		if err != nil {
			return "", true, fmt.Errorf("无法还原现有入站，请使用完整入站配置: %w", err)
		}
	}
	raw, section, err := InboundSource(engine, source, original)
	if err != nil {
		return "", true, err
	}
	oldRaw, oldSection, err := InboundSource(engine, canonical, before.Tag)
	if err != nil || oldSection != section {
		return "", false, nil
	}
	newRaw, newSection, err := InboundSource(engine, generated, after.Tag)
	if err != nil || newSection != section {
		return "", false, nil
	}
	current, err := managedDocument(engine, raw)
	if err != nil {
		return "", true, err
	}
	oldObject, err := managedDocument(engine, oldRaw)
	if err != nil {
		return "", true, err
	}
	newObject, err := managedDocument(engine, newRaw)
	if err != nil {
		return "", true, err
	}
	if engine == core.EngineXray {
		settings := mapValue(current["settings"])
		canonicalSettings := mapValue(oldObject["settings"])
		if settings["clients"] == nil && settings["users"] != nil && canonicalSettings["clients"] != nil {
			// Historical generators used the inactive users key. Migrate the
			// full list, including accounts not represented by the form.
			settings["clients"] = settings["users"]
			delete(settings, "users")
		}
		alignXrayPresetKeys(current, oldObject, newObject)
	}
	keys := make([]string, 0, len(current))
	for k := range current {
		keys = append(keys, k)
	}
	patched := presetDelta(current, oldObject, newObject)
	var encoded []byte
	if engine == core.EngineMihomo {
		encoded, err = yaml.Marshal(map[string]any{section: []any{patched}})
	} else {
		encoded, err = json.Marshal(map[string]any{section: []any{patched}})
	}
	if err != nil {
		return "", true, err
	}
	key := "tag"
	if engine == core.EngineMihomo {
		key = "name"
	}
	result, err := configschema.MutateListItem(engine, source, string(encoded), section, key, original, "modify", keys...)
	return result, true, err
}

// Apply field changes at the spelling the kernel actually reads. Adding a
// partial canonical object can mask the native alias or leave a new value
// behind a legacy key that takes precedence.
func alignXrayPresetKeys(current, before, after map[string]any) {
	stream := mapValue(current["streamSettings"])
	oldStream, newStream := mapValue(before["streamSettings"]), mapValue(after["streamSettings"])
	if stream["method"] != nil {
		alignPresetKey(oldStream, newStream, "network", "method")
	}
	if stream["xhttpSettings"] == nil && stream["splithttpSettings"] != nil {
		alignPresetKey(oldStream, newStream, "xhttpSettings", "splithttpSettings")
	}
	if current["protocol"] == ProtocolSocks {
		if settings := mapValue(current["settings"]); settings["accounts"] == nil && settings["users"] != nil {
			alignPresetKey(mapValue(before["settings"]), mapValue(after["settings"]), "accounts", "users")
		}
	}
	if current["protocol"] != "tunnel" && current["protocol"] != "dokodemo-door" {
		return
	}
	settings := mapValue(current["settings"])
	oldSettings, newSettings := mapValue(before["settings"]), mapValue(after["settings"])
	if settings["address"] != nil {
		alignPresetKey(oldSettings, newSettings, "rewriteAddress", "address")
	}
	if intValue(settings["port"]) != 0 {
		alignPresetKey(oldSettings, newSettings, "rewritePort", "port")
	}
	if settings["network"] != nil {
		alignPresetKey(oldSettings, newSettings, "allowedNetwork", "network")
	}
}

func alignPresetKey(before, after map[string]any, canonical, native string) {
	for _, object := range []map[string]any{before, after} {
		if value, ok := object[canonical]; ok {
			object[native] = value
			delete(object, canonical)
		}
	}
}

func presetDelta(current, before, after any) any {
	if reflect.DeepEqual(before, after) {
		return current
	}
	oldMap, _ := before.(map[string]any)
	newMap, newOK := after.(map[string]any)
	if newOK {
		result, ok := current.(map[string]any)
		if !ok {
			result = map[string]any{}
		}
		for key := range oldMap {
			if _, exists := newMap[key]; !exists {
				delete(result, key)
			}
		}
		for key, value := range newMap {
			old, represented := oldMap[key]
			switch {
			case represented && reflect.DeepEqual(old, value):
				// Leave the native value (or its absence) alone.
			case value == nil:
				// A newly represented null can clear an existing native option.
				result[key] = nil
			default:
				result[key] = presetDelta(result[key], old, value)
			}
		}
		return result
	}
	oldList, oldListOK := before.([]any)
	newList, newListOK := after.([]any)
	currentList, currentListOK := current.([]any)
	// Single-account forms affect their first represented account only. The
	// remaining native list is retained. Keys removed by an explicit protocol
	// change are handled by the enclosing map delta.
	if oldListOK && newListOK && currentListOK && len(oldList) == 1 && len(newList) == 1 && len(currentList) > 0 {
		currentList[0] = presetDelta(currentList[0], oldList[0], newList[0])
		return currentList
	}
	return after
}

// NativePresetObject provides a read-only projection for native YAML forms.
func NativePresetObject(engine core.Engine, fragment string) (string, error) {
	object, err := managedDocument(engine, fragment)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(object)
	return string(encoded), err
}

func PatchNativePreset(engine core.Engine, fragment, baseline, updated string) (string, error) {
	current, err := managedDocument(engine, fragment)
	if err != nil {
		return "", err
	}
	before, err := managedDocument(engine, baseline)
	if err != nil {
		return "", err
	}
	after, err := managedDocument(engine, updated)
	if err != nil {
		return "", err
	}
	// Apply the represented delta across protocol switches as well. Fields no
	// longer represented by the selected protocol are removed; native options
	// absent from both projections remain available for core validation.
	patched := presetDelta(current, before, after)

	var encoded []byte
	if engine == core.EngineMihomo {
		encoded, err = yaml.Marshal(patched)
	} else {
		encoded, err = json.Marshal(patched)
	}
	return string(encoded), err
}
