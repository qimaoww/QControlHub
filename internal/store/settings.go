package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) PanelSettings(ctx context.Context) (core.PanelSettings, error) {
	var settings core.PanelSettings
	err := s.pool.QueryRow(ctx, `
		SELECT panel_name,panel_description,task_page_size,task_poll_interval_ms,core_log_minimum_level,webhook_url,updated_at
		FROM panel_settings WHERE id=1`).Scan(
		&settings.PanelName,
		&settings.PanelDescription,
		&settings.TaskPageSize,
		&settings.TaskPollIntervalMS,
		&settings.CoreLogMinimumLevel,
		&settings.WebhookURL,
		&settings.UpdatedAt,
	)
	if err != nil {
		return core.PanelSettings{}, fmt.Errorf("read panel settings: %w", err)
	}
	return settings, nil
}

func (s *Store) SavePanelSettings(ctx context.Context, settings core.PanelSettings) (core.PanelSettings, error) {
	settings.PanelName = strings.TrimSpace(settings.PanelName)
	settings.PanelDescription = strings.TrimSpace(settings.PanelDescription)
	settings.CoreLogMinimumLevel = strings.ToLower(strings.TrimSpace(settings.CoreLogMinimumLevel))
	settings.WebhookURL = strings.TrimSpace(settings.WebhookURL)
	// Older API clients do not send this field. Preserve the live policy so a
	// cosmetic settings update cannot silently re-enable lower-severity logs.
	if settings.CoreLogMinimumLevel == "" {
		if err := s.pool.QueryRow(ctx, `SELECT core_log_minimum_level FROM panel_settings WHERE id=1`).Scan(&settings.CoreLogMinimumLevel); err != nil {
			return core.PanelSettings{}, fmt.Errorf("read current core log minimum level: %w", err)
		}
	}
	if err := settings.Validate(); err != nil {
		return core.PanelSettings{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	settings.UpdatedAt = time.Now().UTC()
	err := s.pool.QueryRow(ctx, `
		INSERT INTO panel_settings (
			id,panel_name,panel_description,task_page_size,task_poll_interval_ms,core_log_minimum_level,webhook_url,updated_at
		) VALUES (1,$1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET
			panel_name=EXCLUDED.panel_name,
			panel_description=EXCLUDED.panel_description,
			task_page_size=EXCLUDED.task_page_size,
			task_poll_interval_ms=EXCLUDED.task_poll_interval_ms,
			core_log_minimum_level=EXCLUDED.core_log_minimum_level,
			webhook_url=EXCLUDED.webhook_url,
			updated_at=EXCLUDED.updated_at
		RETURNING panel_name,panel_description,task_page_size,task_poll_interval_ms,core_log_minimum_level,webhook_url,updated_at`,
		settings.PanelName,
		settings.PanelDescription,
		settings.TaskPageSize,
		settings.TaskPollIntervalMS,
		settings.CoreLogMinimumLevel,
		settings.WebhookURL,
		settings.UpdatedAt,
	).Scan(
		&settings.PanelName,
		&settings.PanelDescription,
		&settings.TaskPageSize,
		&settings.TaskPollIntervalMS,
		&settings.CoreLogMinimumLevel,
		&settings.WebhookURL,
		&settings.UpdatedAt,
	)
	if err != nil {
		return core.PanelSettings{}, fmt.Errorf("save panel settings: %w", err)
	}
	return settings, nil
}
