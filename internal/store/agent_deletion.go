package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) DeleteAgent(ctx context.Context, id string) (err error) {
	// Only revoke access and persist cleanup work here. Historical data must
	// never hold the HTTP request open or roll back an already deleted node.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stage := "authorize"
	defer func() {
		if err != nil {
			err = fmt.Errorf("delete agent %s (%s): %w", id, stage, err)
		}
	}()
	if err := requireAgentDeletion(ctx, s.pool, id); err != nil {
		return err
	}
	stage = "begin transaction"
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var enrollmentID *string
	stage = "revoke identity"
	err = tx.QueryRow(ctx, `
		UPDATE agents SET revoked_at=now()
		WHERE id=$1 AND revoked_at IS NULL
		RETURNING enrollment_id`, id).Scan(&enrollmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	legacyEnrollmentID := ""
	if enrollmentID != nil {
		legacyEnrollmentID = strings.TrimSpace(*enrollmentID)
	}
	stage = "delete enrollment credentials"
	if _, err := tx.Exec(ctx, `
		DELETE FROM enrollment_tokens
		WHERE agent_id=$1 OR id=NULLIF($2,'')`, id, legacyEnrollmentID); err != nil {
		return err
	}
	stage = "queue cleanup"
	if _, err := tx.Exec(ctx, `INSERT INTO agent_deletion_jobs(agent_id) VALUES ($1)`, id); err != nil {
		return err
	}
	stage = "commit"
	return tx.Commit(ctx)
}
