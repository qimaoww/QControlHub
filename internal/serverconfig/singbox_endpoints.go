package serverconfig

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SingBoxEntry is the identity and listen metadata shared by native inbounds
// and endpoints. Endpoint configuration is intentionally opaque here: only a
// protocol-specific generator/parser may expose credentials or editable
// fields. Entries with a zero port (for example a dynamic Tailscale listener)
// remain source-only until that protocol declares its management contract.
type SingBoxEntry struct {
	Section string `json:"section"`
	Tag     string `json:"tag"`
	Kind    string `json:"type"`
	Port    int    `json:"port"`
}

// DiscoverSingBoxEntries returns both native lists in document order. It
// fails closed for malformed list members rather than allowing an update to
// accidentally replace a custom object.
func DiscoverSingBoxEntries(content string) ([]SingBoxEntry, error) {
	if err := rejectAmbiguousSharedJSON(content); err != nil {
		return nil, err
	}
	var root map[string]any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil || root == nil {
		if err == nil {
			err = fmt.Errorf("JSON root must be an object")
		}
		return nil, err
	}
	result := make([]SingBoxEntry, 0)
	for _, section := range []string{"inbounds", "endpoints"} {
		raw, exists := root[section]
		if !exists {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("sing-box %s must be an array", section)
		}
		for index, value := range items {
			entry := mapValue(value)
			if entry == nil {
				return nil, fmt.Errorf("sing-box %s[%d] must be an object", section, index)
			}
			tag, kind := stringValue(entry["tag"]), stringValue(entry["type"])
			if strings.TrimSpace(tag) == "" || strings.TrimSpace(kind) == "" {
				return nil, fmt.Errorf("sing-box %s[%d] requires tag and type", section, index)
			}
			if tag != strings.TrimSpace(tag) || kind != strings.TrimSpace(kind) {
				return nil, fmt.Errorf("sing-box %s[%d] tag and type must not contain surrounding whitespace", section, index)
			}
			port := 0
			if raw, exists := entry["listen_port"]; exists {
				number, ok := raw.(json.Number)
				value, err := number.Int64()
				if !ok || err != nil || value < 0 || value > 65535 {
					return nil, fmt.Errorf("sing-box %s[%d] listen_port must be an integer between 0 and 65535", section, index)
				}
				port = int(value)
			}
			result = append(result, SingBoxEntry{Section: section, Tag: tag, Kind: kind, Port: port})
		}
	}
	return result, nil
}

// ValidateSingBoxEntryIdentity checks collisions across both native lists.
// A zero listen_port is not considered a collision because it denotes a
// dynamic or protocol-managed endpoint.
func ValidateSingBoxEntryIdentity(content string, candidate SingBoxEntry, originalTag string, operation string) error {
	entries, err := DiscoverSingBoxEntries(content)
	if err != nil {
		return err
	}
	if candidate.Section != "inbounds" && candidate.Section != "endpoints" {
		return fmt.Errorf("sing-box entry section must be inbounds or endpoints")
	}
	if candidate.Tag == "" || (candidate.Kind == "" && operation != "delete") {
		return fmt.Errorf("sing-box entry requires tag and type")
	}
	if candidate.Port < 0 || candidate.Port > 65535 {
		return fmt.Errorf("sing-box listen port must be between 0 and 65535")
	}
	if operation != "add" && operation != "modify" && operation != "delete" {
		return fmt.Errorf("unsupported sing-box entry operation %q", operation)
	}
	if operation != "add" && originalTag == "" {
		return fmt.Errorf("sing-box %s requires an existing tag", operation)
	}
	found := false
	seenTags := make(map[string]bool, len(entries))
	seenPorts := make(map[int]bool, len(entries))
	for _, entry := range entries {
		if seenTags[entry.Tag] {
			return fmt.Errorf("sing-box tag %q is duplicated across inbounds/endpoints", entry.Tag)
		}
		seenTags[entry.Tag] = true
		if operation != "delete" && entry.Port != 0 {
			if seenPorts[entry.Port] {
				return fmt.Errorf("sing-box listen port %d is duplicated across inbounds/endpoints", entry.Port)
			}
			seenPorts[entry.Port] = true
		}
		if entry.Tag == originalTag {
			found = true
			if operation != "add" && entry.Section != candidate.Section {
				return fmt.Errorf("sing-box entry %q cannot move between inbounds and endpoints", originalTag)
			}
		}
		if operation != "delete" && entry.Tag == candidate.Tag && (operation == "add" || entry.Tag != originalTag) {
			return fmt.Errorf("sing-box tag %q already exists across inbounds/endpoints", candidate.Tag)
		}
		if operation != "delete" && candidate.Port != 0 && entry.Port == candidate.Port && (operation == "add" || entry.Tag != originalTag) {
			return fmt.Errorf("sing-box listen port %d already exists across inbounds/endpoints", candidate.Port)
		}
	}
	if operation != "add" && !found {
		return fmt.Errorf("sing-box entry %q does not exist", originalTag)
	}
	return nil
}

// parseSingBoxEndpoint exposes only losslessly editable server endpoints.
func parseSingBoxEndpoint(content string) (Input, bool) {
	var root map[string]any
	if json.Unmarshal([]byte(content), &root) != nil {
		return Input{}, false
	}
	items, _ := root["endpoints"].([]any)
	if len(items) != 1 {
		return Input{}, false
	}
	entry := mapValue(items[0])
	return parseSingBoxEndpointProtocol(entry)
}
