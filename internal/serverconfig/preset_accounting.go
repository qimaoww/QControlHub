package serverconfig

import (
	"encoding/json"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// PresetAccountingSource verifies generated artifacts before removing them.
// Preset mutations operate on originals, never stale per-port outbound copies.
func PresetAccountingSource(engine core.Engine, content string) (string, error) {
	if engine == core.EngineShadowsocksRust {
		// A single-server config stores its mark at the root. Remove verified
		// generated marks before converting to servers[] or changing a port.
		if !strings.Contains(content, "outbound_fwmark") {
			return content, nil
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
		data, err := json.Marshal(root)
		return string(data), err
	}
	if !strings.Contains(content, accountingPrefix) {
		return content, nil
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
	return PrepareAccounting(engine, source)
}
