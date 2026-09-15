package configschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, value, "", "  "); err != nil {
		return "", false, err
	}
	return formatted.String(), true, nil
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
