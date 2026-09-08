package serverconfig

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Official builds may omit with_v2ray_api. Linux routing_mark supplies the
// same dedicated-outbound attribution without requiring a custom binary.
func PrepareMarkedSingBoxAccounting(content string) (AccountingPlan, error) {
	var root map[string]any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil || root == nil {
		return AccountingPlan{}, fmt.Errorf("invalid sing-box configuration")
	}
	outs, _ := root["outbounds"].([]any)
	for _, raw := range outs {
		out := mapValue(raw)
		tag := stringValue(out["tag"])
		if mark := out["routing_mark"]; mark != nil {
			parts := strings.Split(tag, "-")
			if len(parts) != 4 || !strings.HasPrefix(tag, accountingPrefix) {
				return AccountingPlan{}, fmt.Errorf("custom routing_mark requires manual review")
			}
			port, err := strconv.Atoi(parts[2])
			if err != nil || port < 1 || port > 65535 || intValue(mark) != int(uint32(0x51430000)|uint32(port)) {
				return AccountingPlan{}, fmt.Errorf("conflicting accounting mark")
			}
			delete(out, "routing_mark")
		}
	}
	normalized, err := json.Marshal(root)
	if err != nil {
		return AccountingPlan{}, err
	}
	plan, err := PrepareAccounting(core.EngineSingBox, string(normalized))
	if err != nil {
		return plan, err
	}
	decoder = json.NewDecoder(strings.NewReader(plan.Content))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return plan, err
	}
	experimental := mapValue(root["experimental"])
	delete(experimental, "v2ray_api")
	if len(experimental) == 0 {
		delete(root, "experimental")
	}
	marks := map[string]uint32{}
	for i := range plan.Ports {
		port := &plan.Ports[i]
		port.Mark = uint32(0x51430000) | uint32(port.Port)
		for _, tag := range port.Outbounds {
			marks[tag] = port.Mark
		}
	}
	outs, _ = root["outbounds"].([]any)
	for _, raw := range outs {
		out := mapValue(raw)
		if mark := marks[stringValue(out["tag"])]; mark != 0 {
			out["routing_mark"] = mark
		}
	}
	data, err := json.MarshalIndent(root, "", "  ")
	plan.Content, plan.Source, plan.API = string(data)+"\n", "nft-dual", ""
	return plan, err
}
