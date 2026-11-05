package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ThemePreference stores only the current selection. The account is always
// derived from authenticated context; callers cannot choose another owner.
type ThemePreference struct {
	ThemeID  string `json:"theme_id"`
	Revision int64  `json:"revision"`
}

var ErrThemeConflict = errors.New("theme preference revision changed")

func ValidThemeID(id string) bool { return id == "classic" || id == "analytics" }

func (s *Store) ReadTheme(ctx context.Context) (ThemePreference, error) {
	return s.themePreference(ctx, "", 0)
}

func (s *Store) SaveTheme(ctx context.Context, id string, revision int64) (ThemePreference, error) {
	if !ValidThemeID(id) || revision < 0 {
		return ThemePreference{}, fmt.Errorf("%w: invalid theme preference", ErrInvalid)
	}
	return s.themePreference(ctx, id, revision)
}

func (s *Store) themePreference(ctx context.Context, id string, revision int64) (ThemePreference, error) {
	scope, ok := requestConfigScope(ctx)
	if !ok || scope.OwnerID == "unassigned" {
		return ThemePreference{}, ErrForbidden
	}
	account := NodeOrderAccount(scope.OwnerID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ThemePreference{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return ThemePreference{}, err
	}
	// Protect initial inserts too, when no row exists to lock yet.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 81080))`, account); err != nil {
		return ThemePreference{}, err
	}
	// Only accounts without a saved selection inherit the public default.
	// Existing explicit choices, including classic, keep their current revision.
	current := ThemePreference{ThemeID: "analytics"}
	err = tx.QueryRow(ctx, `SELECT theme_id,revision FROM account_themes WHERE account_id=$1`, account).Scan(&current.ThemeID, &current.Revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ThemePreference{}, err
	}
	if id != "" {
		if current.Revision != revision {
			return current, ErrThemeConflict
		}
		current.ThemeID = id
		current.Revision++
		_, err = tx.Exec(ctx, `INSERT INTO account_themes(account_id,theme_id,revision) VALUES($1,$2,$3)
   ON CONFLICT(account_id) DO UPDATE SET theme_id=EXCLUDED.theme_id,revision=EXCLUDED.revision`, account, id, current.Revision)
		if err != nil {
			return ThemePreference{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ThemePreference{}, err
	}
	return current, nil
}
