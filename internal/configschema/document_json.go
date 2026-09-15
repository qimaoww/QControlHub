package configschema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

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
