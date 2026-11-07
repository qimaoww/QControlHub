package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/notify"
)

func (s *Server) notificationRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/settings/notifications", s.requirePermission(core.PermissionSettingsRead, http.HandlerFunc(s.getNotifications)))
	mux.Handle("PUT /api/v1/settings/notifications", s.requirePermission(core.PermissionSettingsManage, http.HandlerFunc(s.putNotifications)))
	mux.Handle("POST /api/v1/settings/notifications/preview", s.requirePermission(core.PermissionSettingsRead, http.HandlerFunc(s.previewNotification)))
	mux.Handle("POST /api/v1/settings/notifications/test", s.requirePermission(core.PermissionSettingsManage, http.HandlerFunc(s.testNotification)))
	mux.Handle("GET /api/v1/settings/notifications/deliveries", s.requirePermission(core.PermissionSettingsRead, http.HandlerFunc(s.getNotificationDeliveries)))
}
func (s *Server) getNotifications(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.NotificationConfiguration(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defaults := map[string]notify.Template{}
	for event := range notify.EventNames {
		defaults[event] = notify.DefaultTemplate(event)
	}
	update, _ := s.store.NotificationUpdateStatus(r.Context())
	writeJSON(w, 200, map[string]any{"configuration": s.notificationConfigurationResponse(c), "events": notify.EventNames, "variables": notify.Variables, "variable_descriptions": notify.VariableDescriptions, "defaults": defaults, "update": update})
}
func (s *Server) putNotifications(w http.ResponseWriter, r *http.Request) {
	var c notify.Configuration
	if err := decodeJSON(w, r, &c, 128<<10); err != nil {
		writeError(w, 400, "通知配置无效")
		return
	}
	saved, err := s.store.SaveNotificationConfiguration(r.Context(), c)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "notifications.saved", "", "api")
	writeJSON(w, 200, s.notificationConfigurationResponse(saved))
}
func (s *Server) previewNotification(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Event    string          `json:"event"`
		Template notify.Template `json:"template"`
	}
	if err := decodeJSON(w, r, &in, 16<<10); err != nil || notify.EventNames[in.Event] == "" {
		writeError(w, 400, "模板事件无效")
		return
	}
	title, body, err := notify.Render(in.Template, notify.Sample(in.Event), "示例面板")
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"title": title, "body": body})
}
func (s *Server) testNotification(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ChannelID string `json:"channel_id"`
		Revision  int64  `json:"revision"`
	}
	if err := decodeJSON(w, r, &in, 1024); err != nil {
		writeError(w, 400, "测试请求无效")
		return
	}
	id, err := s.store.QueueNotificationTest(r.Context(), in.ChannelID, in.Revision)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(r, "notifications.tested", in.ChannelID, "api")
	writeJSON(w, 202, map[string]any{"id": id, "status": "pending"})
}
func (s *Server) getNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.NotificationDeliveries(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *Server) notificationConfigurationResponse(c notify.Configuration) notify.Configuration {
	out := c.Redacted()
	if s.notifier.Secret != "" {
		for i, ch := range c.Channels {
			if _, explicit := ch.Secrets["secret"]; ch.ID == "legacy-webhook" && !explicit {
				out.Channels[i].Configured = append(out.Channels[i].Configured, "secret")
			}
		}
	}
	return out
}
