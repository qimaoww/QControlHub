package api

import (
	"context"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Preview and form submission consume the exact complete workspace draft.
// The saved revision is still checked before planning and in the transaction.
// No preview writes a configuration, metadata, policy or execution task.
type configDraftPreview struct {
	Content         string `json:"content"`
	ExpectedVersion int    `json:"expected_version"`
}

func validateConfigDraft(engine core.Engine, content string) error {
	if len(content) > core.MaxConfigBytes {
		return fmt.Errorf("configuration exceeds size limit")
	}
	if err := core.ValidateConfig(engine, content); err != nil {
		return err
	}
	_, err := configschema.RootKeys(engine, content)
	return err
}

func (s *Server) configDraftSource(ctx context.Context, current core.Config, content *string) (core.Config, error) {
	if content == nil {
		return current, nil
	}
	if err := validateConfigDraft(current.Engine, *content); err != nil {
		return current, err
	}
	updated, err := s.accountingUpdateSource(ctx, current, *content)
	if err != nil {
		return current, err
	}
	current.Content = updated
	return current, nil
}
