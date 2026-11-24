package serverconfig

import "github.com/qimaoww/qcontrolhub/internal/core"

// A single-account preset must not replace anonymous access or discard users.
func socksInboundCredentials(engine core.Engine, inbound map[string]any) (string, string, bool) {
	users, usernameKey, passwordKey := inbound["users"], "username", "password"
	if engine == core.EngineXray {
		settings := mapValue(inbound["settings"])
		if settings["auth"] != "password" {
			return "", "", false
		}
		users, usernameKey, passwordKey = settings["accounts"], "user", "pass"
		if users == nil {
			users = settings["users"]
		}
	}
	accounts, ok := users.([]any)
	if !ok || len(accounts) != 1 {
		return "", "", false
	}
	account := mapValue(accounts[0])
	if len(account) != 2 {
		return "", "", false
	}
	username, password := stringValue(account[usernameKey]), stringValue(account[passwordKey])
	return username, password, username != "" && password != ""
}
