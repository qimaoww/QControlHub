package configschema

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

type jsonPart struct {
	raw, key, prefix, value, suffix string
}

// These ranges are taken from validated JSON. Reusing the original tokens
// keeps field order, whitespace and numbers outside the edited value intact.
func jsonParts(content string, array bool) (int, int, []jsonPart) {
	opening := "{"
	if array {
		opening = "["
	}
	open := strings.Index(content, opening)
	start, depth, quoted, escaped, close := open+1, 0, false, false, len(content)-1
	var raw []string
	for i := start; i < len(content); i++ {
		char := content[i]
		if quoted {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				quoted = false
			}
		} else if char == '"' {
			quoted = true
		} else if char == '{' || char == '[' {
			depth++
		} else if char == '}' || char == ']' {
			if depth == 0 {
				close = i
				break
			}
			depth--
		} else if char == ',' && depth == 0 {
			raw = append(raw, content[start:i])
			start = i + 1
		}
	}
	if strings.TrimSpace(content[start:close]) != "" {
		raw = append(raw, content[start:close])
	}
	parts := make([]jsonPart, 0, len(raw))
	for _, entry := range raw {
		value, key, prefix := entry, "", ""
		if !array {
			decoder := json.NewDecoder(strings.NewReader(entry))
			_ = decoder.Decode(&key)
			colon := strings.IndexByte(entry[decoder.InputOffset():], ':') + int(decoder.InputOffset())
			prefix, value = entry[:colon+1], entry[colon+1:]
		}
		trimmed := strings.TrimSpace(value)
		leading := strings.Index(value, trimmed)
		parts = append(parts, jsonPart{raw: entry, key: key, prefix: prefix + value[:leading], value: trimmed, suffix: value[leading+len(trimmed):]})
	}
	return open, close, parts
}

func patchJSONObject(content string, values map[string]json.RawMessage) string {
	if strings.TrimSpace(content) == "" {
		content = "{}\n"
	}
	open, close, parts := jsonParts(content, false)
	remaining := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		remaining[key] = value
	}
	var entries []string
	for _, part := range parts {
		if value, ok := remaining[part.key]; ok {
			entries = append(entries, part.prefix+string(value)+part.suffix)
			delete(remaining, part.key)
		}
	}
	keys := make([]string, 0, len(remaining))
	for key := range remaining {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		encoded, _ := json.Marshal(key)
		entries = append(entries, "\n  "+string(encoded)+": "+string(remaining[key])+"\n")
	}
	return content[:open+1] + strings.Join(entries, ",") + content[close:]
}

// ApplyJSONChanges transfers only the generator's before/after changes to the
// user's native document. A field absent from both projections is unowned: it
// survives even inside settings, TLS options or a single client's object.
func ApplyJSONChanges(content, before, after string) (string, error) {
	for _, value := range []string{content, before, after} {
		if _, err := jsonRoot(value); err != nil {
			return "", err
		}
	}
	return string(applyJSONChanges(json.RawMessage(content), json.RawMessage(before), json.RawMessage(after))), nil
}

func applyJSONChanges(current, before, after json.RawMessage) json.RawMessage {
	var left, right bytes.Buffer
	_ = json.Compact(&left, before)
	_ = json.Compact(&right, after)
	if bytes.Equal(left.Bytes(), right.Bytes()) {
		return current
	}
	var original, old, next map[string]json.RawMessage
	if json.Unmarshal(current, &original) == nil && original != nil && json.Unmarshal(before, &old) == nil && old != nil && json.Unmarshal(after, &next) == nil && next != nil {
		for key := range old {
			if _, exists := next[key]; !exists {
				delete(original, key)
			}
		}
		for key, value := range next {
			if previous, exists := old[key]; exists && original[key] != nil {
				original[key] = applyJSONChanges(original[key], previous, value)
			} else {
				original[key] = value
			}
		}
		return json.RawMessage(patchJSONObject(string(current), original))
	}
	var originalList, oldList, nextList []json.RawMessage
	if strings.HasPrefix(strings.TrimSpace(string(current)), "[") && strings.HasPrefix(strings.TrimSpace(string(before)), "[") && strings.HasPrefix(strings.TrimSpace(string(after)), "[") && json.Unmarshal(current, &originalList) == nil && json.Unmarshal(before, &oldList) == nil && json.Unmarshal(after, &nextList) == nil {
		open, close, parts := jsonParts(string(current), true)
		var entries []string
		for index, value := range nextList {
			prior := index
			if len(oldList) != len(nextList) {
				prior = -1
				identity := jsonIdentity(value)
				for candidate, oldValue := range oldList {
					if identity != "" && jsonIdentity(oldValue) == identity {
						prior = candidate
						break
					}
				}
			}
			if prior >= 0 && prior < len(originalList) && prior < len(oldList) {
				patched := applyJSONChanges(originalList[prior], oldList[prior], value)
				entries = append(entries, parts[prior].prefix+string(patched)+parts[prior].suffix)
			} else {
				entries = append(entries, string(value))
			}
		}
		return json.RawMessage(string(current[:open+1]) + strings.Join(entries, ",") + string(current[close:]))
	}
	return after
}

func jsonIdentity(value json.RawMessage) string {
	var object map[string]json.RawMessage
	_ = json.Unmarshal(value, &object)
	for _, key := range []string{"tag", "name", "remarks"} {
		var identity string
		if json.Unmarshal(object[key], &identity) == nil && identity != "" {
			return identity
		}
	}
	return ""
}
