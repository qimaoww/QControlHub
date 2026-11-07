package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrTrafficOrderConflict = errors.New("traffic order revision changed")

func trafficOrderNode(key string) string {
	parts := strings.Split(key, ":")
	if len(parts) != 2 || !nodeOrderID.MatchString(parts[0]) {
		return ""
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != parts[1] {
		return ""
	}
	return parts[0]
}

func (s *Store) ReadTrafficOrder(ctx context.Context) (NodeOrder, error) {
	return s.trafficOrder(ctx, nil, 0, false)
}

func (s *Store) SaveTrafficOrder(ctx context.Context, ids []string, revision int64, initialize bool) (NodeOrder, error) {
	if ids == nil || len(ids) > 10000 || revision < 0 {
		return NodeOrder{}, fmt.Errorf("%w: invalid traffic order", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if trafficOrderNode(id) == "" || seen[id] {
			return NodeOrder{}, fmt.Errorf("%w: invalid or duplicate traffic key", ErrInvalid)
		}
		seen[id] = true
	}
	return s.trafficOrder(ctx, ids, revision, initialize)
}

// Preferences belong to the authenticated viewer, including shared viewers.
// Visibility is checked under the same authorization lock as the update.
func (s *Store) trafficOrder(ctx context.Context, ids []string, revision int64, initialize bool) (NodeOrder, error) {
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
	if err = lockAgentUser(ctx, tx); err != nil {
		return NodeOrder{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 81082))`, account); err != nil {
		return NodeOrder{}, err
	}
	current := NodeOrder{IDs: []string{}}
	err = tx.QueryRow(ctx, `SELECT port_keys,revision FROM account_traffic_orders WHERE account_id=$1`, account).Scan(&current.IDs, &current.Revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return NodeOrder{}, err
	}
	args := []any{}
	where := agentAccessClause(ctx, "agents.id", &args)
	rows, err := tx.Query(ctx, `SELECT id FROM agents WHERE revoked_at IS NULL`+where, args...)
	if err != nil {
		return NodeOrder{}, err
	}
	allowed := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return NodeOrder{}, err
		}
		allowed[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return NodeOrder{}, err
	}
	normalized := []string{}
	for _, id := range current.IDs {
		if allowed[trafficOrderNode(id)] {
			normalized = append(normalized, id)
		}
	}
	for _, id := range ids {
		if !allowed[trafficOrderNode(id)] {
			return NodeOrder{}, ErrNotFound
		}
	}
	changed := current.Revision > 0 && !slices.Equal(current.IDs, normalized)
	current.IDs = normalized
	conflict := ids != nil && !(initialize && current.Revision > 0) && revision != current.Revision
	if ids != nil && !conflict && !(initialize && current.Revision > 0) {
		if initialize {
			current.IDs = slices.Clone(ids)
		} else {
			for _, id := range ids {
				if !slices.Contains(current.IDs, id) {
					current.IDs = append(current.IDs, id)
				}
			}
			current.IDs = mergeNodeOrder(current.IDs, ids)
		}
		if len(current.IDs) > 10000 {
			return NodeOrder{}, fmt.Errorf("%w: too many traffic positions", ErrInvalid)
		}
		changed = true
	}
	if changed {
		current.Revision++
		if _, err = tx.Exec(ctx, `INSERT INTO account_traffic_orders(account_id,port_keys,revision) VALUES($1,$2,$3)
   ON CONFLICT(account_id) DO UPDATE SET port_keys=EXCLUDED.port_keys,revision=EXCLUDED.revision`, account, current.IDs, current.Revision); err != nil {
			return NodeOrder{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return NodeOrder{}, err
	}
	if conflict {
		return current, ErrTrafficOrderConflict
	}
	return current, nil
}
