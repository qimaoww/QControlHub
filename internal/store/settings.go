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
		SELECT panel_name,panel_description,task_page_size,task_poll_interval_ms,webhook_url,updated_at
		FROM panel_settings WHERE id=1`).Scan(
		&settings.PanelName,
		&settings.PanelDescription,
		&settings.TaskPageSize,
		&settings.TaskPollIntervalMS,
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
	settings.WebhookURL = strings.TrimSpace(settings.WebhookURL)
	if err := settings.Validate(); err != nil {
		return core.PanelSettings{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	settings.UpdatedAt = time.Now().UTC()
	err := s.pool.QueryRow(ctx, `
		INSERT INTO panel_settings (
			id,panel_name,panel_description,task_page_size,task_poll_interval_ms,webhook_url,updated_at
		) VALUES (1,$1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO UPDATE SET
			panel_name=EXCLUDED.panel_name,
			panel_description=EXCLUDED.panel_description,
			task_page_size=EXCLUDED.task_page_size,
			task_poll_interval_ms=EXCLUDED.task_poll_interval_ms,
			webhook_url=EXCLUDED.webhook_url,
			updated_at=EXCLUDED.updated_at
		RETURNING panel_name,panel_description,task_page_size,task_poll_interval_ms,webhook_url,updated_at`,
		settings.PanelName,
		settings.PanelDescription,
		settings.TaskPageSize,
		settings.TaskPollIntervalMS,
		settings.WebhookURL,
		settings.UpdatedAt,
	).Scan(
		&settings.PanelName,
		&settings.PanelDescription,
		&settings.TaskPageSize,
		&settings.TaskPollIntervalMS,
		&settings.WebhookURL,
		&settings.UpdatedAt,
	)
	if err != nil {
		return core.PanelSettings{}, fmt.Errorf("save panel settings: %w", err)
	}
	return settings, nil
}
