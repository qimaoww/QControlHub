package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MaxConfigBytes         = 2 << 20
	MaxConfigEnvelopeBytes = MaxConfigBytes*6 + 512<<10
	MaxAgentBinaryBytes    = 32 << 20
)

func ValidateConfig(engine Engine, content string) error {
	if !engine.Valid() {
		return fmt.Errorf("unsupported engine %q", engine)
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("configuration cannot be empty")
	}
	if len(content) > MaxConfigBytes {
		return fmt.Errorf("configuration exceeds %d bytes", MaxConfigBytes)
	}

	switch engine {
	case EngineMihomo:
		var value map[string]any
		decoder := yaml.NewDecoder(strings.NewReader(content))
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("invalid mihomo YAML: %w", err)
		}
		if len(value) == 0 {
			return errors.New("mihomo configuration must be a YAML mapping")
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return errors.New("invalid mihomo YAML: multiple documents are not allowed")
		}
	default:
		var value map[string]any
		decoder := json.NewDecoder(strings.NewReader(content))
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("invalid %s JSON: %w", engine, err)
		}
		if value == nil {
			return fmt.Errorf("%s configuration must be a JSON object", engine)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return fmt.Errorf("invalid %s JSON: trailing data", engine)
		}
	}
	return nil
}
