package core

// PanelSettingOptions is the authoritative set of numeric choices shared by
// panel validation, Agent policy validation and generated browser metadata.
// Return fresh slices so callers cannot change the validation contract.
func PanelSettingOptions() map[string][]int {
	return map[string][]int{
		"ui_font_scale":                      {90, 100, 110},
		"task_page_size":                     {50, 100, 500},
		"task_poll_interval_ms":              {600, 1000, 2000, 5000},
		"agent_heartbeat_interval_seconds":   {10, 15, 30},
		"agent_metrics_interval_seconds":     {1, 5, 15, 30},
		"agent_offline_threshold_seconds":    {45, 60, 90, 180},
		"task_stale_timeout_seconds":         {60, 120, 300, 600},
		"install_task_stale_timeout_seconds": {180, 360, 600, 900},
		"task_max_attempts":                  {1, 3, 5},
		"public_ip_probe_interval_seconds":   {300, 900, 3600},
		"core_log_retention_days":            {1, 3, 7, 14, 30},
		"agent_core_log_max_mib":             {1, 2, 4, 8, 16, 32, 64, 128},
		"agent_core_log_rotate_count":        {0, 1, 2, 3, 5},
		"metric_retention_days":              {7, 14, 30},
		"audit_retention_days":               {0, 30, 90, 180},
		"task_retention_days":                {0, 30, 90, 180},
		"config_revision_retention":          {0, 50, 100},
	}
}
