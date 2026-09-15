package agent

import (
	"encoding/json"
	"errors"
	"fmt"
)

// normalizeImportedSSRustLogDestinations keeps only console writers. File
// writers bypass the Agent's node-wide transport-cache budget, while syslog
// writers bypass the private volatile journal and can reach the host journal.
// Older releases also accepted config_path for an arbitrary log4rs policy, so
// that escape hatch is removed during import as well.
func normalizeImportedSSRustLogDestinations(content string) (string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return "", err
	}
	logValue, ok := root["log"]
	if !ok || string(logValue) == "null" {
		return content, nil
	}
	var logging map[string]json.RawMessage
	if err := json.Unmarshal(logValue, &logging); err != nil || logging == nil {
		if err == nil {
			err = errors.New("null log configuration")
		}
		return "", fmt.Errorf("SS Rust log configuration is not an object: %w", err)
	}
	changed := false
	if raw, exists := logging["config_path"]; exists {
		var path string
		if err := json.Unmarshal(raw, &path); err != nil {
			return "", fmt.Errorf("SS Rust log.config_path is not a string: %w", err)
		}
		if path != "" {
			delete(logging, "config_path")
			changed = true
		}
	}
	rawWriters, hasWriters := logging["writers"]
	if hasWriters {
		var writers []json.RawMessage
		if err := json.Unmarshal(rawWriters, &writers); err != nil {
			return "", fmt.Errorf("SS Rust log.writers is not an array: %w", err)
		}
		consoleWriters := make([]json.RawMessage, 0, len(writers))
		var fallbackConsole json.RawMessage
		for _, rawWriter := range writers {
			var writer map[string]json.RawMessage
			if err := json.Unmarshal(rawWriter, &writer); err != nil || len(writer) != 1 {
				return "", errors.New("SS Rust log writer must contain exactly one supported destination")
			}
			if console, exists := writer["console"]; exists {
				var settings map[string]json.RawMessage
				if err := json.Unmarshal(console, &settings); err != nil || settings == nil {
					return "", errors.New("SS Rust console log writer is not an object")
				}
				consoleWriters = append(consoleWriters, rawWriter)
				continue
			}
			var persistent json.RawMessage
			switch {
			case writer["file"] != nil:
				persistent = writer["file"]
			case writer["syslog"] != nil:
				persistent = writer["syslog"]
			default:
				return "", errors.New("SS Rust log writer has an unsupported destination")
			}
			var settings map[string]json.RawMessage
			if err := json.Unmarshal(persistent, &settings); err != nil || settings == nil {
				return "", errors.New("SS Rust persistent log writer is not an object")
			}
			if fallbackConsole == nil {
				consoleSettings := make(map[string]json.RawMessage)
				for _, key := range []string{"level", "format"} {
					if value, exists := settings[key]; exists {
						consoleSettings[key] = value
					}
				}
				encoded, err := json.Marshal(map[string]map[string]json.RawMessage{"console": consoleSettings})
				if err != nil {
					return "", err
				}
				fallbackConsole = encoded
			}
			changed = true
		}
		if changed {
			if len(consoleWriters) == 0 && fallbackConsole != nil {
				consoleWriters = append(consoleWriters, fallbackConsole)
			}
			encoded, err := json.Marshal(consoleWriters)
			if err != nil {
				return "", err
			}
			logging["writers"] = encoded
		}
	}
	if !changed {
		return content, nil
	}
	normalizedLog, err := json.Marshal(logging)
	if err != nil {
		return "", err
	}
	root["log"] = normalizedLog
	normalized, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(normalized) + "\n", nil
}
