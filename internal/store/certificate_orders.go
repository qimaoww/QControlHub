package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
	"golang.org/x/crypto/acme"
)

type certificateChallengeState struct {
	BeforeIDs     []string                  `json:"before_ids,omitempty"`
	CreatePending bool                      `json:"create_pending,omitempty"`
	Public        core.CertificateChallenge `json:"public"`
	URL           string                    `json:"url"`
	AuthURL       string                    `json:"auth_url,omitempty"`
	Record        certificates.DNSRecord    `json:"record"`
}
type certificateOrderState struct {
	URL        string                      `json:"url"`
	Key        string                      `json:"key"`
	CSR        []byte                      `json:"csr"`
	Challenges []certificateChallengeState `json:"challenges"`
	Cleanup    bool                        `json:"cleanup"`
}

func (s *Store) CreateACMECertificate(ctx context.Context, item core.Certificate) (core.Certificate, error) {
	if item.Method != "dns-api" && item.Method != "dns-manual" && item.Method != "http-01" {
		return item, ErrInvalid
	}
	if item.AutoRenew && item.Method == "dns-manual" {
		return item, fmt.Errorf("%w: 手动 DNS 续期需要人工配合", ErrInvalid)
	}
	if _, _, err := certificates.NewKey(item.KeyType); err != nil {
		return item, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var account certificates.ACMEAccount
	meta, err := s.CertificateAccountSecret(ctx, item.AccountID, "acme", &account)
	if err != nil {
		return item, err
	}
	item.CA = meta.CA
	item.Source = "acme"
	item.Status = "queued"
	if item.Method == "dns-api" {
		var credentials certificates.DNSCredentials
		if _, err = s.CertificateAccountSecret(ctx, item.DNSAccountID, "dns", &credentials); err != nil {
			return item, err
		}
	} else {
		item.DNSAccountID = ""
	}
	names, err := certificates.Domains(item.Domains)
	if err != nil {
		return item, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	item.Domains = names
	if item.Method == "http-01" {
		for _, name := range names {
			if strings.HasPrefix(name, "*.") {
				return item, fmt.Errorf("%w: HTTP-01 不支持泛域名，请选择 DNS-01", ErrInvalid)
			}
			if err = s.CheckCertificateHTTPPermission(ctx, item.AgentID, name); err != nil {
				return item, err
			}
		}
	} else {
		item.AgentID = ""
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return item, err
	}
	defer tx.Rollback(ctx)
	if err = s.createCertificateTx(ctx, tx, &item); err != nil {
		return item, err
	}
	if err = s.queueCertificateOrderTx(ctx, tx, item); err != nil {
		return item, err
	}
	return item, tx.Commit(ctx)
}
func (s *Store) queueCertificateOrderTx(ctx context.Context, tx pgx.Tx, item core.Certificate) error {
	sealed, err := s.sealCertificate(certificateOrderState{})
	if err != nil {
		return err
	}
	id, err := core.NewID("cor")
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO certificate_orders(id,certificate_id,owner_id,status,secret) VALUES($1,$2,$3,'queued',$4)`, id, item.ID, item.OwnerID, sealed)
	return mapError(err)
}
func (s *Store) RenewCertificate(ctx context.Context, id string) error {
	item, err := s.GetCertificate(ctx, id)
	if err != nil {
		return err
	}
	if !item.Owned {
		return ErrForbidden
	}
	if item.Source != "acme" {
		return fmt.Errorf("%w: 导入与自签证书请使用替换入口", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM panel_certificates WHERE id=$1 FOR UPDATE`, id); err != nil {
		return err
	}
	if err = s.queueCertificateOrderTx(ctx, tx, item); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) CertificateOrders(ctx context.Context, id string) ([]core.CertificateOrder, error) {
	item, err := s.GetCertificate(ctx, id)
	if err != nil {
		return nil, err
	}
	if !item.Owned {
		return []core.CertificateOrder{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id,status,stage,error,attempt,created_at,updated_at,secret FROM certificate_orders WHERE certificate_id=$1 AND owner_id=$2 ORDER BY created_at DESC LIMIT 50`, id, certificateActor(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.CertificateOrder{}
	for rows.Next() {
		order := core.CertificateOrder{CertificateID: id}
		var secret string
		if err := rows.Scan(&order.ID, &order.Status, &order.Stage, &order.Error, &order.Attempt, &order.CreatedAt, &order.UpdatedAt, &secret); err != nil {
			return nil, err
		}
		var state certificateOrderState
		if err = s.openCertificate(secret, &state); err != nil {
			return nil, err
		}
		order.Challenges = []core.CertificateChallenge{}
		for _, challenge := range state.Challenges {
			order.Challenges = append(order.Challenges, challenge.Public)
		}
		out = append(out, order)
	}
	return out, rows.Err()
}
func (s *Store) ContinueCertificateOrder(ctx context.Context, id, orderID string, cancel bool) error {
	item, err := s.GetCertificate(ctx, id)
	if err != nil {
		return err
	}
	if !item.Owned {
		return ErrForbidden
	}
	status := "queued"
	valid := []string{"waiting_dns"}
	if cancel {
		status = "canceling"
		valid = []string{"queued", "running", "waiting_dns", "validating", "failed"}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE certificate_orders SET status=$4,next_attempt=now(),updated_at=now() WHERE id=$1 AND certificate_id=$2 AND owner_id=$3 AND status=ANY($5::text[])`, orderID, id, certificateActor(ctx), status, valid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}
func (s *Store) CheckCertificateHTTPPermission(ctx context.Context, agentID, domain string) error {
	var allowed, capable, online bool
	err := s.pool.QueryRow(ctx, `SELECT (a.owner_id=$2 OR (EXISTS(SELECT 1 FROM certificate_http_grants g JOIN agent_shares sh ON sh.agent_id=g.agent_id AND sh.user_id=g.user_id WHERE g.agent_id=a.id AND g.user_id=$2 AND g.domain=$3 AND sh.enabled AND sh.status='accepted'))),a.features ? $4,COALESCE(a.last_seen>now()-interval '1 minute',false) FROM agents a WHERE a.id=$1 AND a.revoked_at IS NULL`, agentID, certificateActor(ctx), domain, core.AgentFeatureCertificates).Scan(&allowed, &capable, &online)
	if err != nil {
		return mapError(err)
	}
	if !allowed {
		return fmt.Errorf("%w: 此域名的节点 HTTP-01 公网入口未授权", ErrForbidden)
	}
	if !capable {
		return fmt.Errorf("%w: 请升级验证节点 Agent 以支持证书中心", ErrConflict)
	}
	if !online {
		return fmt.Errorf("%w: HTTP-01 验证节点离线，恢复连接后重试", ErrConflict)
	}
	return certificateUserActive(ctx, s.pool, certificateActor(ctx))
}
func (s *Store) SetCertificateHTTPGrant(ctx context.Context, grant core.HTTPChallengeGrant, remove bool) error {
	names, err := certificates.Domains([]string{grant.Domain})
	if err != nil || strings.HasPrefix(grant.Domain, "*.") {
		return ErrInvalid
	}
	grant.Domain = names[0]
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return err
	}
	var owner string
	if err = tx.QueryRow(ctx, `SELECT owner_id FROM agents WHERE id=$1 AND owner_id=$2 AND revoked_at IS NULL FOR UPDATE`, grant.AgentID, certificateActor(ctx)).Scan(&owner); err != nil {
		return mapError(err)
	}
	var otherOwner bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM certificate_http_grants WHERE agent_id=$1 AND domain=$2 AND user_id<>$3)`, grant.AgentID, grant.Domain, grant.UserID).Scan(&otherOwner); err != nil {
		return err
	}
	if otherOwner {
		return fmt.Errorf("%w: 此域名入口已分配给另一用户", ErrConflict)
	}
	if remove {
		_, err = tx.Exec(ctx, `DELETE FROM certificate_http_grants WHERE agent_id=$1 AND domain=$2 AND user_id=$3`, grant.AgentID, grant.Domain, grant.UserID)
	} else {
		var allowed bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_shares s JOIN panel_users u ON u.id=s.user_id WHERE s.agent_id=$1 AND s.user_id=$2 AND s.enabled AND s.status='accepted' AND NOT u.disabled)`, grant.AgentID, grant.UserID).Scan(&allowed)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrForbidden
		}
		_, err = tx.Exec(ctx, `INSERT INTO certificate_http_grants(agent_id,domain,user_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, grant.AgentID, grant.Domain, grant.UserID)
	}
	if err != nil {
		return mapError(err)
	}
	return tx.Commit(ctx)
}
func (s *Store) CertificateHTTPGrants(ctx context.Context) ([]core.HTTPChallengeGrant, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.agent_id,g.user_id,g.domain FROM certificate_http_grants g JOIN agents a ON a.id=g.agent_id WHERE a.owner_id=$1 OR g.user_id=$1`, certificateActor(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.HTTPChallengeGrant{}
	for rows.Next() {
		var g core.HTTPChallengeGrant
		if err = rows.Scan(&g.AgentID, &g.UserID, &g.Domain); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Only the signed Agent websocket can read this public challenge payload.
func (s *Store) AgentCertificateChallenges(ctx context.Context, id string) ([]core.HTTPChallenge, error) {
	rows, err := s.pool.Query(ctx, `SELECT h.order_id,h.domain,h.token,h.value,h.expires_at FROM certificate_http_challenges h JOIN certificate_orders o ON o.id=h.order_id JOIN agents a ON a.id=h.agent_id WHERE h.agent_id=$1 AND h.expires_at>now() AND o.status IN ('queued','running','validating') AND NOT EXISTS(SELECT 1 FROM panel_users u WHERE u.id=o.owner_id AND u.disabled) AND (a.owner_id=o.owner_id OR EXISTS(SELECT 1 FROM certificate_http_grants g JOIN agent_shares sh ON sh.agent_id=g.agent_id AND sh.user_id=g.user_id WHERE g.agent_id=a.id AND g.domain=h.domain AND g.user_id=o.owner_id AND sh.enabled AND sh.status='accepted'))`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.HTTPChallenge{}
	for rows.Next() {
		var h core.HTTPChallenge
		if err = rows.Scan(&h.OrderID, &h.Domain, &h.Token, &h.Value, &h.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
func (s *Store) saveCertificateOrderState(ctx context.Context, id string, state certificateOrderState, stage string) error {
	sealed, err := s.sealCertificate(state)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE certificate_orders SET secret=$2,stage=$3,updated_at=now() WHERE id=$1 AND status NOT IN ('canceling','canceled','succeeded')`, id, sealed, stage)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("订单已取消或结束")
	}
	return nil
}
func (s *Store) certificateOrderCanceled(ctx context.Context, id string) bool {
	var status string
	return s.pool.QueryRow(ctx, `SELECT status FROM certificate_orders WHERE id=$1`, id).Scan(&status) != nil || status == "canceling" || status == "canceled"
}
func (s *Store) cleanupCertificateOrder(ctx context.Context, item core.Certificate, id string, state *certificateOrderState) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM certificate_http_challenges WHERE order_id=$1`, id); err != nil {
		return err
	}
	if item.Method == "dns-api" {
		var credentials certificates.DNSCredentials
		account, err := s.CertificateAccountSecret(ctx, item.DNSAccountID, "dns", &credentials)
		if err != nil {
			return err
		}
		client := certificates.DNSClient{Provider: account.Provider, Credentials: credentials, HTTP: s.certificateDNSHTTP}
		for i := range state.Challenges {
			if state.Challenges[i].CreatePending && state.Challenges[i].Record.ID == "" {
				if err = s.recoverDNSCreate(ctx, client, &state.Challenges[i]); err != nil {
					return err
				}
			}
			r := state.Challenges[i].Record
			if r.ID != "" {
				// A prior cleanup may have deleted the record just before a
				// crash. Exact lookup makes retry safe without touching peers.
				records, findErr := client.Find(ctx, state.Challenges[i].Public.Name, state.Challenges[i].Public.Value)
				if findErr != nil {
					return findErr
				}
				for _, existing := range records {
					if existing.ID == r.ID {
						if err = client.Delete(ctx, r); err != nil {
							return err
						}
					}
				}
				state.Challenges[i].Record = certificates.DNSRecord{}
				sealed, sealErr := s.sealCertificate(state)
				if sealErr != nil {
					return sealErr
				}
				if _, err = s.pool.Exec(ctx, `UPDATE certificate_orders SET secret=$2 WHERE id=$1`, id, sealed); err != nil {
					return err
				}
			}
		}
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM certificate_dns_leases WHERE order_id=$1`, id); err != nil {
		return err
	}
	state.Cleanup = true
	sealed, err := s.sealCertificate(state)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE certificate_orders SET secret=$2 WHERE id=$1`, id, sealed)
	return err
}

// HTTP preflight checks every address without proxying or following redirects.
// The caller checks node/domain authority before any public network operation.
func checkHTTPChallenge(ctx context.Context, domain, token, value string) error {
	return checkHTTPChallengeWith(ctx, domain, token, value, net.DefaultResolver.LookupIPAddr, (&net.Dialer{Timeout: 5 * time.Second}).DialContext)
}
func checkHTTPChallengeWith(ctx context.Context, domain, token, value string, lookup func(context.Context, string) ([]net.IPAddr, error), dial func(context.Context, string, string) (net.Conn, error)) error {
	addresses, err := lookup(ctx, domain)
	if err != nil || len(addresses) == 0 {
		return errors.New("A/AAAA 解析失败")
	}
	if len(addresses) > 32 {
		return errors.New("域名地址数量过多")
	}
	for _, address := range addresses {
		if !netpolicy.IsPublicAddress(netip.MustParseAddr(address.IP.String())) {
			return errors.New("HTTP-01 必须使用公网 A/AAAA 地址")
		}
		ip := address.IP.String()
		transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dial(ctx, "tcp", net.JoinHostPort(ip, "80"))
		}}
		client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+domain+"/.well-known/acme-challenge/"+token, nil)
		resp, err := client.Do(req)
		if err != nil {
			transport.CloseIdleConnections()
			return fmt.Errorf("%s:80 无法访问；检查防火墙、IPv4/IPv6 与挑战路由", ip)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		transport.CloseIdleConnections()
		if resp.StatusCode != 200 || string(body) != value {
			return fmt.Errorf("%s:80 挑战路径不匹配；请将现有站点的 ACME 路由转发至节点 127.0.0.1:9080，并保留 Host", ip)
		}
	}
	return nil
}
func (s *Store) processCertificateOrder(ctx context.Context, item core.Certificate, id, status string, state certificateOrderState) error {
	if status == "canceling" {
		if err := s.cleanupCertificateOrder(ctx, item, id, &state); err != nil {
			return err
		}
		_, err := s.pool.Exec(ctx, `UPDATE certificate_orders SET status='canceled',stage='canceled',updated_at=now() WHERE id=$1`, id)
		return err
	}
	if err := certificateUserActive(ctx, s.pool, item.OwnerID); err != nil {
		return err
	}
	var account certificates.ACMEAccount
	accountMeta, err := s.CertificateAccountSecret(ctx, item.AccountID, "acme", &account)
	if err != nil {
		return err
	}
	client, err := certificates.Client(accountMeta.CA, account)
	if err != nil {
		return err
	}
	var order *acme.Order
	if state.URL == "" {
		order, err = client.AuthorizeOrder(ctx, acme.DomainIDs(item.Domains...))
		if err != nil {
			return errors.New("ACME 创建订单失败，请检查域名、账号和 CA 限额")
		}
		state.URL = order.URI
		state.CSR, state.Key, err = certificates.CSR(item.Domains, item.KeyType)
		if err != nil {
			return err
		}
		if err = s.saveCertificateOrderState(ctx, id, state, "order_created"); err != nil {
			return err
		}
	} else {
		order, err = client.GetOrder(ctx, state.URL)
		if err != nil {
			return errors.New("ACME 订单读取失败")
		}
	}
	if order.Status == acme.StatusInvalid {
		return errors.New("ACME 订单已失效，请重新申请")
	}
	if len(state.Challenges) == 0 {
		for _, authURL := range order.AuthzURLs {
			auth, err := client.GetAuthorization(ctx, authURL)
			if err != nil {
				return errors.New("ACME 授权读取失败")
			}
			if auth.Status == acme.StatusValid {
				continue
			}
			typ := "dns-01"
			if item.Method == "http-01" {
				typ = "http-01"
			}
			var challenge *acme.Challenge
			for _, c := range auth.Challenges {
				if c.Type == typ {
					challenge = c
					break
				}
			}
			if challenge == nil {
				return errors.New("CA 不支持所选验证方式")
			}
			name := "_acme-challenge." + strings.TrimPrefix(auth.Identifier.Value, "*.")
			var value string
			if typ == "dns-01" {
				value, err = client.DNS01ChallengeRecord(challenge.Token)
			} else {
				value, err = client.HTTP01ChallengeResponse(challenge.Token)
			}
			if err != nil {
				return err
			}
			state.Challenges = append(state.Challenges, certificateChallengeState{Public: core.CertificateChallenge{Domain: auth.Identifier.Value, Token: challenge.Token, Value: value, Name: name, Type: typ, Status: auth.Status}, URL: challenge.URI, AuthURL: authURL})
		}
		if err = s.saveCertificateOrderState(ctx, id, state, "challenges_ready"); err != nil {
			return err
		}
	}
	if item.Method == "dns-manual" && status != "queued" {
		_, err = s.pool.Exec(ctx, `UPDATE certificate_orders SET status='waiting_dns',stage='waiting_dns',updated_at=now() WHERE id=$1 AND status<>'canceling'`, id)
		return err
	}
	// A newly-created manual order stops before validation. Continue uses queued
	// with persisted challenges and bypasses this first-visit condition.
	if item.Method == "dns-manual" && status == "queued" && state.Cleanup == false {
		var stage string
		s.pool.QueryRow(ctx, `SELECT stage FROM certificate_orders WHERE id=$1`, id).Scan(&stage)
		if stage == "challenges_ready" {
			_, err = s.pool.Exec(ctx, `UPDATE certificate_orders SET status='waiting_dns',stage='waiting_dns',updated_at=now() WHERE id=$1 AND status<>'canceling'`, id)
			return err
		}
	}
	if item.Method == "dns-api" {
		var credentials certificates.DNSCredentials
		meta, err := s.CertificateAccountSecret(ctx, item.DNSAccountID, "dns", &credentials)
		if err != nil {
			return err
		}
		dns := certificates.DNSClient{Provider: meta.Provider, Credentials: credentials, HTTP: s.certificateDNSHTTP}
		for i := range state.Challenges {
			c := &state.Challenges[i]
			if s.certificateOrderCanceled(ctx, id) {
				return errors.New("订单已取消")
			}
			if c.Record.ID == "" && c.CreatePending {
				if err = s.recoverDNSCreate(ctx, dns, c); err != nil {
					return err
				}
			}
			if c.Record.ID == "" {
				tag, leaseErr := s.pool.Exec(ctx, `INSERT INTO certificate_dns_leases(name,value,order_id) VALUES($1,$2,$3) ON CONFLICT(name,value) DO UPDATE SET order_id=EXCLUDED.order_id WHERE certificate_dns_leases.order_id=EXCLUDED.order_id`, c.Public.Name, c.Public.Value, id)
				if leaseErr != nil {
					return leaseErr
				}
				if tag.RowsAffected() == 0 {
					return errors.New("同名 DNS 挑战正在由其他订单处理，稍后重试")
				}
				previous, findErr := dns.Find(ctx, c.Public.Name, c.Public.Value)
				if findErr != nil {
					return findErr
				}
				c.BeforeIDs = nil
				for _, record := range previous {
					c.BeforeIDs = append(c.BeforeIDs, record.ID)
				}
				c.CreatePending = true
				if err = s.saveCertificateOrderState(ctx, id, state, "dns_create_intent"); err != nil {
					return err
				}
				c.Record, err = dns.Create(ctx, c.Public.Name, c.Public.Value)
				if err != nil {
					return err
				}
				c.CreatePending = false
				if err = s.saveCertificateOrderState(ctx, id, state, "dns_created"); err != nil {
					cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
					defer done()
					_ = dns.Delete(cleanup, c.Record)
					return err
				}
			}
		}
	}
	if item.Method == "http-01" {
		for _, challenge := range state.Challenges {
			if err = s.CheckCertificateHTTPPermission(ctx, item.AgentID, challenge.Public.Domain); err != nil {
				return err
			}
			_, err = s.pool.Exec(ctx, `INSERT INTO certificate_http_challenges(order_id,agent_id,domain,token,value,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '15 minutes') ON CONFLICT(order_id,domain,token) DO UPDATE SET expires_at=EXCLUDED.expires_at`, id, item.AgentID, challenge.Public.Domain, challenge.Public.Token, challenge.Public.Value)
			if err != nil {
				return err
			}
		}
		if err = s.saveCertificateOrderState(ctx, id, state, "http_preflight"); err != nil {
			return err
		}
	}
	for i := range state.Challenges {
		c := &state.Challenges[i]
		if c.AuthURL != "" {
			auth, authErr := client.GetAuthorization(ctx, c.AuthURL)
			if authErr != nil {
				return errors.New("ACME 域名授权状态读取失败")
			}
			c.Public.Status = auth.Status
			if auth.Status == acme.StatusInvalid {
				return fmt.Errorf("域名验证失败: %s；请检查 DNS 或公网挑战入口后重新申请", c.Public.Domain)
			}
		}
		if c.Public.Status == acme.StatusValid {
			continue
		}
		if s.certificateOrderCanceled(ctx, id) {
			return errors.New("订单已取消")
		}
		if item.Method == "http-01" {
			if err = s.CheckCertificateHTTPPermission(ctx, item.AgentID, c.Public.Domain); err != nil {
				return err
			}
			if err = checkHTTPChallenge(ctx, c.Public.Domain, c.Public.Token, c.Public.Value); err != nil {
				return fmt.Errorf("域名 %s: %w", c.Public.Domain, err)
			}
		} else {
			values, lookupErr := s.certificateTXTResolver().LookupTXT(ctx, c.Public.Name)
			found := false
			for _, v := range values {
				found = found || v == c.Public.Value
			}
			if lookupErr != nil || !found {
				return fmt.Errorf("DNS TXT 尚未传播: %s；请保留已有记录，等待后继续", c.Public.Name)
			}
		}
		if _, err = client.Accept(ctx, &acme.Challenge{URI: c.URL}); err != nil {
			return errors.New("CA 拒绝挑战；检查验证入口后重新申请")
		}
		c.Public.Status = "processing"
		if err = s.saveCertificateOrderState(ctx, id, state, "validating"); err != nil {
			return err
		}
	}
	order, err = client.WaitOrder(ctx, state.URL)
	if err != nil {
		return errors.New("CA 验证尚未完成或失败，请检查所有域名入口")
	}
	for i := range state.Challenges {
		state.Challenges[i].Public.Status = acme.StatusValid
	}
	if err = s.saveCertificateOrderState(ctx, id, state, "finalizing"); err != nil {
		return err
	}
	if s.certificateOrderCanceled(ctx, id) {
		return errors.New("订单已取消")
	}
	var pair certificates.Pair
	if order.Status == acme.StatusValid && order.CertURL != "" {
		chain, err := client.FetchCert(ctx, order.CertURL, true)
		if err != nil {
			return errors.New("CA 证书下载失败")
		}
		pair = certificatePairFromDER(chain, state.Key)
	} else {
		order.URI = state.URL
		pair, err = certificates.Finalize(ctx, client, order, state.CSR, state.Key)
		if err != nil {
			return err
		}
	}
	if err = certificates.Covers(pair, item.Domains); err != nil {
		return err
	}
	info, err := certificates.Inspect(pair, time.Now())
	if err != nil {
		return err
	}
	sealed, err := s.sealCertificate(pair)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentStatus string
	if err = tx.QueryRow(ctx, `SELECT status FROM certificate_orders WHERE id=$1 FOR UPDATE`, id).Scan(&currentStatus); err != nil {
		return err
	}
	if currentStatus == "canceling" {
		return errors.New("订单已取消")
	}
	if err = certificateUserActive(ctx, tx, item.OwnerID); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `UPDATE panel_certificates SET version=version+1 WHERE id=$1 RETURNING version`, item.ID).Scan(&item.Version); err != nil {
		return err
	}
	item.NotBefore = &info.NotBefore
	item.NotAfter = &info.NotAfter
	item.Fingerprint = info.Fingerprint
	item.KeyType = info.KeyType
	item.Status = "issued"
	item.Error = ""
	raw, _ := json.Marshal(item)
	if _, err = tx.Exec(ctx, `INSERT INTO certificate_versions(certificate_id,version,secret,metadata) VALUES($1,$2,$3,$4)`, item.ID, item.Version, sealed, raw); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE panel_certificates SET metadata=$2,next_attempt=now()+interval '1 day' WHERE id=$1`, item.ID, raw); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE certificate_orders SET status='succeeded',stage='cleanup',updated_at=now(),error='' WHERE id=$1`, id); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return s.cleanupCertificateOrder(ctx, item, id, &state)
}

func (s *Store) certificateTXTResolver() *net.Resolver {
	if s.certificateResolver != nil {
		return s.certificateResolver
	}
	return net.DefaultResolver
}

func (s *Store) recoverDNSCreate(ctx context.Context, client certificates.DNSClient, c *certificateChallengeState) error {
	records, err := client.Find(ctx, c.Public.Name, c.Public.Value)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, id := range c.BeforeIDs {
		known[id] = true
	}
	created := []certificates.DNSRecord{}
	for _, r := range records {
		if !known[r.ID] {
			created = append(created, r)
		}
	}
	if len(created) > 1 {
		return errors.New("DNS 创建结果不明确，保留记录并等待人工检查")
	}
	if len(created) == 1 {
		c.Record = created[0]
	}
	c.CreatePending = false
	return nil
}
