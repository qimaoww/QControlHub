package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PageSizePreference stores only the current selection. The account is always
// derived from authenticated context; callers cannot choose another owner.
type PageSizePreference struct {
	PageSize int   `json:"page_size"`
	Revision int64 `json:"revision"`
}

var ErrPageSizeConflict = errors.New("page size preference revision changed")

func ValidPageSize(size int) bool { return size == 30 || size == 50 || size == 100 || size == 200 }

func ValidPageSizePage(page string) bool {
	return page == "nodes" || page == "traffic" || page == "tcp"
}

func (s *Store) ReadPageSize(ctx context.Context, page string) (PageSizePreference, error) {
	return s.pageSizePreference(ctx, page, 0, 0)
}

func (s *Store) SavePageSize(ctx context.Context, page string, size int, revision int64) (PageSizePreference, error) {
	if !ValidPageSize(size) || revision < 0 {
		return PageSizePreference{}, fmt.Errorf("%w: invalid page size preference", ErrInvalid)
	}
	return s.pageSizePreference(ctx, page, size, revision)
}

func (s *Store) pageSizePreference(ctx context.Context, page string, size int, revision int64) (PageSizePreference, error) {
	if !ValidPageSizePage(page) {
		return PageSizePreference{}, ErrNotFound
	}
	scope, ok := requestConfigScope(ctx)
	if !ok || scope.OwnerID == "unassigned" {
		return PageSizePreference{}, ErrForbidden
	}
	account := NodeOrderAccount(scope.OwnerID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PageSizePreference{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return PageSizePreference{}, err
	}
	// Protect initial inserts too, when no row exists to lock yet.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 81082))`, account+":"+page); err != nil {
		return PageSizePreference{}, err
	}
	current := PageSizePreference{PageSize: 30}
	err = tx.QueryRow(ctx, `SELECT page_size,revision FROM account_page_sizes WHERE account_id=$1 AND page=$2`, account, page).Scan(&current.PageSize, &current.Revision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PageSizePreference{}, err
	}
	if !ValidPageSize(current.PageSize) {
		current.PageSize = 30
	}
	if size != 0 {
		if current.Revision != revision {
			return current, ErrPageSizeConflict
		}
		current.PageSize = size
		current.Revision++
		_, err = tx.Exec(ctx, `INSERT INTO account_page_sizes(account_id,page,page_size,revision) VALUES($1,$2,$3,$4)
   ON CONFLICT(account_id,page) DO UPDATE SET page_size=EXCLUDED.page_size,revision=EXCLUDED.revision`, account, page, size, current.Revision)
		if err != nil {
			return PageSizePreference{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PageSizePreference{}, err
	}
	return current, nil
}
