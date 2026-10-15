package serverconfig

import (
	"fmt"
	"strings"
)

func accountingEntryMatches(value any, entry AccountingPort) bool {
	for _, tag := range append([]string{entry.Inbound}, entry.Aliases...) {
		if accountingRuleMatches(value, tag) {
			return true
		}
	}
	return false
}

func accountingEntryRuleTags(value any, entry AccountingPort) []string {
	var result []string
	for _, tag := range append([]string{entry.Inbound}, entry.Aliases...) {
		if accountingRuleMatches(value, tag) {
			result = append(result, tag)
		}
	}
	return result
}

// Fallback connections are counted by the public Raw listener and by XHTTP.
// Use one authenticated VLESS user counter for the group to avoid counting
// the loopback relay and HTTP framing again as client traffic.
func xhttpAccountingUser(root, primary map[string]any) (string, error) {
	backend, _ := xhttpGroup(root, primary)
	user := firstMap(mapValue(backend["settings"])["clients"])
	email := stringValue(user["email"])
	if email == "" || intValue(user["level"]) != 0 {
		return "", fmt.Errorf("XHTTP 组合需要非空用户名与用户 level=0，以建立独立统计")
	}
	if vision := firstMap(mapValue(primary["settings"])["clients"]); vision != nil && (vision["email"] != email || intValue(vision["level"]) != 0) {
		return "", fmt.Errorf("组合 Vision 与 XHTTP 必须使用相同统计用户名与 level=0，请通过预设重新保存")
	}
	entries, _ := root["inbounds"].([]any)
	for _, raw := range entries {
		in := mapValue(raw)
		if in["tag"] == primary["tag"] || in["tag"] == backend["tag"] {
			continue
		}
		for _, key := range []string{"clients", "users"} {
			users, _ := mapValue(in["settings"])[key].([]any)
			for _, rawUser := range users {
				if strings.EqualFold(stringValue(mapValue(rawUser)["email"]), email) {
					return "", fmt.Errorf("组合统计用户名 %s 与入站 %s 重复，请为各实例分配不同用户名", email, in["tag"])
				}
			}
		}
	}
	return email, nil
}
