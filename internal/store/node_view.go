package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// NodeViewPreference stores only the current selection. The account is always
// derived from authenticated context; callers cannot choose another owner.
type NodeViewPreference struct {
	View     string `json:"view"`
	Revision int64  `json:"revision"`
}

var ErrNodeViewConflict = errors.New("node view preference revision changed")

func ValidNodeView(id string) bool { return id == "cards" || id == "list" }

func (s *Store) ReadNodeView(ctx context.Context) (NodeViewPreference, error) {
	return s.nodeViewPreference(ctx, "", 0)
}

func (s *Store) SaveNodeView(ctx context.Context, id string, revision int64) (NodeViewPreference, error) {
	if !ValidNodeView(id) || revision < 0 {
		return NodeViewPreference{}, fmt.Errorf("%w: invalid node view preference", ErrInvalid)
	}
	return s.nodeViewPreference(ctx, id, revision)
}

func (s *Store) nodeViewPreference(ctx context.Context, id string, revision int64) (NodeViewPreference, error) {
	scope, ok := requestConfigScope(ctx)
	if !ok || scope.OwnerID == "unassigned" {
		return NodeViewPreference{}, ErrForbidden
	}
	account := NodeOrderAccount(scope.OwnerID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NodeViewPreference{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return NodeViewPreference{}, err
	}
	// Protect initial inserts too, when no row exists to lock yet.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 81081))`, account); err != nil {
		return NodeViewPreference{}, err
	}
	current := NodeViewPreference{View: "cards"}
	err = tx.QueryRow(ctx, `SELECT view,revision FROM account_node_views WHERE account_id=$1`, account).Scan(&current.View, &current.Revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return NodeViewPreference{}, err
	}
	if id != "" {
		if current.Revision != revision {
			return current, ErrNodeViewConflict
		}
		current.View = id
		current.Revision++
		_, err = tx.Exec(ctx, `INSERT INTO account_node_views(account_id,view,revision) VALUES($1,$2,$3)
   ON CONFLICT(account_id) DO UPDATE SET view=EXCLUDED.view,revision=EXCLUDED.revision`, account, id, current.Revision)
		if err != nil {
			return NodeViewPreference{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return NodeViewPreference{}, err
	}
	return current, nil
}
