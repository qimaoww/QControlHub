package webui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// taskFilters describes the query parameters that shape the tasks page.
type taskFilters struct {
	AgentID   string
	Status    core.TaskStatus
	Action    core.Action
	Limit     int
	PageError string
}

// taskFiltersFromRequest parses and validates the tasks page query string.
// Invalid filters degrade to no filter and surface a page error instead of
// failing the request.
func taskFiltersFromRequest(request *http.Request, defaultLimit int) taskFilters {
	filters := taskFilters{
		AgentID: strings.TrimSpace(request.URL.Query().Get("agent_id")),
		Status:  core.TaskStatus(request.URL.Query().Get("status")),
		Action:  core.Action(request.URL.Query().Get("action")),
		Limit:   defaultLimit,
	}
	if filters.Status != "" && !filters.Status.Valid() {
		filters.PageError = "任务状态筛选无效"
		filters.Status = ""
	}
	if filters.Action != "" && !filters.Action.Valid() {
		filters.PageError = firstNonEmpty(filters.PageError, "任务动作筛选无效")
		filters.Action = ""
	}
	if rawLimit := request.URL.Query().Get("limit"); rawLimit != "" {
		parsed, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil || (parsed != 50 && parsed != 100 && parsed != 500) {
			filters.PageError = firstNonEmpty(filters.PageError, "任务数量必须是 50、100 或 500")
		} else {
			filters.Limit = parsed
		}
	}
	return filters
}
