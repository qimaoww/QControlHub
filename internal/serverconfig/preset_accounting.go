package serverconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// FormatJSONSource changes whitespace only: number lexemes, escapes, unknown
// fields and array order must survive display and save-only operations.
func FormatJSONSource(content string) (string, error) {
	var output bytes.Buffer
	if err := json.Indent(&output, bytes.TrimSpace([]byte(content)), "", "  "); err != nil {
		return "", err
	}
	output.WriteByte('\n')
	if output.Len() > core.MaxConfigBytes {
		return "", fmt.Errorf("格式化结果超过 2 MiB 上限，已保留原文")
	}
	return output.String(), nil
}

// FormatConfigSource keeps YAML verbatim and formats supported JSON sources.
func FormatConfigSource(engine core.Engine, content string) (string, error) {
	if engine == core.EngineXray || engine == core.EngineSingBox || engine == core.EngineShadowsocksRust {
		return FormatJSONSource(content)
	}
	return content, nil
}

// PresetAccountingSource verifies generated artifacts before removing them.
// Preset mutations operate on originals, never stale per-port outbound copies.
func PresetAccountingSource(engine core.Engine, content string) (string, error) {
	if engine == core.EngineShadowsocksRust {
		// A single-server config stores its mark at the root. Remove verified
		// generated marks before converting to servers[] or changing a port.
		if !strings.Contains(content, "outbound_fwmark") {
			return FormatJSONSource(content)
		}
		if _, err := PrepareAccounting(engine, content); err != nil {
			return "", err
		}
		var root map[string]any
		decoder := json.NewDecoder(strings.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&root); err != nil {
			return "", err
		}
		delete(root, "outbound_fwmark")
		entries, _ := root["servers"].([]any)
		for _, entry := range entries {
			delete(mapValue(entry), "outbound_fwmark")
		}
		data, err := json.MarshalIndent(root, "", "  ")
		return string(data) + "\n", err
	}
	if !strings.Contains(content, accountingPrefix) {
		return FormatConfigSource(engine, content)
	}
	return AccountingUpdateSource(engine, content, content)
}

// PlanPresetAccounting checks the whole saved configuration, not only the
// inbound being edited. Agent still selects the actual backend at deployment.
func PlanPresetAccounting(engine core.Engine, content string) (AccountingPlan, error) {
	source, err := PresetAccountingSource(engine, content)
	if err != nil {
		return AccountingPlan{}, err
	}
	return PrepareIndependentEgress(engine, source)
}
