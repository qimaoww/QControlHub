package serverconfig

// Recognize an API transport by its declared API target and an unconditional
// inbound route, never by an arbitrary listener name alone.
func xrayInternalAPIInbound(root, inbound map[string]any) bool {
	apiTag := stringValue(mapValue(root["api"])["tag"])
	tag := stringValue(inbound["tag"])
	if apiTag == "" || tag == "" || stringValue(inbound["protocol"]) != "dokodemo-door" {
		return false
	}
	rules, _ := mapValue(root["routing"])["rules"].([]any)
	for _, raw := range rules {
		rule := mapValue(raw)
		if !accountingRuleMatches(rule["inboundTag"], tag) {
			continue
		}
		if stringValue(rule["outboundTag"]) != apiTag {
			return false
		}
		for key := range rule {
			if key != "type" && key != "inboundTag" && key != "outboundTag" {
				return false
			}
		}
		return rule["inboundTag"] != nil
	}
	return false
}
