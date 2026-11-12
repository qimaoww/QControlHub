package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// A new consent cannot reactivate the previous instance after an intervening
// withdrawal, even if the node was offline throughout that transition.
func rotateProxyIngressesTx(ctx context.Context, tx pgx.Tx, shareID string) error {
	_, err := tx.Exec(ctx, `UPDATE agent_shares s SET proxy_ingress_revision=proxy_ingress_revision+1,
		proxy_ingress_error='', proxy_ingresses=(SELECT jsonb_agg(g || jsonb_build_object('generation',s.proxy_ingress_revision+1) ORDER BY n)
		FROM jsonb_array_elements(s.proxy_ingresses) WITH ORDINALITY entry(g,n))
		WHERE s.id=$1 AND s.proxy_ingresses<>'[]'::jsonb`, shareID)
	return err
}

// Called under the same recipient/Agent/share locks as ordinary allocations.
// Omission preserves a grant; an explicit empty list revokes it.
func setProxyIngressesTx(ctx context.Context, tx pgx.Tx, agentID, userID string, requested *[]core.ProxyIngress) error {
	if requested == nil {
		return nil
	}
	if len(*requested) > 32 {
		return fmt.Errorf("%w: 最多授权 32 个托管入口", ErrInvalid)
	}
	var id string
	var prior []core.ProxyIngress
	var revision int64
	var supported bool
	if err := tx.QueryRow(ctx, `SELECT s.id,s.proxy_ingresses,s.proxy_ingress_revision,a.features ? $3
	 FROM agent_shares s JOIN agents a ON a.id=s.agent_id WHERE s.agent_id=$1 AND s.user_id=$2`, agentID, userID, core.AgentFeatureSharedProxyIngress).Scan(&id, &prior, &revision, &supported); err != nil {
		return err
	}
	next := append([]core.ProxyIngress{}, (*requested)...)
	sort.Slice(next, func(i, j int) bool { return next[i].Port < next[j].Port })
	for i := range next {
		g := &next[i]
		if !g.Valid() || i > 0 && next[i-1].Port == g.Port {
			return fmt.Errorf("%w: 托管入口需指定 Nginx/Caddy 和不重复的有效端口", ErrInvalid)
		}
		g.Generation = revision + 1 // Client-supplied generations never authorize use.
		for _, old := range prior {
			if old.Port == g.Port && old.Mode == g.Mode {
				g.Generation = old.Generation
			}
		}
		var incompatible bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_shares s, jsonb_array_elements(s.proxy_ingresses) g
		 WHERE s.agent_id=$1 AND s.id<>$2 AND s.enabled AND (g->>'port')::int=$3 AND g->>'mode'<>$4)`, agentID, id, g.Port, g.Mode).Scan(&incompatible); err != nil {
			return err
		}
		if incompatible {
			return fmt.Errorf("%w: 该公共端口已有其他反代类型的授权", ErrConflict)
		}
	}
	if slices.Equal(prior, next) {
		return nil
	}
	if len(next) > 0 && !supported {
		return fmt.Errorf("%w: 请先升级 Agent（%s）再授权共用入口", ErrConflict, core.AgentFeatureSharedProxyIngress)
	}
	_, err := tx.Exec(ctx, `UPDATE agent_shares SET proxy_ingresses=$2,proxy_ingress_revision=proxy_ingress_revision+1,
	 proxy_ingress_error='',updated_at=now() WHERE id=$1`, id, next)
	return err
}

// Authority is derived from the actual configuration owner, never task JSON.
func proxyIngressGrantTx(ctx context.Context, tx pgx.Tx, agentID, owner string, f core.TLSFrontend) (int64, bool, error) {
	if f.LocalOnly {
		return 0, false, nil
	}
	var hostAuthority bool
	// Empty ownership is reserved for the legacy/break-glass administrator's
	// workspace. It has host authority even on a user-owned node. Callers pass
	// the persisted configuration owner, never an owner from the request body.
	if err := tx.QueryRow(ctx, `SELECT $2::text='' OR EXISTS(SELECT 1 FROM agents a WHERE a.id=$1 AND a.owner_id=$2)
	 OR EXISTS(SELECT 1 FROM panel_users u WHERE u.id=$2 AND u.role='admin' AND NOT u.disabled)`, agentID, owner).Scan(&hostAuthority); err != nil {
		return 0, false, err
	}
	if hostAuthority {
		return 0, true, nil
	}
	var generation int64
	err := tx.QueryRow(ctx, `SELECT (g->>'generation')::bigint FROM agent_shares s JOIN panel_users u ON u.id=s.user_id
	 JOIN agents a ON a.id=s.agent_id,
	 jsonb_array_elements(s.proxy_ingresses) g WHERE s.agent_id=$1 AND s.user_id=$2 AND s.enabled AND s.status='accepted'
	 AND a.features ? $5 AND NOT u.disabled AND (g->>'port')::int=$3 AND g->>'mode'=$4`, agentID, owner, f.PublicPort, f.Mode, core.AgentFeatureSharedProxyIngress).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return generation, err == nil, err
}

func (s *Store) cancelUnauthorizedProxyIngressTasksTx(ctx context.Context, tx pgx.Tx, agentID string) error {
	rows, err := tx.Query(ctx, `SELECT t.id,COALESCE(config_content,''),COALESCE(c.owner_id,t.owner_id) FROM tasks t
	 LEFT JOIN configs c ON c.id=t.config_id WHERE t.agent_id=$1 AND t.status IN ('pending','running')
	 AND t.shared_traffic_id<>'' AND t.action IN ('deploy','validate','start','restart')`, agentID)
	if err != nil {
		return err
	}
	type snapshot struct{ id, content, owner string }
	var items []snapshot
	for rows.Next() {
		var item snapshot
		if err := rows.Scan(&item.id, &item.content, &item.owner); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		plain, err := s.decryptContent(item.content)
		if err != nil {
			return err
		}
		_, frontends, err := core.DecodeExecutionSnapshot(plain)
		if err != nil {
			return err
		}
		for _, f := range frontends {
			if f.SharedIngressGeneration == 0 {
				continue
			}
			generation, allowed, err := proxyIngressGrantTx(ctx, tx, agentID, item.owner, f)
			if err != nil {
				return err
			}
			if !allowed || generation != f.SharedIngressGeneration {
				if _, err := tx.Exec(ctx, `UPDATE tasks SET status=CASE WHEN status='running' THEN 'failed' ELSE 'canceled' END,
				 error='托管入口授权已撤销；已分派任务的节点清理等待确认',finished_at=now(),config_content=NULL,lease_id=NULL WHERE id=$1`, item.id); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// Reports acknowledge a complete policy application, including removal of
// unauthorized live routes. Stale reports cannot acknowledge a newer change.
func (s *Store) recordProxyIngressReports(ctx context.Context, agentID string, reports []core.ProxyIngressReport) error {
	if len(reports) > 512 {
		return fmt.Errorf("%w: too many ingress reports", ErrInvalid)
	}
	for _, r := range reports {
		if !core.ValidAgentShareID(r.ShareID) || r.Revision < 1 {
			continue
		}
		message := ""
		if r.Error != "" {
			message = "节点入口清理失败，等待 Agent 重试"
		}
		if _, err := s.pool.Exec(ctx, `UPDATE agent_shares s SET proxy_ingress_applied_revision=$3,
		 proxy_ingress_applied_revoked=$4,proxy_ingress_error=$5 FROM panel_users u WHERE s.id=$2 AND s.agent_id=$1
		 AND u.id=s.user_id AND s.proxy_ingress_revision+s.invitation_revision=$3
		 AND (u.disabled OR NOT s.enabled OR s.status<>'accepted')=$4`, agentID, r.ShareID, r.Revision, r.Revoked, message); err != nil {
			return err
		}
	}
	return nil
}

func proxyIngressShareColumns(alias string) string {
	return alias + `.proxy_ingresses,CASE
	 WHEN ` + alias + `.proxy_ingress_revision=1 THEN ''
	 WHEN ` + alias + `.proxy_ingress_applied_revision=` + alias + `.proxy_ingress_revision+` + alias + `.invitation_revision
	 AND ` + alias + `.proxy_ingress_applied_revoked=(NOT ` + alias + `.enabled OR ` + alias + `.status<>'accepted' OR EXISTS(SELECT 1 FROM panel_users u WHERE u.id=` + alias + `.user_id AND u.disabled))
	 THEN CASE WHEN ` + alias + `.proxy_ingress_error<>'' THEN 'failed' ELSE 'applied' END
	 WHEN NOT EXISTS(SELECT 1 FROM port_traffic_policies p WHERE p.share_id=` + alias + `.id)
	 AND NOT EXISTS(SELECT 1 FROM agent_shared_instance_ownership o WHERE o.share_id=` + alias + `.id AND (o.running OR o.uncertain OR o.config_uncertain))
	 AND NOT EXISTS(SELECT 1 FROM tasks t WHERE t.shared_traffic_id=` + alias + `.id AND t.status IN ('pending','running')) THEN 'idle'
	 ELSE 'pending' END,` + alias + `.proxy_ingress_error`
}

func candidateFrontends(task *core.Task) []core.TLSFrontend {
	if task == nil {
		return nil
	}
	return task.TLSFrontends
}
func proxyIngressConflictMessage(reason string) string {
	switch reason {
	case "proxy_type_conflict":
		return "该公共端口的反代类型不兼容"
	case "tls_certificate_conflict":
		return "同一 SNI 的证书或自动证书方式不一致"
	case "static_page_conflict":
		return "同一站点的静态页面已有归属"
	case "backend_port_conflict":
		return "公共入口与内部监听端口冲突"
	default:
		return "反代路径与现有路由冲突"
	}
}

func taskPortConflictField(task *core.Task, port int) string {
	for _, f := range candidateFrontends(task) {
		if f.PublicPort == port {
			if f.LocalOnly {
				return "xhttp_tls_port"
			}
			return "tls_public_port"
		}
		if f.LocalOnly && f.BackendPort == port {
			return "xhttp_backend_port"
		}
	}
	return "port"
}
