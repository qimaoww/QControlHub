package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Configurations, encrypted task snapshots and Agent manifests keep their real
// listeners. Exact old leases bridge those snapshots to the new type grants.
func migrateProxyIngressTypes(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT id,proxy_ingresses,proxy_ingress_revision FROM agent_shares WHERE proxy_ingresses<>'[]'::jsonb ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	type item struct {
		id       string
		grants   []core.ProxyIngress
		revision int64
	}
	var items []item
	for rows.Next() {
		var i item
		if err := rows.Scan(&i.id, &i.grants, &i.revision); err != nil {
			rows.Close()
			return err
		}
		items = append(items, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, i := range items {
		modes := map[string]core.ProxyIngress{}
		for _, old := range i.grants {
			if !old.Valid() || old.Generation < 1 {
				return fmt.Errorf("invalid legacy proxy authorization on share %s", i.id)
			}
			g, exists := modes[old.Mode]
			if !exists {
				g = core.ProxyIngress{Mode: old.Mode, Scope: core.ProxyIngressTypeScope, Generation: i.revision + 1}
			}
			if old.Scope == core.ProxyIngressTypeScope {
				g = old
			} else {
				g.Legacy = append(g.Legacy, core.ProxyIngressLease{Port: old.Port, Generation: old.Generation})
			}
			modes[old.Mode] = g
		}
		next := []core.ProxyIngress{}
		for _, g := range modes {
			sort.Slice(g.Legacy, func(a, b int) bool { return g.Legacy[a].Port < g.Legacy[b].Port })
			next = append(next, g)
		}
		sort.Slice(next, func(a, b int) bool { return next[a].Mode < next[b].Mode })
		if _, err := tx.Exec(ctx, `UPDATE agent_shares SET proxy_ingresses=$2,proxy_ingress_revision=proxy_ingress_revision+1,proxy_ingress_error='' WHERE id=$1`, i.id, next); err != nil {
			return err
		}
	}
	// Invalidate stale editors without changing invitation or enabled state.
	_, err = tx.Exec(ctx, `UPDATE agents SET sharing_revision=sharing_revision+1 WHERE id IN (SELECT agent_id FROM agent_shares WHERE proxy_ingresses<>'[]'::jsonb);
	 UPDATE panel_users SET agent_access_revision=agent_access_revision+1 WHERE id IN (SELECT user_id FROM agent_shares WHERE proxy_ingresses<>'[]'::jsonb)`)
	return err
}
