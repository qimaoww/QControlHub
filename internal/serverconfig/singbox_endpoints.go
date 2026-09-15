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
	var root map[string]any
	if err := json.Unmarshal([]byte(content), &root); err != nil || root == nil {
		if err == nil {
			err = fmt.Errorf("JSON root must be an object")
		}
		return nil, err
	}
	result := make([]SingBoxEntry, 0)
	for _, section := range []string{"inbounds", "endpoints"} {
		raw, exists := root[section]
		if !exists || raw == nil {
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
			tag, kind := strings.TrimSpace(stringValue(entry["tag"])), strings.TrimSpace(stringValue(entry["type"]))
			if tag == "" || kind == "" {
				return nil, fmt.Errorf("sing-box %s[%d] requires tag and type", section, index)
			}
			port := intValue(entry["listen_port"])
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
	if candidate.Tag == "" || candidate.Kind == "" {
		return fmt.Errorf("sing-box entry requires tag and type")
	}
	if candidate.Port < 0 || candidate.Port > 65535 {
		return fmt.Errorf("sing-box listen port must be between 0 and 65535")
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
		if entry.Port != 0 {
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
		if entry.Tag == candidate.Tag && (operation == "add" || entry.Tag != originalTag) {
			return fmt.Errorf("sing-box tag %q already exists across inbounds/endpoints", candidate.Tag)
		}
		if candidate.Port != 0 && entry.Port == candidate.Port && (operation == "add" || entry.Tag != originalTag) {
			return fmt.Errorf("sing-box listen port %d already exists across inbounds/endpoints", candidate.Port)
		}
	}
	if operation != "add" && !found {
		return fmt.Errorf("sing-box entry %q does not exist", originalTag)
	}
	return nil
}

// parseSingBoxEndpoint parses the common fields of a supported endpoint. A
// protocol-specific parser can enrich the returned Input after this gate;
// unknown endpoint fields are never discarded by mutation.
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
	protocol := protocolKey(stringValue(entry["type"]))
	if protocol == "" {
		return Input{}, false
	}
	if protocol == ProtocolWireGuard || protocol == ProtocolTailscale || protocol == ProtocolOpenVPNServer {
		return parseSingBoxEndpointProtocol(entry)
	}
	input := Input{Protocol: protocol, Tag: stringValue(entry["tag"]), Port: intValue(entry["listen_port"]), Username: "default", Transport: "raw"}
	input.Credential = stringValue(entry["password"])
	if input.Credential == "" {
		if user := firstMap(entry["users"]); user != nil {
			input.Username = stringValue(user["name"])
			input.Credential = stringValue(user["password"])
			if input.Credential == "" {
				input.Credential = stringValue(user["uuid"])
			}
		}
	}
	return input, parsedInputValid(input)
}

// validateSingBoxEntryMutation performs the identity check before mutating an
// endpoint. It is kept separate from configschema's list helper because the
// latter cannot see collisions in a second root list.
func validateSingBoxEntryMutation(content, generatedContent, originalTag, operation string) error {
	var generated map[string]any
	if err := json.Unmarshal([]byte(generatedContent), &generated); err != nil {
		return err
	}
	items, _ := generated["endpoints"].([]any)
	if len(items) != 1 {
		return fmt.Errorf("generated sing-box endpoint must contain exactly one endpoints item")
	}
	entry := mapValue(items[0])
	if entry == nil {
		return fmt.Errorf("generated sing-box endpoint must be an object")
	}
	candidate := SingBoxEntry{Section: "endpoints", Tag: stringValue(entry["tag"]), Kind: stringValue(entry["type"]), Port: intValue(entry["listen_port"])}
	return ValidateSingBoxEntryIdentity(content, candidate, originalTag, operation)
}
