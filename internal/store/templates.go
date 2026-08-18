package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// CreateConfigTemplate stores a new reusable configuration template.
func (s *Store) CreateConfigTemplate(ctx context.Context, name, engineName string, content string) (core.ConfigTemplate, error) {
	name = strings.TrimSpace(name)
	content = strings.TrimSpace(content)
	engine, err := core.ParseEngine(engineName)
	if err != nil {
		return core.ConfigTemplate{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return core.ConfigTemplate{}, fmt.Errorf("%w: template name is required and limited to 100 characters", ErrInvalid)
	}
	if len(content) == 0 || len(content) > core.MaxConfigBytes {
		return core.ConfigTemplate{}, fmt.Errorf("%w: template content must be between 1 byte and the configuration limit", ErrInvalid)
	}
	id, err := core.NewID("tpl")
	if err != nil {
		return core.ConfigTemplate{}, err
	}
	now := time.Now().UTC()
	storedContent, err := s.encryptContent(content)
	if err != nil {
		return core.ConfigTemplate{}, err
	}
	template := core.ConfigTemplate{ID: id, Name: name, Engine: engine, Content: content, CreatedAt: now, UpdatedAt: now}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO config_templates (id,name,engine,content,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		template.ID, template.Name, template.Engine, storedContent, template.CreatedAt, template.UpdatedAt)
	if err != nil {
		return core.ConfigTemplate{}, mapError(err)
	}
	return template, nil
}

// ListConfigTemplates returns all templates, newest first.
func (s *Store) ListConfigTemplates(ctx context.Context) ([]core.ConfigTemplate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,name,engine,content,created_at,updated_at
		FROM config_templates ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list config templates: %w", err)
	}
	defer rows.Close()
	templates := make([]core.ConfigTemplate, 0)
	for rows.Next() {
		var template core.ConfigTemplate
		if err := rows.Scan(&template.ID, &template.Name, &template.Engine, &template.Content,
			&template.CreatedAt, &template.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan config template: %w", err)
		}
		template.Content, err = s.decryptContent(template.Content)
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	return templates, rows.Err()
}

// DeleteConfigTemplate removes a template by id.
func (s *Store) DeleteConfigTemplate(ctx context.Context, id string) error {
	command, err := s.pool.Exec(ctx, `DELETE FROM config_templates WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete config template: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RenderConfigTemplate fills the {{variable}} placeholders with node-specific
// values. Unknown placeholders are left untouched so templates can carry
// literal braces without breaking. Random ports are drawn from 20000-63991
// (the same range the server plan builder uses).
func RenderConfigTemplate(content string, agent core.Agent) (string, error) {
	lanIP := ""
	if len(agent.Metrics.NetworkInterfaces) > 0 && len(agent.Metrics.NetworkInterfaces[0].Addresses) > 0 {
		lanIP = agent.Metrics.NetworkInterfaces[0].Addresses[0]
	}
	replacements := map[string]string{
		"node_name": agent.Name,
		"node_id":   agent.ID,
		"lan_ip":    lanIP,
	}
	rendered := content
	for key, value := range replacements {
		rendered = strings.ReplaceAll(rendered, "{{"+key+"}}", value)
	}
	if strings.Contains(rendered, "{{random_port}}") {
		limit := big.NewInt(63991 - 20000 + 1)
		value, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("generate template random port: %w", err)
		}
		rendered = strings.ReplaceAll(rendered, "{{random_port}}", fmt.Sprint(20000+value.Int64()))
	}
	return rendered, nil
}

// renderTemplateForAgent resolves a template against an agent and validates
// the result with the engine checker.
func (s *Store) RenderTemplateForAgent(ctx context.Context, templateID, agentID string) (core.ConfigTemplate, core.Agent, string, error) {
	var template core.ConfigTemplate
	err := s.pool.QueryRow(ctx, `
		SELECT id,name,engine,content,created_at,updated_at
		FROM config_templates WHERE id=$1`, templateID).Scan(
		&template.ID, &template.Name, &template.Engine, &template.Content, &template.CreatedAt, &template.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ConfigTemplate{}, core.Agent{}, "", ErrNotFound
	}
	if err != nil {
		return core.ConfigTemplate{}, core.Agent{}, "", err
	}
	template.Content, err = s.decryptContent(template.Content)
	if err != nil {
		return core.ConfigTemplate{}, core.Agent{}, "", err
	}
	agent, err := s.GetAgent(ctx, agentID)
	if err != nil {
		return core.ConfigTemplate{}, core.Agent{}, "", err
	}
	rendered, err := RenderConfigTemplate(template.Content, agent)
	if err != nil {
		return core.ConfigTemplate{}, core.Agent{}, "", err
	}
	if err := core.ValidateConfig(template.Engine, rendered); err != nil {
		return core.ConfigTemplate{}, core.Agent{}, "", fmt.Errorf("%w: rendered template is invalid: %v", ErrInvalid, err)
	}
	return template, agent, rendered, nil
}
