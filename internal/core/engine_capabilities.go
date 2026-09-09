package core

import (
	"fmt"
	"strings"
)

func AllEngines() []Engine {
	return []Engine{EngineMihomo, EngineXray, EngineSingBox, EngineShadowsocksRust}
}

// ValidateEngineCapabilities permits an explicitly empty set (monitor-only nodes).
func ValidateEngineCapabilities(engines []Engine) error {
	seen := make(map[Engine]bool)
	for _, engine := range engines {
		if !engine.Valid() || seen[engine] {
			return fmt.Errorf("invalid or duplicate engine %q", engine)
		}
		seen[engine] = true
	}
	return nil
}

// ParseDefaultAgentEngines accepts a comma-separated installation preference.
func ParseDefaultAgentEngines(value string) ([]Engine, error) {
	engines := []Engine{}
	if strings.TrimSpace(value) == "none" {
		return engines, nil
	}
	for _, item := range strings.Split(value, ",") {
		engines = append(engines, Engine(strings.TrimSpace(item)))
	}
	return engines, ValidateEngineCapabilities(engines)
}

func IntersectEngines(selected, supported []Engine) []Engine {
	result := []Engine{}
	for _, engine := range selected {
		for _, candidate := range supported {
			if engine == candidate {
				result = append(result, engine)
				break
			}
		}
	}
	return result
}
