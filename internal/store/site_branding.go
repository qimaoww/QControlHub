package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) SiteBranding(ctx context.Context) (core.SiteBranding, error) {
	var value core.SiteBranding
	err := s.pool.QueryRow(ctx, `SELECT logo,revision,updated_at FROM site_branding WHERE id=1`).Scan(&value.Logo, &value.Revision, &value.UpdatedAt)
	return value, err
}

func (s *Store) SaveSiteBranding(ctx context.Context, logo *string, revision int64) (core.SiteBranding, error) {
	if !scopeForConfig(ctx).Admin {
		return core.SiteBranding{}, ErrForbidden
	}
	if revision < 1 {
		return core.SiteBranding{}, fmt.Errorf("%w: 缺少站点品牌并发校验信息", ErrInvalid)
	}
	if logo != nil {
		if err := core.ValidateSiteLogo(*logo); err != nil {
			return core.SiteBranding{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.SiteBranding{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.SiteBranding{}, err
	}
	var saved core.SiteBranding
	err = tx.QueryRow(ctx, `UPDATE site_branding SET logo=COALESCE($1,logo),revision=revision+1,updated_at=now() WHERE id=1 AND revision=$2 RETURNING logo,revision,updated_at`, logo, revision).Scan(&saved.Logo, &saved.Revision, &saved.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.SiteBranding{}, fmt.Errorf("%w: 站点品牌已由其他会话修改，请重新读取后保存", ErrConflict)
	}
	if err != nil {
		return core.SiteBranding{}, err
	}
	return saved, tx.Commit(ctx)
}
