package core

import "strings"

// Instance IDs are stable within (node, engine). Empty means legacy/unattributed.
const AgentFeatureCoreLogInstances = "core-log-instances-v1"

func CoreLogService(engine Engine, instance, manager string) string {
	name := string(engine)
	if engine == EngineShadowsocksRust {
		name = "shadowsocks-rust"
	}
	service := "qagent-" + name
	if instance != "" && instance != "main" {
		if manager == "openrc" {
			return service + "-" + instance
		}
		return service + "@" + instance + ".service"
	}
	if manager == "openrc" {
		return service
	}
	return service + ".service"
}

func CoreLogInstance(engine Engine, service string) (string, bool) {
	if (engine == EngineXray && service == "xray.service") || (engine == EngineSingBox && (service == "sing-box.service" || service == "singbox.service")) || (engine == EngineShadowsocksRust && service == "shadowsocks-rust.service") {
		return "main", true
	}
	for _, manager := range []string{"systemd", "openrc"} {
		if service == CoreLogService(engine, "main", manager) {
			return "main", true
		}
		prefix := strings.TrimSuffix(CoreLogService(engine, "main", manager), ".service")
		candidate := ""
		if manager == "systemd" && strings.HasPrefix(service, prefix+"@") && strings.HasSuffix(service, ".service") {
			candidate = strings.TrimSuffix(strings.TrimPrefix(service, prefix+"@"), ".service")
		}
		if manager == "openrc" && strings.HasPrefix(service, prefix+"-") {
			candidate = strings.TrimPrefix(service, prefix+"-")
		}
		if ValidAgentShareID(candidate) {
			return candidate, true
		}
	}
	return "", false
}
