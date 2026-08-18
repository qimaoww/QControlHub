package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) insertConfigRevision(ctx context.Context, tx pgx.Tx, config core.Config) error {
	storedContent, err := s.encryptContent(config.Content)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO config_revisions (config_id,version,agent_id,name,description,engine,content,created_at)
		VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8)`,
		config.ID, config.Version, config.AgentID, config.Name, config.Description, config.Engine, storedContent, config.UpdatedAt)
	return mapError(err)
}

func (s *Store) ListConfigRevisions(ctx context.Context, configID string, limit int) ([]core.Config, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM configs WHERE id=$1 AND deleted_at IS NULL
	)`, configID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.config_id,COALESCE(r.agent_id,''),r.name,r.description,r.engine,r.content,r.version,r.created_at,r.created_at
		FROM config_revisions r JOIN configs c ON c.id=r.config_id
		WHERE r.config_id=$1 AND c.deleted_at IS NULL
		ORDER BY r.version DESC LIMIT $2`, configID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.Config, 0)
	for rows.Next() {
		var revision core.Config
		if err := rows.Scan(&revision.ID, &revision.AgentID, &revision.Name, &revision.Description, &revision.Engine,
			&revision.Content, &revision.Version, &revision.CreatedAt, &revision.UpdatedAt); err != nil {
			return nil, err
		}
		revision.Content, err = s.decryptContent(revision.Content)
		if err != nil {
			return nil, err
		}
		result = append(result, revision)
	}
	return result, rows.Err()
}

func (s *Store) ConfigRevision(ctx context.Context, configID string, version int) (core.Config, error) {
	if version < 1 {
		return core.Config{}, fmt.Errorf("%w: revision version must be positive", ErrInvalid)
	}
	var revision core.Config
	err := s.pool.QueryRow(ctx, `
		SELECT r.config_id,COALESCE(r.agent_id,''),r.name,r.description,r.engine,r.content,r.version,r.created_at,r.created_at
		FROM config_revisions r JOIN configs c ON c.id=r.config_id
		WHERE r.config_id=$1 AND r.version=$2 AND c.deleted_at IS NULL`, configID, version).Scan(
		&revision.ID, &revision.AgentID, &revision.Name, &revision.Description, &revision.Engine,
		&revision.Content, &revision.Version, &revision.CreatedAt, &revision.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Config{}, ErrNotFound
	}
	if err != nil {
		return core.Config{}, err
	}
	revision.Content, err = s.decryptContent(revision.Content)
	if err != nil {
		return core.Config{}, err
	}
	return revision, nil
}

func (s *Store) RestoreConfigRevision(ctx context.Context, configID string, revisionVersion, expectedVersion int) (core.Config, error) {
	if revisionVersion < 1 {
		return core.Config{}, fmt.Errorf("%w: revision version must be positive", ErrInvalid)
	}
	if expectedVersion < 1 {
		return core.Config{}, fmt.Errorf("%w: expected configuration version must be positive", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, err
	}
	defer tx.Rollback(ctx)

	// Node-owned configuration writes always lock the agent before the config.
	// Keep the same ordering here so a restore cannot deadlock with
	// SaveAgentConfig or DeleteAgent while the revision foreign key is checked.
	var ownerAgentID string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(agent_id,'') FROM configs WHERE id=$1 AND deleted_at IS NULL`, configID).Scan(&ownerAgentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Config{}, ErrNotFound
	}
	if err != nil {
		return core.Config{}, err
	}
	if ownerAgentID != "" {
		var lockedAgentID string
		err = tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, ownerAgentID).Scan(&lockedAgentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return core.Config{}, ErrNotFound
		}
		if err != nil {
			return core.Config{}, err
		}
	}

	var current core.Config
	err = tx.QueryRow(ctx, `
		SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at
		FROM configs WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, configID).Scan(
		&current.ID, &current.AgentID, &current.Name, &current.Description, &current.Engine,
		&current.Content, &current.Version, &current.CreatedAt, &current.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Config{}, ErrNotFound
	}
	if err != nil {
		return core.Config{}, err
	}
	if current.AgentID != ownerAgentID {
		return core.Config{}, fmt.Errorf("%w: configuration ownership changed during restore", ErrConflict)
	}
	if current.Version != expectedVersion {
		return core.Config{}, fmt.Errorf("%w: configuration changed; reload before restoring", ErrConflict)
	}

	var revision core.Config
	err = tx.QueryRow(ctx, `
		SELECT config_id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,created_at
		FROM config_revisions WHERE config_id=$1 AND version=$2`, configID, revisionVersion).Scan(
		&revision.ID, &revision.AgentID, &revision.Name, &revision.Description, &revision.Engine,
		&revision.Content, &revision.Version, &revision.CreatedAt, &revision.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Config{}, ErrNotFound
	}
	if err != nil {
		return core.Config{}, err
	}
	if revision.AgentID != current.AgentID || (current.AgentID != "" && revision.Engine != current.Engine) {
		return core.Config{}, fmt.Errorf("%w: revision does not belong to the current configuration", ErrConflict)
	}
	revision.Content, err = s.decryptContent(revision.Content)
	if err != nil {
		return core.Config{}, err
	}
	if err := core.ValidateConfig(revision.Engine, revision.Content); err != nil {
		return core.Config{}, fmt.Errorf("%w: stored revision is invalid: %v", ErrInvalid, err)
	}
	name, description, err := validateConfigMetadata(revision.Name, revision.Description)
	if err != nil {
		return core.Config{}, err
	}

	restored := core.Config{
		ID: configID, AgentID: current.AgentID, Name: name, Description: description,
		Engine: revision.Engine, Content: revision.Content, Version: current.Version + 1,
		CreatedAt: current.CreatedAt, UpdatedAt: time.Now().UTC(),
	}
	storedRestoredContent, err := s.encryptContent(restored.Content)
	if err != nil {
		return core.Config{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE configs SET name=$2,description=$3,engine=$4,content=$5,version=$6,updated_at=$7
		WHERE id=$1 AND deleted_at IS NULL AND version=$8`, restored.ID, restored.Name,
		restored.Description, restored.Engine, storedRestoredContent, restored.Version, restored.UpdatedAt, expectedVersion)
	if err != nil {
		return core.Config{}, mapError(err)
	}
	if command.RowsAffected() != 1 {
		return core.Config{}, fmt.Errorf("%w: configuration changed; reload before restoring", ErrConflict)
	}
	if err := s.insertConfigRevision(ctx, tx, restored); err != nil {
		return core.Config{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Config{}, err
	}
	return restored, nil
}
