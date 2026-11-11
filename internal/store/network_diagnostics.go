package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) UpdateDiagnosticCapabilities(ctx context.Context, id string, c *core.DiagnosticCapabilities) error {
	if c == nil {
		return nil
	}
	b, e := json.Marshal(c)
	if e != nil || len(b) > 16384 {
		return ErrInvalid
	}
	_, e = s.pool.Exec(ctx, `INSERT INTO network_diagnostic_capabilities(agent_id,capabilities) VALUES($1,$2)
 ON CONFLICT(agent_id) DO UPDATE SET capabilities=EXCLUDED.capabilities,updated_at=now()
 WHERE network_diagnostic_capabilities.capabilities IS DISTINCT FROM EXCLUDED.capabilities`, id, b)
	return e
}

func diagnosticEndpointAccess(ctx context.Context, ex storeExecutor, endpoint core.DiagnosticEndpoint) error {
	if err := requireAgentAccess(ctx, ex, endpoint.AgentID); err != nil {
		return err
	}
	scope := scopeForConfig(ctx)
	var owner string
	if err := ex.QueryRow(ctx, `SELECT owner_id FROM agents WHERE id=$1 AND revoked_at IS NULL`, endpoint.AgentID).Scan(&owner); err != nil {
		return mapError(err)
	}
	if endpoint.InstanceID == "" {
		if scope.Admin || owner == scope.OwnerID || strings.HasPrefix(scope.OwnerID, "token_") {
			return nil
		}
		return ErrForbidden
	}
	var allowed bool
	if err := ex.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_shares sh JOIN network_diagnostic_grants g ON g.share_id=sh.id
 WHERE sh.id=$1 AND sh.agent_id=$2 AND sh.user_id=$3 AND sh.enabled AND sh.status='accepted' AND cardinality(sh.engines)>0 AND g.enabled)`, endpoint.InstanceID, endpoint.AgentID, scope.OwnerID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%w: 共享实例未获节点网络诊断授权", ErrForbidden)
	}
	return nil
}

func (s *Store) DiagnosticNodes(ctx context.Context) ([]core.DiagnosticNode, error) {
	if err := requireTaskPermission(ctx, s.pool, core.ActionStatus, false); err != nil {
		return nil, err
	}
	agents, err := s.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	result := []core.DiagnosticNode{}
	for _, a := range agents {
		n := core.DiagnosticNode{AgentID: a.ID, Name: a.Name, Online: a.Status == "online", Context: "节点网络"}
		var sh string
		if err := s.pool.QueryRow(ctx, `SELECT id FROM agent_shares WHERE agent_id=$1 AND user_id=$2 AND enabled AND status='accepted'`, a.ID, scopeForConfig(ctx).OwnerID).Scan(&sh); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if a.OwnerID != scopeForConfig(ctx).OwnerID && !scopeForConfig(ctx).Admin {
			n.InstanceID = sh
		}
		if err = diagnosticEndpointAccess(ctx, s.pool, core.DiagnosticEndpoint{AgentID: a.ID, InstanceID: n.InstanceID}); err != nil {
			continue
		}
		var b []byte
		if err = s.pool.QueryRow(ctx, `SELECT capabilities FROM network_diagnostic_capabilities WHERE agent_id=$1`, a.ID).Scan(&b); err == nil {
			if !slices.Contains(a.Features, core.AgentFeatureNetworkDiagnostics) {
				b = []byte(`{"tools":{},"providers":{},"addresses":[]}`)
			}
			if err = json.Unmarshal(b, &n.Capabilities); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		result = append(result, n)
	}
	return result, nil
}

// An explicit grant permits bounded diagnostics in the node's host network;
// it conveys no shell, firewall, configuration or other user's data access.
func (s *Store) SetDiagnosticGrant(ctx context.Context, agentID, shareID string, enabled bool, revision int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockAgentUser(ctx, tx); err != nil {
		return err
	}
	if err = requireAgentAdministration(ctx, tx, agentID); err != nil {
		return err
	}
	var belongs bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_shares WHERE id=$1 AND agent_id=$2)`, shareID, agentID).Scan(&belongs); err != nil {
		return err
	}
	if !belongs {
		return ErrNotFound
	}
	var affected int64
	if revision == 0 {
		command, e := tx.Exec(ctx, `INSERT INTO network_diagnostic_grants(share_id,enabled) VALUES($1,$2) ON CONFLICT DO NOTHING`, shareID, enabled)
		err = e
		affected = command.RowsAffected()
	} else {
		command, e := tx.Exec(ctx, `UPDATE network_diagnostic_grants SET enabled=$2,revision=revision+1,updated_at=now() WHERE share_id=$1 AND revision=$3`, shareID, enabled, revision)
		err = e
		affected = command.RowsAffected()
	}
	if err == nil && affected == 0 {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) DiagnosticGrants(ctx context.Context, agentID string) (map[string]core.DiagnosticGrant, error) {
	if err := requireAgentAdministration(ctx, s.pool, agentID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT sh.id,COALESCE(g.enabled,false),COALESCE(g.revision,0) FROM agent_shares sh LEFT JOIN network_diagnostic_grants g ON g.share_id=sh.id WHERE sh.agent_id=$1`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]core.DiagnosticGrant{}
	for rows.Next() {
		var id string
		var b core.DiagnosticGrant
		if err = rows.Scan(&id, &b.Enabled, &b.Revision); err != nil {
			return nil, err
		}
		out[id] = b
	}
	return out, rows.Err()
}

func (s *Store) CreateDiagnostic(ctx context.Context, in core.DiagnosticRequest) (core.DiagnosticSession, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.DiagnosticSession{}, err
	}
	defer tx.Rollback(ctx)
	fail := func(e error) (core.DiagnosticSession, error) { return core.DiagnosticSession{}, e }
	if err = lockAgentUser(ctx, tx); err != nil {
		return fail(err)
	}
	if err = requireTaskPermission(ctx, tx, core.ActionStatus, false); err != nil {
		return fail(err)
	}
	if in.RequestID == "" || len(in.RequestID) > 80 || strings.ContainsAny(in.RequestID, "\n\r\x00") {
		return fail(ErrInvalid)
	}
	if in.TargetMode != "" && in.TargetMode != "ip" {
		return fail(ErrInvalid)
	}
	if in.TargetMode == "ip" {
		if in.Node2.AgentID != "" || in.Node2.InstanceID != "" || in.Bidirectional {
			return fail(fmt.Errorf("%w: 自定义 IP 不支持双向执行或关联节点", ErrInvalid))
		}
		ip, e := core.DiagnosticAddress(in.Node2.Address)
		if e != nil {
			return fail(fmt.Errorf("%w: %v", ErrInvalid, e))
		}
		in.Node2.Name = ip.String()
		if in.Node1.Address == in.Node2.Address {
			return fail(fmt.Errorf("%w: 目标 IP 不能与源地址相同", ErrInvalid))
		}
	}
	if in.Node1.AgentID == in.Node2.AgentID {
		return fail(fmt.Errorf("%w: 请选择两个不同节点", ErrInvalid))
	}
	// Serialize capacity reservations in stable node order, including two
	// overlapping requests with reversed endpoint order.
	ids := []string{in.Node1.AgentID}
	if in.TargetMode != "ip" {
		ids = append(ids, in.Node2.AgentID)
	}
	slices.Sort(ids)
	for _, id := range ids {
		var found string
		if err = tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, id).Scan(&found); err != nil {
			return fail(mapError(err))
		}
	}
	endpoints := []*core.DiagnosticEndpoint{&in.Node1}
	if in.TargetMode != "ip" {
		endpoints = append(endpoints, &in.Node2)
	}
	for _, ep := range endpoints {
		if err = diagnosticEndpointAccess(ctx, tx, *ep); err != nil {
			return fail(err)
		}
		var caps []byte
		var fresh bool
		var features []byte
		if err = tx.QueryRow(ctx, `SELECT a.name,a.last_seen>now()-(`+strings.ReplaceAll(agentOfflineThresholdSQL, "agents.", "a.")+`)*interval '1 second',c.capabilities,a.features FROM agents a JOIN network_diagnostic_capabilities c ON c.agent_id=a.id WHERE a.id=$1`, ep.AgentID).Scan(&ep.Name, &fresh, &caps, &features); err != nil {
			return fail(fmt.Errorf("%w: 节点需要支持网络诊断的 Agent", ErrConflict))
		}
		if !strings.Contains(string(features), core.AgentFeatureNetworkDiagnostics) {
			return fail(fmt.Errorf("%w: 请升级 Agent 后执行网络诊断", ErrConflict))
		}
		if !fresh {
			return fail(fmt.Errorf("%w: 节点离线", ErrConflict))
		}
		var c core.DiagnosticCapabilities
		if err = json.Unmarshal(caps, &c); err != nil {
			return fail(err)
		}
		if !slices.Contains(c.Addresses, ep.Address) {
			return fail(fmt.Errorf("%w: 地址未分配到所选节点；NAT 或代理入口不能直接作为节点地址", ErrInvalid))
		}
		ip, e := core.DiagnosticAddress(ep.Address)
		if e != nil {
			return fail(fmt.Errorf("%w: %v", ErrInvalid, e))
		}
		if e = in.Parameters.Validate(ip.Is6()); e != nil {
			return fail(fmt.Errorf("%w: %v", ErrInvalid, e))
		}
		tool := in.Parameters.Tool
		if tool == "mtr" || tool == "traceroute" {
			tool = "nexttrace"
		}
		t, ok := c.Tools[tool]
		if !ok || t.Error != "" || t.Version == "" {
			return fail(fmt.Errorf("%w: %s：%s", ErrConflict, ep.Name, t.Error))
		}
		if tool == "nexttrace" {
			reason, ok := c.Providers[in.Parameters.Provider]
			if !ok || reason != "" {
				return fail(fmt.Errorf("%w: %s 查询提供方不可用：%s", ErrConflict, ep.Name, reason))
			}
		}
		if ep.InstanceID != "" && in.Parameters.Tool == "iperf3" {
			var allowed bool
			budget := in.Parameters.Bandwidth * int64(in.Parameters.Streams) * int64(in.Parameters.Duration) / 8 * 2
			// Reserve a conservative upper bound immediately. The reservation remains
			// charged on interrupted/unknown tests instead of allowing quota bypass.
			if err = tx.QueryRow(ctx, `SELECT ($4 OR sh.ports_unrestricted OR EXISTS(SELECT 1 FROM agent_share_ports p WHERE p.share_id=sh.id AND p.port=$2)) AND (sh.limit_bytes=0 OR sh.used_bytes+$3<=sh.limit_bytes) FROM agent_shares sh WHERE sh.id=$1 FOR UPDATE`, ep.InstanceID, in.Parameters.Port, budget, ep.AgentID != in.Node2.AgentID).Scan(&allowed); err != nil {
				return fail(err)
			}
			if !allowed {
				return fail(fmt.Errorf("%w: 共享实例端口未授权或剩余额度不足", ErrForbidden))
			}
		}
	}
	a, _ := core.DiagnosticAddress(in.Node1.Address)
	b, _ := core.DiagnosticAddress(in.Node2.Address)
	if a.Is6() != b.Is6() {
		return fail(fmt.Errorf("%w: 两端必须使用同一地址族", ErrInvalid))
	}
	if in.Bidirectional && in.Parameters.Tool != "mtr" && in.Parameters.Tool != "traceroute" {
		return fail(ErrInvalid)
	}
	var existing []byte
	if err = tx.QueryRow(ctx, `SELECT document FROM network_diagnostic_sessions WHERE owner_id=$1 AND request_id=$2`, scopeForConfig(ctx).OwnerID, in.RequestID).Scan(&existing); err == nil {
		var old core.DiagnosticSession
		if err = json.Unmarshal(existing, &old); err != nil {
			return fail(err)
		}
		x, _ := json.Marshal(old.Request)
		y, _ := json.Marshal(in)
		if string(x) != string(y) {
			return fail(fmt.Errorf("%w: 请求标识已用于其他参数", ErrConflict))
		}
		return old, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fail(err)
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM network_diagnostic_sessions WHERE state IN ('preparing','running','stopping','cleanup_pending') AND (node1=ANY($1) OR node2=ANY($1)))`, ids).Scan(&busy); err != nil {
		return fail(err)
	}
	if busy {
		return fail(fmt.Errorf("%w: 节点已有未完成诊断或待确认的资源清理", ErrConflict))
	}
	id, err := core.NewID("ndg")
	if err != nil {
		return fail(err)
	}
	now := time.Now().UTC()
	deadline := now.Add(10 * time.Minute)
	session := core.DiagnosticSession{ID: id, OwnerID: scopeForConfig(ctx).OwnerID, Request: in, State: "preparing", Results: map[string]core.DiagnosticResult{}, CreatedAt: now, Deadline: deadline}
	keyID := ""
	if key, ok := ctx.Value(apiKeyAuthContextKey{}).(apiKeyAuth); ok {
		keyID = key.ID
	}
	if _, err = tx.Exec(ctx, `INSERT INTO tasks(id,agent_id,owner_id,api_key_id,action,engine,status,attempt,created_at,started_at,output) VALUES($1,NULL,$2,$3,'network-diagnostic','','running',1,$4,$4,$5)`, id, session.OwnerID, keyID, now, "网络诊断 · "+in.Parameters.Tool); err != nil {
		return fail(err)
	}
	raw, _ := json.Marshal(session)
	if _, err = tx.Exec(ctx, `INSERT INTO network_diagnostic_sessions(id,owner_id,owner_admin,api_key_id,request_id,node1,node2,document,state,deadline) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, session.OwnerID, scopeForConfig(ctx).Admin, keyID, in.RequestID, in.Node1.AgentID, in.Node2.AgentID, raw, session.State, deadline); err != nil {
		return fail(err)
	}
	if err = recordAPIKeyWork(ctx, tx, "network-diagnostic", id); err != nil {
		return fail(err)
	}
	if in.Parameters.Tool == "iperf3" {
		for _, ep := range endpoints {
			if ep.InstanceID != "" {
				budget := in.Parameters.Bandwidth * int64(in.Parameters.Streams) * int64(in.Parameters.Duration) / 8 * 2
				if _, err = tx.Exec(ctx, `UPDATE agent_shares SET used_bytes=used_bytes+$2 WHERE id=$1`, ep.InstanceID, budget); err != nil {
					return fail(err)
				}
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fail(err)
	}
	return session, nil
}

func (s *Store) GetDiagnostic(ctx context.Context, id string) (core.DiagnosticSession, error) {
	var b []byte
	args := []any{id, scopeForConfig(ctx).OwnerID}
	err := s.pool.QueryRow(ctx, `SELECT document FROM network_diagnostic_sessions WHERE id=$1 AND owner_id=$2`, args...).Scan(&b)
	if err != nil {
		return core.DiagnosticSession{}, mapError(err)
	}
	var d core.DiagnosticSession
	if err = json.Unmarshal(b, &d); err != nil {
		return d, err
	}
	if time.Now().After(d.Deadline) && slices.Contains([]string{"preparing", "running", "stopping"}, d.State) {
		d.State = "cleanup_pending"
		_, err = s.pool.Exec(ctx, `UPDATE network_diagnostic_sessions SET state='cleanup_pending',document=jsonb_set(document,'{state}','"cleanup_pending"') WHERE id=$1 AND state IN ('preparing','running','stopping')`, id)
		if err != nil {
			return d, err
		}
	}
	if err = requireTaskPermission(ctx, s.pool, core.ActionStatus, false); err != nil {
		return d, err
	}
	for _, ep := range diagnosticEndpoints(d.Request) {
		if err = diagnosticEndpointAccess(ctx, s.pool, ep); err != nil {
			return core.DiagnosticSession{}, err
		}
	}
	return d, nil
}
func (s *Store) ListDiagnostics(ctx context.Context) ([]core.DiagnosticSession, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM network_diagnostic_sessions WHERE owner_id=$1 AND (created_at>now()-interval '7 days' OR state IN ('preparing','running','stopping','cleanup_pending')) ORDER BY created_at DESC LIMIT 30`, scopeForConfig(ctx).OwnerID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []core.DiagnosticSession{}
	for _, id := range ids {
		d, e := s.GetDiagnostic(ctx, id)
		if errors.Is(e, ErrNotFound) || errors.Is(e, ErrForbidden) {
			continue
		}
		if e != nil {
			return nil, e
		}
		result = append(result, d)
	}
	return result, nil
}
func (s *Store) StopDiagnostic(ctx context.Context, id string) error {
	if _, err := s.GetDiagnostic(ctx, id); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE network_diagnostic_sessions SET state='stopping',document=jsonb_set(document,'{state}','"stopping"') WHERE id=$1 AND owner_id=$2 AND state IN ('preparing','running')`, id, scopeForConfig(ctx).OwnerID)
	return err
}

func diagnosticCommands(d core.DiagnosticSession) []core.DiagnosticCommand {
	p := d.Request.Parameters
	a, b := d.Request.Node1, d.Request.Node2
	makeCommand := func(suffix, role string, source, target core.DiagnosticEndpoint) core.DiagnosticCommand {
		return core.DiagnosticCommand{ID: d.ID + suffix, SessionID: d.ID, Role: role, Source: source, Target: target, Parameters: p, Deadline: d.Deadline}
	}
	if p.Tool != "iperf3" {
		commands := []core.DiagnosticCommand{makeCommand("-forward", "probe", a, b)}
		if d.Request.Bidirectional {
			commands = append(commands, makeCommand("-reverse", "probe", b, a))
		}
		return commands
	}
	if d.Request.TargetMode == "ip" {
		return []core.DiagnosticCommand{makeCommand("-client", "client", a, b)}
	}
	commands := []core.DiagnosticCommand{makeCommand("-server", "server", b, a)}
	if r, ok := d.Results[d.ID+"-server"]; ok && r.State == "ready" {
		commands = append(commands, makeCommand("-client", "client", a, b))
	} else if _, ok := d.Results[d.ID+"-client"]; ok {
		commands = append(commands, makeCommand("-client", "client", a, b))
	}
	return commands
}
func diagnosticComplete(d *core.DiagnosticSession) {
	commands := diagnosticCommands(*d)
	all, failed, canceled := true, false, false
	for _, c := range commands {
		r, ok := d.Results[c.ID]
		if !ok || !r.Terminal() || !r.Cleaned {
			all = false
		}
		failed = failed || r.State == "failed"
		canceled = canceled || r.State == "canceled" && !(c.Role == "server" && d.Results[d.ID+"-client"].State == "succeeded")
	}
	if d.Request.Parameters.Tool == "iperf3" && d.Request.TargetMode != "ip" {
		_, client := d.Results[d.ID+"-client"]
		if !client {
			server := d.Results[d.ID+"-server"]
			if !server.Terminal() {
				all = false
			}
			failed = true
		}
	}
	if all {
		if d.State == "stopping" || canceled {
			d.State = "canceled"
		} else if failed {
			d.State = "failed"
		} else {
			d.State = "succeeded"
		}
	}
}

// Every reconciliation rechecks the creator and both allocations, even for
// work dispatched on the other node. Cleanup commands bypass revoked grants.
func (s *Store) AgentDiagnosticCommands(ctx context.Context, agentID string) ([]core.DiagnosticCommand, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT document,owner_admin,api_key_id FROM network_diagnostic_sessions WHERE (node1=$1 OR node2=$1) AND state IN ('preparing','running','stopping','cleanup_pending') ORDER BY id FOR UPDATE`, agentID)
	if err != nil {
		return nil, err
	}
	type pending struct {
		d     core.DiagnosticSession
		admin bool
		key   string
	}
	list := []pending{}
	for rows.Next() {
		var b []byte
		var p pending
		if err = rows.Scan(&b, &p.admin, &p.key); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(b, &p.d); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []core.DiagnosticCommand{}
	for _, p := range list {
		d := p.d
		currentAdmin := p.admin
		if d.OwnerID != "" && !strings.HasPrefix(d.OwnerID, "token_") {
			var currentRole string
			if e := tx.QueryRow(ctx, `SELECT role FROM panel_users WHERE id=$1`, d.OwnerID).Scan(&currentRole); errors.Is(e, pgx.ErrNoRows) {
				d.State = "stopping"
				currentAdmin = false
			} else if e != nil {
				return nil, e
			} else {
				currentAdmin = currentRole == "admin"
			}
		}
		scoped := WithConfigScope(ctx, d.OwnerID, currentAdmin)
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT ($1='' OR starts_with($1,'token_') OR EXISTS(SELECT 1 FROM panel_users WHERE id=$1)) AND NOT EXISTS(SELECT 1 FROM panel_users WHERE id=$1 AND (disabled OR (role<>'admin' AND NOT permissions @> '["tasks.execute","tasks.read","agents.read"]'::jsonb))) AND ($2='' OR EXISTS(SELECT 1 FROM admin_api_keys WHERE id=$2 AND NOT disabled AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now()) AND mode='read_write' AND permissions @> '["tasks.execute","tasks.read","agents.read"]'::jsonb))`, d.OwnerID, p.key).Scan(&valid); err != nil {
			return nil, err
		}
		if !valid {
			d.State = "stopping"
		}
		keyAllowed, e := apiKeyWorkAllowed(ctx, tx, "network-diagnostic", d.ID)
		if e != nil {
			return nil, e
		}
		if !keyAllowed {
			d.State = "stopping"
		}

		for _, ep := range diagnosticEndpoints(d.Request) {
			if d.Request.Parameters.Tool == "iperf3" && ep.InstanceID != "" {
				var resources bool
				if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_shares sh WHERE sh.id=$1 AND (sh.limit_bytes=0 OR sh.used_bytes<=sh.limit_bytes) AND ($3 OR sh.ports_unrestricted OR EXISTS(SELECT 1 FROM agent_share_ports sp WHERE sp.share_id=sh.id AND sp.port=$2)))`, ep.InstanceID, d.Request.Parameters.Port, ep.AgentID != d.Request.Node2.AgentID).Scan(&resources); e != nil {
					return nil, e
				}
				if !resources {
					d.State = "stopping"
				}
			}
			if e := diagnosticEndpointAccess(scoped, tx, ep); e != nil {
				if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) {
					d.State = "stopping"
				} else {
					return nil, e
				}
			}
		}
		if time.Now().After(d.Deadline) {
			d.State = "cleanup_pending"
		}
		commands := diagnosticCommands(d)
		// A completed client immediately releases its dedicated server.
		clientDone := d.Results[d.ID+"-client"].Terminal()
		serverResult := d.Results[d.ID+"-server"]
		serverFailed := serverResult.State == "failed" || serverResult.State == "canceled" || serverResult.Terminal() && time.Since(serverResult.At) > 10*time.Second
		for _, c := range commands {
			if c.Source.AgentID != agentID {
				continue
			}
			r := d.Results[c.ID]
			if r.Terminal() && r.Cleaned {
				continue
			}
			if d.State == "stopping" || d.State == "cleanup_pending" || c.Role == "server" && clientDone || c.Role == "client" && d.Request.TargetMode != "ip" && serverFailed {
				c.Role = "stop"
			}
			result = append(result, c)
		}
		if d.State == "preparing" {
			d.State = "running"
		}
		diagnosticComplete(&d)
		b, _ := json.Marshal(d)
		if err = syncDiagnosticTask(ctx, tx, d); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE network_diagnostic_sessions SET document=$2,state=$3 WHERE id=$1 AND document IS DISTINCT FROM $2`, d.ID, b, d.State); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *Store) CompleteDiagnosticCommand(ctx context.Context, agentID string, r core.DiagnosticResult) error {
	if len(r.ID) > 100 || len(r.Output) > 512<<10 || len(r.Stderr) > 8192 || len(r.Error) > 8192 || !slices.Contains([]string{"ready", "running", "succeeded", "failed", "canceled"}, r.State) {
		return ErrInvalid
	}
	parts := strings.Split(r.ID, "-")
	if len(parts) != 2 {
		return ErrInvalid
	}
	id := parts[0]
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var b []byte
	if err = tx.QueryRow(ctx, `SELECT document FROM network_diagnostic_sessions WHERE id=$1 FOR UPDATE`, id).Scan(&b); err != nil {
		return mapError(err)
	}
	var d core.DiagnosticSession
	if err = json.Unmarshal(b, &d); err != nil {
		return err
	}
	commands := diagnosticCommands(d)
	valid := false
	for _, c := range commands {
		if c.ID == r.ID && c.Source.AgentID == agentID {
			valid = true
		}
	}
	if !valid {
		return ErrForbidden
	}
	if old, ok := d.Results[r.ID]; ok {
		if old.Terminal() && old.Cleaned {
			return nil
		}
		if old.State == "ready" && r.State == "running" {
			return nil
		}
		incoming := r
		incoming.At = old.At
		oldJSON, _ := json.Marshal(old)
		incomingJSON, _ := json.Marshal(incoming)
		if string(oldJSON) == string(incomingJSON) {
			return nil
		}
	}
	r.Stderr = core.DiagnosticText(r.Stderr)
	r.Error = core.DiagnosticText(r.Error)
	r.At = time.Now().UTC()
	d.Results[r.ID] = r
	diagnosticComplete(&d)
	if err = syncDiagnosticTask(ctx, tx, d); err != nil {
		return err
	}
	b, _ = json.Marshal(d)
	if _, err = tx.Exec(ctx, `UPDATE network_diagnostic_sessions SET document=$2,state=$3 WHERE id=$1`, id, b, d.State); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func syncDiagnosticTask(ctx context.Context, tx pgx.Tx, d core.DiagnosticSession) error {
	if !slices.Contains([]string{"succeeded", "failed", "canceled"}, d.State) {
		return nil
	}
	// Keep regular task polling compact; full probe output is owned and read
	// through the diagnostic session endpoint, including historical tasks.
	summary := map[string]core.DiagnosticResult{}
	for id, result := range d.Results {
		result.Output = nil
		result.Stderr = ""
		summary[id] = result
	}
	b, _ := json.Marshal(summary)
	failures := []string{}
	for _, r := range d.Results {
		if r.Error != "" {
			failures = append(failures, r.Error)
		}
	}
	if d.State == "failed" && len(failures) == 0 {
		failures = append(failures, "测量未完成；请查看分方向结果")
	}
	detail := strings.Join(failures, "\n")
	_, err := tx.Exec(ctx, `UPDATE tasks SET status=$2,finished_at=now(),output=$3,error=$4 WHERE id=$1 AND status='running'`, d.ID, d.State, string(b), detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_diagnostics(task_id,attempt,output,error,source) VALUES($1,1,$2,$3,'network-diagnostic') ON CONFLICT DO NOTHING`, d.ID, string(b), detail)
	return err
}

func diagnosticTaskAccessClause(ctx context.Context, args *[]any) string {
	scope := scopeForConfig(ctx)
	if key, ok := ctx.Value(apiKeyAuthContextKey{}).(apiKeyAuth); ok && !core.HasPermission(key.Effective, core.PermissionTasksExecute) {
		return "false"
	}
	*args = append(*args, scope.OwnerID)
	owner := fmt.Sprintf("$%d", len(*args))
	clause := `EXISTS(SELECT 1 FROM network_diagnostic_sessions ds WHERE ds.id=tasks.id AND ds.owner_id=` + owner + ` AND NOT EXISTS(SELECT 1 FROM panel_users du WHERE du.id=` + owner + ` AND (du.disabled OR (du.role<>'admin' AND NOT du.permissions @> '["tasks.execute","tasks.read","agents.read"]'::jsonb)))`
	for i, name := range []string{"node1", "node2"} {
		col := "ds." + name
		condition := `EXISTS(SELECT 1 FROM agents diag_agent WHERE diag_agent.id=` + col + ` AND diag_agent.revoked_at IS NULL` + agentAccessClause(ctx, "diag_agent.id", args) + `)`
		instance := fmt.Sprintf("(ds.document #>> '{request,node%d,instance_id}')", i+1)
		own := `EXISTS(SELECT 1 FROM agents diag_owner WHERE diag_owner.id=` + col + ` AND diag_owner.owner_id=` + owner + `)`
		if scope.Admin || strings.HasPrefix(scope.OwnerID, "token_") {
			own = "true"
		}
		condition += ` AND ((` + instance + `='' AND ` + own + `) OR EXISTS(SELECT 1 FROM agent_shares sh JOIN network_diagnostic_grants g ON g.share_id=sh.id WHERE sh.id=` + instance + ` AND sh.agent_id=` + col + ` AND sh.user_id=` + owner + ` AND sh.enabled AND sh.status='accepted' AND cardinality(sh.engines)>0 AND g.enabled))`
		if i == 1 {
			condition = `((ds.document #>> '{request,target_mode}'='ip' AND ds.node2='') OR (` + condition + `))`
		}
		clause += ` AND (` + condition + `)`
	}
	return clause + `)`
}

func diagnosticEndpoints(r core.DiagnosticRequest) []core.DiagnosticEndpoint {
	if r.TargetMode == "ip" {
		return []core.DiagnosticEndpoint{r.Node1}
	}
	return []core.DiagnosticEndpoint{r.Node1, r.Node2}
}
