package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// UIPreferences holds current UI choices only. Resource IDs are hints, never
// authorization: each resource API still resolves the authenticated scope.
type UIPreferences struct {
	Values   map[string]map[string]json.RawMessage `json:"values"`
	Revision int64                                 `json:"revision"`
}

var ErrUIPreferencesConflict = errors.New("UI preferences revision changed")

func ValidUIPreferencePatch(values map[string]map[string]json.RawMessage) bool {
	if len(values) == 0 || len(values) > len(uiPreferenceRules) {
		return false
	}
	for page, fields := range values {
		rules, ok := uiPreferenceRules[page]
		if !ok || len(fields) == 0 || len(fields) > len(rules) {
			return false
		}
		for key, value := range fields {
			rule := rules[key]
			if rule.valid == nil || !rule.valid(value) {
				return false
			}
		}
	}
	return true
}
func (s *Store) ReadUIPreferences(ctx context.Context) (UIPreferences, error) {
	return s.uiPreferences(ctx, nil, 0)
}
func (s *Store) PatchUIPreferences(ctx context.Context, patch map[string]map[string]json.RawMessage, revision int64) (UIPreferences, error) {
	if revision < 0 || !ValidUIPreferencePatch(patch) {
		return UIPreferences{}, fmt.Errorf("%w: invalid UI preferences", ErrInvalid)
	}
	return s.uiPreferences(ctx, patch, revision)
}
func (s *Store) uiPreferences(ctx context.Context, patch map[string]map[string]json.RawMessage, revision int64) (UIPreferences, error) {
	scope, ok := requestConfigScope(ctx)
	if !ok || scope.OwnerID == "unassigned" {
		return UIPreferences{}, ErrForbidden
	}
	account := NodeOrderAccount(scope.OwnerID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return UIPreferences{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return UIPreferences{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 81082))`, account); err != nil {
		return UIPreferences{}, err
	}
	current := UIPreferences{Values: map[string]map[string]json.RawMessage{}}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT choices,revision FROM account_ui_preferences WHERE account_id=$1`, account).Scan(&raw, &current.Revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return UIPreferences{}, err
	}
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &current.Values); err != nil {
			return UIPreferences{}, err
		}
	}
	if patch != nil {
		if current.Revision != revision {
			return current, ErrUIPreferencesConflict
		}
		for page, fields := range patch {
			if current.Values[page] == nil {
				current.Values[page] = map[string]json.RawMessage{}
			}
			for key, value := range fields {
				current.Values[page][key] = value
			}
		}
		raw, err = json.Marshal(current.Values)
		if err != nil {
			return UIPreferences{}, err
		}
		if len(raw) > 32768 {
			return UIPreferences{}, fmt.Errorf("%w: preferences too large", ErrInvalid)
		}
		current.Revision++
		if _, err = tx.Exec(ctx, `INSERT INTO account_ui_preferences(account_id,choices,revision) VALUES($1,$2,$3) ON CONFLICT(account_id) DO UPDATE SET choices=EXCLUDED.choices,revision=EXCLUDED.revision`, account, raw, current.Revision); err != nil {
			return UIPreferences{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return UIPreferences{}, err
	}
	return current, nil
}
