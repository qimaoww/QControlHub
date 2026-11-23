package core

// TaskFilterActions are the actions offered by the task list and accepted in
// account preferences. Internal read/import actions remain queryable via API.
func TaskFilterActions() []Action {
	return []Action{ActionValidate, ActionDeploy, ActionInstall, ActionUninstall,
		ActionStart, ActionStop, ActionRestart, ActionStatus, ActionUpgradeAgent,
		ActionNetworkDiagnostic, ActionIPQuality, ActionEnableBBR, ActionDisableBBR,
		ActionMigrateTLS, ActionConfigureTCP, ActionCheckNginx, ActionUpdateNginx, ActionCheckCaddy, ActionUpdateCaddy, ActionPanelFailure}
}
