package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/notify"
)

// MonitorAgentPresence retains the existing durable transitions and runs bounded
// database detection separately from external delivery workers.
func (s *Server) MonitorAgentPresence(ctx context.Context) {
	sender := notify.NewSender()
	defer sender.HTTP.CloseIdleConnections()
	for i := 0; i < 4; i++ {
		go s.notificationDeliveryWorker(ctx, sender)
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	maintenance := time.NewTicker(time.Minute)
	defer maintenance.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			op, cancel := context.WithTimeout(ctx, 15*time.Second)
			settings, err := s.store.PanelSettings(op)
			if err == nil {
				_, err = s.store.AgentPresenceTransitions(op, time.Now().UTC(), time.Duration(settings.AgentOfflineThresholdSeconds)*time.Second)
			}
			if err == nil {
				_, err = s.store.ClaimTrafficQuotaTransitions(op)
			}
			if err == nil {
				if cached, cacheErr := s.store.NotificationUpdateStatus(op); cacheErr == nil && cached.Version != "" {
					available, _ := imageVersionStatus(s.controlPlaneVersion, cached.Version)
					err = s.store.SetNotificationUpdateComparison(op, cached.Version, available)
				}
			}
			if err == nil {
				err = s.store.ScanNotifications(op)
			}
			if err != nil && ctx.Err() == nil {
				slog.Warn("scan notifications", "error", err)
			}
			cancel()
		case <-maintenance.C:
			op, cancel := context.WithTimeout(ctx, 20*time.Second)
			if s.store.NotificationUpdateDue(op) {
				s.refreshNotificationUpdate(op, false)
			}
			if err := s.store.CleanNotificationRecords(op); err != nil && ctx.Err() == nil {
				slog.Warn("clean notification records", "error", err)
			}
			cancel()
		}
	}
}
func (s *Server) notificationDeliveryWorker(ctx context.Context, sender *notify.Sender) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			op, cancel := context.WithTimeout(ctx, 20*time.Second)
			_, err := s.store.DeliverNotification(op, sender, s.notifier.Secret)
			cancel()
			if err != nil && ctx.Err() == nil {
				slog.Warn("process notification delivery", "error", err)
			}
		}
	}
}
