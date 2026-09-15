package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) CreateConfig(ctx context.Context, input core.Config) (core.Config, error) {
	if input.AgentID != "" {
		return core.Config{}, fmt.Errorf("%w: node-owned configurations must use the agent configuration workflow", ErrInvalid)
	}
	if err := core.ValidateConfig(input.Engine, input.Content); err != nil {
		return core.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	name, description, err := validateConfigMetadata(input.Name, input.Description)
	if err != nil {
		return core.Config{}, err
	}
	id, err := core.NewID("cfg")
	if err != nil {
		return core.Config{}, err
	}
	storedContent, err := s.encryptContent(input.Content)
	if err != nil {
		return core.Config{}, err
	}
	now := time.Now().UTC()
	config := core.Config{
		ID: id, OwnerID: scopeForConfig(ctx).OwnerID, AgentID: input.AgentID, Name: name, Description: description,
		Engine: input.Engine, Content: input.Content, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
			INSERT INTO configs (id,agent_id,name,description,engine,content,version,created_at,updated_at,owner_id)
		VALUES ($1,NULLIF($2,''),$3,$4,$5,$6,$7,$8,$8,$9)`,
		config.ID, config.AgentID, config.Name, config.Description, config.Engine, storedContent, config.Version, now, config.OwnerID)
	if err != nil {
		return core.Config{}, mapError(err)
	}
	if err := s.insertConfigRevision(ctx, tx, config); err != nil {
		return core.Config{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Config{}, err
	}
	return config, nil
}

func (s *Store) UpdateConfig(ctx context.Context, id string, input core.Config) (core.Config, error) {
	if err := core.ValidateConfig(input.Engine, input.Content); err != nil {
		return core.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	name, description, err := validateConfigMetadata(input.Name, input.Description)
	if err != nil {
		return core.Config{}, err
	}
	if input.Version < 1 {
		return core.Config{}, fmt.Errorf("%w: configuration version is required", ErrInvalid)
	}
	storedContent, err := s.encryptContent(input.Content)
	if err != nil {
		return core.Config{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, err
	}
	defer tx.Rollback(ctx)
	var config core.Config
	args := []any{id, name, description, input.Engine, storedContent, input.Version}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	err = tx.QueryRow(ctx, `
		UPDATE configs SET name=$2,description=$3,engine=$4,content=$5,version=version+1,updated_at=now()
		WHERE id=$1 AND deleted_at IS NULL AND agent_id IS NULL AND version=$6`+ownerWhere+`
		RETURNING id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at,owner_id`,
		args...).Scan(
		&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content, &config.Version, &config.CreatedAt, &config.UpdatedAt, &config.OwnerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var exists bool
			existsArgs := []any{id}
			existsWhere := ownerClause(ctx, "owner_id", &existsArgs)
			if existsErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM configs WHERE id=$1 AND deleted_at IS NULL AND agent_id IS NULL`+existsWhere+`)`, existsArgs...).Scan(&exists); existsErr != nil {
				return core.Config{}, existsErr
			}
			if exists {
				return core.Config{}, fmt.Errorf("%w: configuration changed; reload before saving", ErrConflict)
			}
			return core.Config{}, ErrNotFound
		}
		return core.Config{}, mapError(err)
	}
	config.Content, err = s.decryptContent(config.Content)
	if err != nil {
		return core.Config{}, err
	}
	if err := s.insertConfigRevision(ctx, tx, config); err != nil {
		return core.Config{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Config{}, err
	}
	return config, nil
}

func (s *Store) DeleteConfig(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	args := []any{id}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	command, err := tx.Exec(ctx, `UPDATE configs SET deleted_at=now(),content='' WHERE id=$1 AND deleted_at IS NULL AND agent_id IS NULL`+ownerWhere, args...)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM config_revisions WHERE config_id=$1`, id); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE tasks SET status='failed',error='configuration was deleted before dispatch',finished_at=now(),config_content=NULL,lease_id=NULL
		WHERE config_id=$1 AND status='pending'`, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListConfigs(ctx context.Context) ([]core.Config, error) {
	args := []any{}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	rows, err := s.pool.Query(ctx, `
		SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at,owner_id
		FROM configs WHERE deleted_at IS NULL AND agent_id IS NULL`+ownerWhere+` ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configs := make([]core.Config, 0)
	for rows.Next() {
		var config core.Config
		if err := rows.Scan(&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content, &config.Version, &config.CreatedAt, &config.UpdatedAt, &config.OwnerID); err != nil {
			return nil, err
		}
		config.Content, err = s.decryptContent(config.Content)
		if err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	return configs, rows.Err()
}

func (s *Store) ExistingConfigIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	existing := make(map[string]bool)
	if len(ids) == 0 {
		return existing, nil
	}
	args := []any{ids}
	ownerWhere := ownerClause(ctx, "owner_id", &args)
	ownerWhere += configAgentAccessClause(ctx, "configs.agent_id", "configs.engine", &args)
	rows, err := s.pool.Query(ctx, `SELECT id FROM configs WHERE deleted_at IS NULL AND id=ANY($1::text[])`+ownerWhere, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		existing[id] = true
	}
	return existing, rows.Err()
}

func validateConfigMetadata(rawName, rawDescription string) (string, string, error) {
	name := strings.TrimSpace(rawName)
	description := strings.TrimSpace(rawDescription)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return "", "", fmt.Errorf("%w: configuration name is required and must not exceed 100 characters", ErrInvalid)
	}
	if utf8.RuneCountInString(description) > 300 {
		return "", "", fmt.Errorf("%w: configuration description exceeds 300 characters", ErrInvalid)
	}
	return name, description, nil
}
