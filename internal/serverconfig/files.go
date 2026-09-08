package serverconfig

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// ConfigFile is an ordered source fragment, not an arbitrary filesystem path.
// The complete ordered bundle is stored/versioned as one configuration so a
// revision can never combine an old outbound with a newly edited inbound.
type ConfigFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

var configNameUnsafe = regexp.MustCompile(`[^\p{L}\p{N}_.-]`)
var configSourceName = regexp.MustCompile(`^[\p{L}\p{N}_-][\p{L}\p{N}_.-]*\.json$`)

// Names come from the preset's inbound/outbound tag, never the Agent's name.
// The manifest keeps array order; filenames must not determine routing order.
func configEntryFilename(tag, kind string, index int, used map[string]bool) string {
	base := strings.TrimSuffix(tag, ".json")
	base = configNameUnsafe.ReplaceAllString(base, "_")
	base = strings.Trim(base, ".")
	runes := []rune(base)
	if len(runes) > 48 {
		base = string(runes[:48])
	}
	if base == "" {
		base = fmt.Sprintf("%s-%d", strings.TrimSuffix(kind, "s"), index+1)
	}
	name := base + ".json"
	for suffix := 2; used[name]; suffix++ {
		name = fmt.Sprintf("%s-%d.json", base, suffix)
	}
	used[name] = true
	return name
}

func SplitConfigFiles(engine core.Engine, content string) ([]ConfigFile, error) {
	if engine != core.EngineXray && engine != core.EngineSingBox {
		name := "config.json"
		if engine == core.EngineMihomo {
			name = "config.yaml"
		}
		return []ConfigFile{{Path: name, Content: content}}, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil || root == nil {
		return nil, fmt.Errorf("multi-file configuration must be a JSON object")
	}
	files := []ConfigFile{{Path: "common.json"}}
	for _, key := range []string{"inbounds", "outbounds"} {
		raw, exists := root[key]
		if !exists {
			continue
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, fmt.Errorf("%s must be an array", key)
		}
		if entries == nil {
			return nil, fmt.Errorf("%s must not be null", key)
		}
		used := map[string]bool{}
		for i, entry := range entries {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(entry, &object); err != nil || object == nil {
				return nil, fmt.Errorf("%s[%d] must be an object", key, i)
			}
			value, _ := json.MarshalIndent(map[string]any{key: []json.RawMessage{entry}}, "", "  ")
			var tag string
			_ = json.Unmarshal(object["tag"], &tag)
			files = append(files, ConfigFile{Path: key + "/" + configEntryFilename(tag, key, i, used), Content: string(value) + "\n"})
		}
		// Keep empty arrays in common to preserve their presence.
		if len(entries) > 0 {
			delete(root, key)
		}
	}
	value, _ := json.MarshalIndent(root, "", "  ")
	files[0].Content = string(value) + "\n"
	return files, nil
}

func MergeConfigFiles(engine core.Engine, files []ConfigFile) (string, error) {
	if engine != core.EngineXray && engine != core.EngineSingBox {
		return "", fmt.Errorf("multi-file editing is only available for Xray and sing-box")
	}
	if len(files) == 0 || len(files) > 1025 || (files[0].Path != "common.json" && files[0].Path != "00-common.json") {
		return "", fmt.Errorf("invalid configuration file manifest")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(files[0].Content), &root); err != nil || root == nil {
		return "", fmt.Errorf("invalid common configuration")
	}
	lists := map[string][]json.RawMessage{}
	seen := map[string]bool{}
	size := 0
	for i, file := range files {
		size += len(file.Content)
		if size > core.MaxConfigBytes {
			return "", fmt.Errorf("configuration bundle exceeds size limit")
		}
		if seen[file.Path] {
			return "", fmt.Errorf("duplicate configuration file")
		}
		seen[file.Path] = true
		if i == 0 {
			continue
		}
		key, name, ok := strings.Cut(file.Path, "/")
		validName := len(name) <= 220 && configSourceName.MatchString(name)
		if files[0].Path == "00-common.json" {
			validName = file.Path == fmt.Sprintf("%s/%04d.json", key, len(lists[key]))
		}
		if !ok || (key != "inbounds" && key != "outbounds") || !validName {
			return "", fmt.Errorf("invalid ordered configuration path %q", file.Path)
		}
		var fragment map[string]json.RawMessage
		if err := json.Unmarshal([]byte(file.Content), &fragment); err != nil || len(fragment) != 1 {
			return "", fmt.Errorf("%s must contain only %s", file.Path, key)
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(fragment[key], &entries); err != nil || len(entries) != 1 {
			return "", fmt.Errorf("%s must contain exactly one entry", file.Path)
		}
		lists[key] = append(lists[key], entries[0])
	}
	for key, entries := range lists {
		if _, exists := root[key]; exists {
			return "", fmt.Errorf("%s is defined in both common and entry files", key)
		}
		root[key], _ = json.Marshal(entries)
	}
	content, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	if err := core.ValidateConfig(engine, string(content)); err != nil {
		return "", err
	}
	// Also validate object entries, including edits that replaced one with null.
	if _, err := SplitConfigFiles(engine, string(content)); err != nil {
		return "", err
	}
	return string(content) + "\n", nil
}
