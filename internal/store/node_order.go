package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/jackc/pgx/v5"
)

// NodeOrder is a complete account-visible fleet order. Revision zero means no
// preference has been saved, including on an account with an empty fleet.
type NodeOrder struct {
	IDs      []string `json:"ids"`
	Revision int64    `json:"revision"`
}

var nodeOrderID = regexp.MustCompile(`^agt_[a-f0-9]{16}$`)

var ErrNodeOrderConflict = errors.New("node order revision changed")

// NodeOrderAccount also gives the single break-glass administrator a durable
// identity, distinct from every database user and restricted token workspace.
func NodeOrderAccount(owner string) string {
	if owner == "" {
		return "environment-admin"
	}
	return owner
}

func mergeNodeOrder(current, subset []string) []string {
	selected := make(map[string]bool, len(subset))
	for _, id := range subset {
		selected[id] = true
	}
	result := slices.Clone(current)
	next := 0
	for i, id := range result {
		if selected[id] {
			result[i] = subset[next]
			next++
		}
	}
	return result
}

func (s *Store) ReadNodeOrder(ctx context.Context) (NodeOrder, error) {
	return s.nodeOrder(ctx, nil, 0, false)
}

func (s *Store) SaveNodeOrder(ctx context.Context, ids []string, revision int64, initialize bool) (NodeOrder, error) {
	if ids == nil || len(ids) > 10000 || revision < 0 {
		return NodeOrder{}, fmt.Errorf("%w: invalid node order", ErrInvalid)
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !nodeOrderID.MatchString(id) || seen[id] {
			return NodeOrder{}, fmt.Errorf("%w: invalid or duplicate node ID", ErrInvalid)
		}
		seen[id] = true
	}
	return s.nodeOrder(ctx, ids, revision, initialize)
}

func (s *Store) nodeOrder(ctx context.Context, ids []string, revision int64, initialize bool) (NodeOrder, error) {
	scope, ok := requestConfigScope(ctx)
	if !ok || scope.OwnerID == "unassigned" {
		return NodeOrder{}, ErrForbidden
	}
	account := NodeOrderAccount(scope.OwnerID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NodeOrder{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return NodeOrder{}, err
	}
	// Serializes first initialization as well as later partial-list updates.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 81079))`, account); err != nil {
		return NodeOrder{}, err
	}
	current := NodeOrder{IDs: []string{}}
	err = tx.QueryRow(ctx, `SELECT node_ids,revision FROM account_node_orders WHERE account_id=$1`, account).Scan(&current.IDs, &current.Revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return NodeOrder{}, err
	}
	args := []any{}
	where := agentAccessClause(ctx, "agents.id", &args)
	rows, err := tx.Query(ctx, `SELECT id FROM agents WHERE revoked_at IS NULL`+where+` ORDER BY enrolled_at,id`, args...)
	if err != nil {
		return NodeOrder{}, err
	}
	visible := make(map[string]bool)
	fleet := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return NodeOrder{}, err
		}
		visible[id] = true
		fleet = append(fleet, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return NodeOrder{}, err
	}
	normalized := []string{}
	for _, id := range current.IDs {
		if visible[id] {
			normalized = append(normalized, id)
			delete(visible, id)
		}
	}
	for _, id := range fleet {
		if visible[id] {
			normalized = append(normalized, id)
		}
	}
	// Validate against the live visibility predicate even for initialization.
	if ids != nil {
		allowed := make(map[string]bool, len(fleet))
		for _, id := range fleet {
			allowed[id] = true
		}
		for _, id := range ids {
			if !allowed[id] {
				return NodeOrder{}, ErrNotFound
			}
		}
	}
	changed := current.Revision > 0 && !slices.Equal(current.IDs, normalized)
	current.IDs = normalized
	conflict := ids != nil && !(initialize && current.Revision > 0) && revision != current.Revision
	if ids != nil && !conflict && !(initialize && current.Revision > 0) {
		if initialize {
			selected := make(map[string]bool, len(ids))
			for _, id := range ids {
				selected[id] = true
			}
			result := slices.Clone(ids)
			for _, id := range current.IDs {
				if !selected[id] {
					result = append(result, id)
				}
			}
			current.IDs = result
		} else {
			current.IDs = mergeNodeOrder(current.IDs, ids)
		}
		changed = true
	}
	if changed {
		current.Revision++
		_, err = tx.Exec(ctx, `INSERT INTO account_node_orders(account_id,node_ids,revision) VALUES($1,$2,$3)
   ON CONFLICT(account_id) DO UPDATE SET node_ids=EXCLUDED.node_ids,revision=EXCLUDED.revision`, account, current.IDs, current.Revision)
		if err != nil {
			return NodeOrder{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return NodeOrder{}, err
	}
	if conflict {
		return current, ErrNodeOrderConflict
	}
	return current, nil
}
