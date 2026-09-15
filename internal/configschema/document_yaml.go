package configschema

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

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
