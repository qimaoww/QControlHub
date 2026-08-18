package webui

import (
	"context"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// loadSettingsPageAudit loads the recent audit trail for the settings page.
func (s *Server) loadSettingsPageAudit(ctx context.Context) ([]core.AuditLogEntry, error) {
	return s.store.ListAuditLogs(ctx, 25)
}
