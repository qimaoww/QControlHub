package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Explicitly reviewed read operations. GET routes with management behavior
// (retry-editor, remote synchronization, credential export) are not included.
// A new route is unavailable to API Keys until it is reviewed here.
var diagnosticRoutes = map[string][]core.Permission{
	"GET /api/v1/api-key":                                   {},
	"GET /api/v1/overview":                                  {core.PermissionOverviewRead},
	"GET /api/v1/agents":                                    {core.PermissionAgentsRead},
	"GET /api/v1/agent-directory":                           {core.PermissionAgentsRead},
	"GET /api/v1/deployments":                               {core.PermissionDeploymentsRead},
	"GET /api/v1/config-catalogs/{engine}":                  {core.PermissionCatalogsRead},
	"GET /api/v1/agents/{id}/configs":                       {core.PermissionAgentConfigRead},
	"GET /api/v1/agents/{id}/configs/{engine}":              {core.PermissionAgentConfigRead},
	"GET /api/v1/agents/{id}/configs/{engine}/files":        {core.PermissionAgentConfigRead},
	"GET /api/v1/agents/{id}/configs/{engine}/workspace":    {core.PermissionAgentConfigRead},
	"GET /api/v1/agents/{id}/configs/{engine}/state":        {core.PermissionAgentConfigRead},
	"GET /api/v1/agents/{id}/configs/{engine}/fields/{key}": {core.PermissionAgentConfigRead},
	"GET /api/v1/configs":                                   {core.PermissionConfigsRead},
	"GET /api/v1/configs/{id}/revisions":                    {core.PermissionConfigsRead},
	"GET /api/v1/configs/{id}/revisions/{version}":          {core.PermissionConfigsRead},
	"GET /api/v1/tasks":                                     {core.PermissionTasksRead},
	"GET /api/v1/tasks/{id}":                                {core.PermissionTasksRead},
	"GET /api/v1/tasks/{id}/config-snapshot":                {core.PermissionTasksRead, core.PermissionAgentConfigRead},
	"GET /api/v1/system-tcp/tasks":                          {core.PermissionTasksRead},
	"GET /api/v1/settings":                                  {core.PermissionSettingsRead},
	"GET /api/v1/settings/deployment":                       {core.PermissionSettingsRead},
	"GET /api/v1/audit":                                     {core.PermissionAuditRead},
	"GET /api/v1/panel-metrics":                             {core.PermissionPanelMetricsRead},
	"GET /api/v1/metrics/{id}":                              {core.PermissionMetricsRead},
	"GET /api/v1/core-log-sources":                          {core.PermissionCoreLogsRead},
	"GET /api/v1/core-logs":                                 {core.PermissionCoreLogsRead},
	"GET /api/v1/client-connections":                        {core.PermissionCoreLogsRead},
	"GET /api/v1/traffic-policies":                          {core.PermissionTrafficRead},
	"GET /api/v1/traffic-endpoints":                         {core.PermissionTrafficRead},
	"GET /api/v1/traffic-usage":                             {core.PermissionTrafficRead},
	"GET /api/v1/templates":                                 {core.PermissionTemplatesRead},
}

type apiKeyResponseWriter struct {
	http.ResponseWriter
	body   bytes.Buffer
	status int
	secret string
}

func (w *apiKeyResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *apiKeyResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(p)
}
func (w *apiKeyResponseWriter) finish() int {
	if w.status == 0 {
		w.status = 200
	}
	data := w.body.Bytes()
	if len(data) > 0 {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err == nil {
			redacted, err := json.Marshal(core.RedactDiagnosticValue(value))
			if err == nil {
				data = append(redacted, '\n')
			} else {
				w.status = 500
				data = []byte(`{"error":"诊断输出无法脱敏"}`)
			}
		} else {
			data = []byte(core.RedactDiagnostic(string(data)))
		}
		// Last line of defense for reflected request text in legacy handlers.
		data = bytes.ReplaceAll(data, []byte(w.secret), []byte("[redacted]"))
	}
	w.Header().Del("Content-Length")
	w.Header().Del("ETag")
	w.Header().Set("X-QControlHub-Auth-Type", "api_key")
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(data)
	// Do not retain credentials in a writer after the request.
	w.secret = ""
	w.body.Reset()
	return w.status
}

func diagnosticRouteList(permissions []core.Permission) []string {
	result := []string{}
	for pattern, required := range diagnosticRoutes {
		allowed := true
		for _, p := range required {
			if !core.HasPermission(permissions, p) {
				allowed = false
			}
		}
		if allowed {
			result = append(result, strings.TrimSpace(pattern))
		}
	}
	return result
}
