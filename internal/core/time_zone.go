package core

import (
	"strings"
	"time"
	_ "time/tzdata"
)

const AgentTimeZoneLookupFailure = "无法定位公网 IP，请检查 Agent 网络后重试"

func ValidTimeZone(zone string) bool {
	if zone == "" || zone == "Local" || len(zone) > 128 || strings.TrimSpace(zone) != zone ||
		strings.HasPrefix(zone, ":") || strings.HasPrefix(zone, "/") || strings.Contains(zone, "\\") || strings.Contains(zone, "..") {
		return false
	}
	_, err := time.LoadLocation(zone)
	return err == nil
}
