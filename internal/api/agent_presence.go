package api

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/notify"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// MonitorAgentPresence emits each durable online/offline transition once.
// State is stored in PostgreSQL so a process restart cannot duplicate alerts.
func (s *Server) MonitorAgentPresence(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			operationContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			settings, err := s.store.PanelSettings(operationContext)
			if err == nil {
				var transitions []store.AgentPresenceTransition
				transitions, err = s.store.AgentPresenceTransitions(operationContext, now.UTC(), time.Duration(settings.AgentOfflineThresholdSeconds)*time.Second)
				if err == nil {
					for _, transition := range transitions {
						ownerSettings, settingsErr := s.store.AgentPanelSettings(operationContext, transition.Agent.ID)
						if settingsErr != nil {
							slog.Warn("load agent notification settings", "agent_id", transition.Agent.ID, "error", settingsErr)
							continue
						}
						if strings.TrimSpace(ownerSettings.WebhookURL) == "" || (transition.Online && !ownerSettings.NotifyAgentOnline) || (!transition.Online && !ownerSettings.NotifyAgentOffline) {
							continue
						}
						event := notify.AgentOfflineEvent(transition.Agent)
						if transition.Online {
							event = notify.AgentOnlineEvent(transition.Agent)
						}
						if sendErr := s.notifier.Send(operationContext, ownerSettings.WebhookURL, event); sendErr != nil {
							slog.Warn("deliver agent presence webhook", "agent_id", transition.Agent.ID, "error", sendErr)
						}
					}
					var quotaTransitions []store.TrafficQuotaTransition
					quotaTransitions, err = s.store.ClaimTrafficQuotaTransitions(operationContext)
					if err == nil {
						for _, transition := range quotaTransitions {
							ownerSettings, settingsErr := s.store.AgentPanelSettings(operationContext, transition.Policy.AgentID)
							if settingsErr != nil || !ownerSettings.NotifyTrafficQuota || strings.TrimSpace(ownerSettings.WebhookURL) == "" {
								continue
							}
							if sendErr := s.notifier.Send(operationContext, ownerSettings.WebhookURL, notify.TrafficQuotaEvent(transition.Policy, transition.AgentName)); sendErr != nil {
								slog.Warn("deliver traffic quota webhook", "policy_id", transition.Policy.ID, "error", sendErr)
							}
						}
					}
				}
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("monitor agent presence", "error", err)
			}
			cancel()
		}
	}
}
