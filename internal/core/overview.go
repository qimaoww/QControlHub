package core

type Overview struct {
	Agents       int `json:"agents"`
	AgentsOnline int `json:"agents_online"`
	Configs      int `json:"configs"`
	NodeConfigs  int `json:"node_configs"`
	TasksPending int `json:"tasks_pending"`
	TasksQueued  int `json:"tasks_queued"`
	TasksRunning int `json:"tasks_running"`
	TasksFailed  int `json:"tasks_failed"`
}
