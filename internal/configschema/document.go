package configschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

// RootKeys reports the keys already present in a document with one parse.
func RootKeys(engine core.Engine, content string) (map[string]bool, error) {
	result := make(map[string]bool)
	if engine == core.EngineMihomo {
		root, err := yamlRoot(content)
		if err != nil {
			return nil, err
		}
		for index := 0; index+1 < len(root.Content); index += 2 {
			result[root.Content[index].Value] = true
		}
		return result, nil
	}
	root, err := jsonRoot(content)
	if err != nil {
		return nil, err
	}
	for key := range root {
		result[key] = true
	}
	return result, nil
}

// Fragment returns one root key encoded in the core's native syntax.
func Fragment(engine core.Engine, content, key string) (string, bool, error) {
	if engine == core.EngineMihomo {
		root, err := yamlRoot(content)
		if err != nil {
			return "", false, err
		}
		_, value := yamlMapEntry(root, key)
		if value == nil {
			return "", false, nil
		}
		var output bytes.Buffer
		encoder := yaml.NewEncoder(&output)
		encoder.SetIndent(2)
		if err := encoder.Encode(value); err != nil {
			return "", false, err
		}
		_ = encoder.Close()
		return strings.TrimSuffix(output.String(), "\n"), true, nil
	}
	root, err := jsonRoot(content)
	if err != nil {
		return "", false, err
	}
	value, ok := root[key]
	if !ok {
		return "", false, nil
	}
	var compact any
	if err := json.Unmarshal(value, &compact); err != nil {
		return "", false, err
	}
	formatted, err := json.MarshalIndent(compact, "", "  ")
	return string(formatted), true, err
}

// MergeFragment replaces or removes one root key. It preserves all other
// fields, including fields unknown to this catalog. YAML nodes retain comments
// and ordering; JSON values retain their complete data and are reformatted.
func MergeFragment(engine core.Engine, content, key, fragment string, remove bool) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errors.New("configuration key is required")
	}
	if engine == core.EngineMihomo {
		return mergeYAML(content, key, fragment, remove)
	}
	return mergeJSON(content, key, fragment, remove)
}

// MergeListItem replaces one matching object in a root-level list or appends it
// when no match exists. Other list items, unknown root fields and YAML comments
// are retained. Missing non-list defaults from generatedContent are added so a
// newly introduced server inbound still has the generator's required scaffold.
func MergeListItem(engine core.Engine, content, generatedContent, listKey, matchKey, matchValue string, replacedKeys ...string) (string, error) {
	return mutateListItem(engine, content, generatedContent, listKey, matchKey, matchValue, "upsert", replacedKeys...)
}

// MutateListItem applies an explicit add, modify, or delete operation to one
// root-level list object. Unlike MergeListItem, it never falls back from a
// missing modify target to an append, and it rejects duplicate adds.
func MutateListItem(engine core.Engine, content, generatedContent, listKey, matchKey, matchValue, operation string, replacedKeys ...string) (string, error) {
	if operation != "add" && operation != "modify" && operation != "delete" {
		return "", fmt.Errorf("unsupported list operation %q", operation)
	}
	return mutateListItem(engine, content, generatedContent, listKey, matchKey, matchValue, operation, replacedKeys...)
}

func mutateListItem(engine core.Engine, content, generatedContent, listKey, matchKey, matchValue, operation string, replacedKeys ...string) (string, error) {
	if strings.TrimSpace(listKey) == "" || strings.TrimSpace(matchKey) == "" {
		return "", errors.New("list and match keys are required")
	}
	keysToReplace := make(map[string]struct{}, len(replacedKeys))
	for _, key := range replacedKeys {
		if key = strings.TrimSpace(key); key != "" {
			keysToReplace[key] = struct{}{}
		}
	}
	if engine == core.EngineMihomo {
		return mergeYAMLListItem(content, generatedContent, listKey, matchKey, matchValue, operation, keysToReplace)
	}
	return mergeJSONListItem(content, generatedContent, listKey, matchKey, matchValue, operation, keysToReplace)
}

func mergeYAMLListItem(content, generatedContent, listKey, matchKey, matchValue, operation string, replacedKeys map[string]struct{}) (string, error) {
	var document, generatedDocument yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return "", fmt.Errorf("parse YAML document: %w", err)
	}
	if err := yaml.Unmarshal([]byte(generatedContent), &generatedDocument); err != nil {
		return "", fmt.Errorf("parse generated YAML document: %w", err)
	}
	root, err := yamlRootNode(&document)
	if err != nil {
		return "", err
	}
	generatedRoot, err := yamlRootNode(&generatedDocument)
	if err != nil {
		return "", err
	}
	_, generatedList := yamlMapEntry(generatedRoot, listKey)
	if generatedList == nil || generatedList.Kind != yaml.SequenceNode || len(generatedList.Content) != 1 || generatedList.Content[0].Kind != yaml.MappingNode {
		return "", errors.New("generated YAML must contain exactly one list object")
	}
	if operation != "delete" {
		for index := 0; index+1 < len(generatedRoot.Content); index += 2 {
			key := generatedRoot.Content[index].Value
			if key == listKey {
				continue
			}
			if _, existing := yamlMapEntry(root, key); existing == nil {
				root.Content = append(root.Content, generatedRoot.Content[index], generatedRoot.Content[index+1])
			}
		}
	}
	_, existingList := yamlMapEntry(root, listKey)
	if existingList == nil {
		if operation == "modify" || operation == "delete" {
			return "", fmt.Errorf("YAML field %q has no item %q", listKey, matchValue)
		}
		existingList = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: listKey}, existingList)
	}
	if existingList.Kind != yaml.SequenceNode {
		return "", fmt.Errorf("YAML field %q must be a list", listKey)
	}
	generatedItem := generatedList.Content[0]
	_, generatedMatch := yamlMapEntry(generatedItem, matchKey)
	if generatedMatch == nil || strings.TrimSpace(generatedMatch.Value) == "" {
		return "", fmt.Errorf("generated YAML item is missing %q", matchKey)
	}
	target := matchValue
	if operation == "add" {
		target = generatedMatch.Value
	}
	foundIndex := -1
	if target != "" {
		for index, item := range existingList.Content {
			if item.Kind == yaml.MappingNode {
				_, value := yamlMapEntry(item, matchKey)
				if value != nil && value.Value == target {
					foundIndex = index
					break
				}
			}
		}
	}
	switch operation {
	case "add":
		if foundIndex >= 0 {
			return "", fmt.Errorf("YAML item %q already exists in %q", target, listKey)
		}
		existingList.Content = append(existingList.Content, generatedItem)
	case "modify":
		if foundIndex < 0 {
			return "", fmt.Errorf("YAML item %q does not exist in %q", target, listKey)
		}
		item := existingList.Content[foundIndex]
		removeYAMLMappingKeys(item, replacedKeys)
		existingList.Content[foundIndex] = mergeYAMLMapping(item, generatedItem)
	case "delete":
		if foundIndex < 0 {
			return "", fmt.Errorf("YAML item %q does not exist in %q", target, listKey)
		}
		existingList.Content = append(existingList.Content[:foundIndex], existingList.Content[foundIndex+1:]...)
	case "upsert":
		if foundIndex >= 0 {
			item := existingList.Content[foundIndex]
			removeYAMLMappingKeys(item, replacedKeys)
			existingList.Content[foundIndex] = mergeYAMLMapping(item, generatedItem)
		} else {
			existingList.Content = append(existingList.Content, generatedItem)
		}
	default:
		existingList.Content = append(existingList.Content, generatedItem)
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return "", err
	}
	_ = encoder.Close()
	return output.String(), nil
}

func mergeJSONListItem(content, generatedContent, listKey, matchKey, matchValue, operation string, replacedKeys map[string]struct{}) (string, error) {
	root, err := jsonRoot(content)
	if err != nil {
		return "", err
	}
	generatedRoot, err := jsonRoot(generatedContent)
	if err != nil {
		return "", err
	}
	var generatedItems []json.RawMessage
	if err := json.Unmarshal(generatedRoot[listKey], &generatedItems); err != nil || len(generatedItems) != 1 {
		return "", errors.New("generated JSON must contain exactly one list object")
	}
	if operation != "delete" {
		for key, value := range generatedRoot {
			if key == listKey {
				continue
			}
			if _, exists := root[key]; !exists {
				root[key] = append(json.RawMessage(nil), value...)
			}
		}
	}
	items := make([]json.RawMessage, 0)
	if existing, exists := root[listKey]; exists {
		if err := json.Unmarshal(existing, &items); err != nil {
			return "", fmt.Errorf("JSON field %q must be a list: %w", listKey, err)
		}
	}
	var generatedObject map[string]any
	if json.Unmarshal(generatedItems[0], &generatedObject) != nil {
		return "", errors.New("generated list entry must be a JSON object")
	}
	generatedMatch, ok := generatedObject[matchKey].(string)
	if !ok || strings.TrimSpace(generatedMatch) == "" {
		return "", fmt.Errorf("generated JSON item is missing %q", matchKey)
	}
	target := matchValue
	if operation == "add" {
		target = generatedMatch
	}
	foundIndex := -1
	if target != "" {
		for index, item := range items {
			var object map[string]json.RawMessage
			if json.Unmarshal(item, &object) == nil {
				var value string
				if json.Unmarshal(object[matchKey], &value) == nil && value == target {
					foundIndex = index
					break
				}
			}
		}
	}
	switch operation {
	case "add":
		if foundIndex >= 0 {
			return "", fmt.Errorf("JSON item %q already exists in %q", target, listKey)
		}
		items = append(items, append(json.RawMessage(nil), generatedItems[0]...))
	case "modify", "upsert":
		if foundIndex < 0 {
			if operation == "modify" {
				return "", fmt.Errorf("JSON item %q does not exist in %q", target, listKey)
			}
			items = append(items, append(json.RawMessage(nil), generatedItems[0]...))
			break
		}
		var existingObject map[string]any
		if json.Unmarshal(items[foundIndex], &existingObject) != nil {
			return "", errors.New("list entries must be JSON objects")
		}
		for key := range replacedKeys {
			delete(existingObject, key)
		}
		merged, marshalErr := json.Marshal(mergeJSONMapping(existingObject, generatedObject))
		if marshalErr != nil {
			return "", marshalErr
		}
		items[foundIndex] = merged
	case "delete":
		if foundIndex < 0 {
			return "", fmt.Errorf("JSON item %q does not exist in %q", target, listKey)
		}
		items = append(items[:foundIndex], items[foundIndex+1:]...)
	default:
		items = append(items, append(json.RawMessage(nil), generatedItems[0]...))
	}
	root[listKey], err = json.Marshal(items)
	if err != nil {
		return "", err
	}
	formatted, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(formatted) + "\n", nil
}

func removeYAMLMappingKeys(mapping *yaml.Node, keys map[string]struct{}) {
	if mapping == nil || mapping.Kind != yaml.MappingNode || len(keys) == 0 {
		return
	}
	kept := mapping.Content[:0]
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if _, remove := keys[mapping.Content[index].Value]; remove {
			continue
		}
		kept = append(kept, mapping.Content[index], mapping.Content[index+1])
	}
	mapping.Content = kept
}

func mergeYAMLMapping(existing, generated *yaml.Node) *yaml.Node {
	if existing == nil || generated == nil || existing.Kind != yaml.MappingNode || generated.Kind != yaml.MappingNode {
		return generated
	}
	for index := 0; index+1 < len(generated.Content); index += 2 {
		key := generated.Content[index].Value
		existingIndex, existingValue := yamlMapEntry(existing, key)
		generatedValue := generated.Content[index+1]
		if existingValue == nil {
			existing.Content = append(existing.Content, generated.Content[index], generatedValue)
			continue
		}
		if existingValue.Kind == yaml.MappingNode && generatedValue.Kind == yaml.MappingNode {
			existing.Content[existingIndex+1] = mergeYAMLMapping(existingValue, generatedValue)
		} else {
			existing.Content[existingIndex+1] = generatedValue
		}
	}
	return existing
}

func mergeJSONMapping(existing, generated map[string]any) map[string]any {
	if existing == nil {
		existing = make(map[string]any)
	}
	for key, generatedValue := range generated {
		generatedMap, generatedIsMap := generatedValue.(map[string]any)
		existingMap, existingIsMap := existing[key].(map[string]any)
		if generatedIsMap && existingIsMap {
			existing[key] = mergeJSONMapping(existingMap, generatedMap)
		} else {
			existing[key] = generatedValue
		}
	}
	return existing
}

func mergeYAML(content, key, fragment string, remove bool) (string, error) {
	document := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	if strings.TrimSpace(content) != "" {
		if err := yaml.Unmarshal([]byte(content), document); err != nil {
			return "", fmt.Errorf("parse YAML document: %w", err)
		}
	}
	root, err := yamlRootNode(document)
	if err != nil {
		return "", err
	}
	index, existing := yamlMapEntry(root, key)
	if remove {
		if existing != nil {
			root.Content = append(root.Content[:index], root.Content[index+2:]...)
		}
	} else {
		var valueDocument yaml.Node
		if err := yaml.Unmarshal([]byte(fragment), &valueDocument); err != nil {
			return "", fmt.Errorf("parse YAML fragment: %w", err)
		}
		if len(valueDocument.Content) != 1 {
			return "", errors.New("YAML fragment must contain exactly one value")
		}
		value := valueDocument.Content[0]
		if existing == nil {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
		} else {
			root.Content[index+1] = value
		}
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return "", err
	}
	_ = encoder.Close()
	return output.String(), nil
}

func mergeJSON(content, key, fragment string, remove bool) (string, error) {
	root, err := jsonRoot(content)
	if err != nil {
		return "", err
	}
	if remove {
		delete(root, key)
	} else {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" {
			return "", errors.New("JSON fragment cannot be empty")
		}
		var value json.RawMessage
		if err := json.Unmarshal([]byte(fragment), &value); err != nil {
			return "", fmt.Errorf("parse JSON fragment: %w", err)
		}
		root[key] = append(json.RawMessage(nil), value...)
	}
	formatted, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(formatted) + "\n", nil
}

func yamlRoot(content string) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return nil, fmt.Errorf("parse YAML document: %w", err)
	}
	return yamlRootNode(&document)
}

func yamlRootNode(document *yaml.Node) (*yaml.Node, error) {
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("YAML configuration must be one mapping document")
	}
	return document.Content[0], nil
}

func yamlMapEntry(root *yaml.Node, key string) (int, *yaml.Node) {
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == key {
			return index, root.Content[index+1]
		}
	}
	return len(root.Content), nil
}

func jsonRoot(content string) (map[string]json.RawMessage, error) {
	if strings.TrimSpace(content) == "" {
		return make(map[string]json.RawMessage), nil
	}
	root := make(map[string]json.RawMessage)
	decoder := json.NewDecoder(strings.NewReader(content))
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("parse JSON document: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("JSON document contains trailing data")
		}
		return nil, fmt.Errorf("parse JSON trailing data: %w", err)
	}
	return root, nil
}
