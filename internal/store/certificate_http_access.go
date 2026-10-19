package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Sharing and certificate forms consume the same durable authorization model.
func (s *Store) CertificateHTTPNodes(ctx context.Context) ([]core.CertificateHTTPNode, error) {
	if err := certificateUserActive(ctx, s.pool, certificateActor(ctx)); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT a.id,a.name,a.owner_id=$1,
 COALESCE(a.last_seen>now()-interval '1 minute',false),a.features ? $2,
 COALESCE(sh.enabled,false),COALESCE(sh.status,''),
 ARRAY(SELECT domain FROM certificate_http_grants g WHERE g.agent_id=a.id AND g.user_id=$1 ORDER BY domain)
 FROM agents a LEFT JOIN agent_shares sh ON sh.agent_id=a.id AND sh.user_id=$1
 WHERE a.revoked_at IS NULL AND (a.owner_id=$1 OR sh.id IS NOT NULL) ORDER BY a.name,a.id`, certificateActor(ctx), core.AgentFeatureCertificates)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.CertificateHTTPNode{}
	for rows.Next() {
		var n core.CertificateHTTPNode
		var online, capable, enabled bool
		var status string
		if err = rows.Scan(&n.ID, &n.Name, &n.Owned, &online, &capable, &enabled, &status, &n.Domains); err != nil {
			return nil, err
		}
		switch {
		case !n.Owned && status != "accepted":
			n.Reason = "共享邀请尚未接受，请先在节点共享中接受邀请"
		case !n.Owned && !enabled:
			n.Reason = "共享已停用，请联系节点所有者"
		case !n.Owned && len(n.Domains) == 0:
			n.Reason = "尚未授予 HTTP-01 域名权限，请联系所有者在节点共享中补齐"
		case !capable:
			n.Reason = "请升级验证节点 Agent 以支持证书中心"
		case !online:
			n.Reason = "HTTP-01 验证节点离线，恢复连接后重试"
		default:
			n.Available = true
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func setShareHTTPDomainsTx(ctx context.Context, tx pgx.Tx, agentID, userID string, domains *[]string) error {
	if domains == nil {
		return nil
	} // Existing API clients preserve explicit grants.
	var own bool
	if err := tx.QueryRow(ctx, `SELECT owner_id=$2 FROM agents WHERE id=$1 AND revoked_at IS NULL`, agentID, certificateActor(ctx)).Scan(&own); err != nil {
		return mapError(err)
	}
	if !own {
		return fmt.Errorf("%w: 仅节点所有者可配置 HTTP-01 公网入口权限", ErrForbidden)
	}
	normalized := []string{}
	if len(*domains) > 0 {
		var err error
		normalized, err = certificates.Domains(*domains)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	for _, domain := range normalized {
		if strings.HasPrefix(domain, "*.") {
			return fmt.Errorf("%w: HTTP-01 不支持泛域名，请选择 DNS-01", ErrInvalid)
		}
		var conflict bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM certificate_http_grants WHERE agent_id=$1 AND domain=$2 AND user_id<>$3)`, agentID, domain, userID).Scan(&conflict); err != nil {
			return err
		}
		if conflict {
			return fmt.Errorf("%w: 域名 %s 已授权给另一用户", ErrConflict, domain)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM certificate_http_grants WHERE agent_id=$1 AND user_id=$2`, agentID, userID); err != nil {
		return err
	}
	for _, domain := range normalized {
		if _, err := tx.Exec(ctx, `INSERT INTO certificate_http_grants(agent_id,user_id,domain) VALUES($1,$2,$3)`, agentID, userID, domain); err != nil {
			return mapError(err)
		}
	}
	_, err := tx.Exec(ctx, `DELETE FROM certificate_http_challenges h USING certificate_orders o WHERE o.id=h.order_id AND h.agent_id=$1 AND o.owner_id=$2 AND NOT(h.domain=ANY($3::text[]))`, agentID, userID, normalized)
	return err
}
