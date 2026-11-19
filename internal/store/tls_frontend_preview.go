package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// TLSFrontendMember is an allowlisted projection. Execution snapshots and
// encrypted client metadata must never be placed in a preview response.
type TLSFrontendMember struct {
	ConfigID           string      `json:"config_id"`
	InstanceID         string      `json:"instance_id"`
	Engine             core.Engine `json:"engine"`
	Tag                string      `json:"tag"`
	Version            int         `json:"version"`
	DeployedVersion    int         `json:"deployed_version"`
	Domain             string      `json:"domain"`
	Path               string      `json:"path"`
	Transport          string      `json:"transport"`
	BackendPort        int         `json:"backend_port"`
	From               string      `json:"from"`
	To                 string      `json:"to"`
	CertificateID      string      `json:"certificate_id,omitempty"`
	GrantGeneration    int64       `json:"grant_generation,omitempty"`
	CertificateVersion int         `json:"certificate_version,omitempty"`
}

type TLSFrontendPreview struct {
	AgentID                        string              `json:"agent_id"`
	PublicPort                     int                 `json:"public_port"`
	TargetMode                     string              `json:"target_mode"`
	PlanID                         string              `json:"plan_id"`
	Members                        []TLSFrontendMember `json:"members"`
	Status                         string              `json:"status"`
	Blockers                       []string            `json:"blockers"`
	RuntimeChecks                  []string            `json:"runtime_checks"`
	ConnectionInterruptionPossible bool                `json:"connection_interruption_possible"`
}

type TLSFrontendSelection struct {
	ExpectedVersion    int     `json:"expected_version"`
	ExpectedInstanceID *string `json:"expected_instance_id"`
	TargetMode         string  `json:"target_mode"`
}

type frontendWorkspace struct {
	id, owner, instance, content string
	engine                       core.Engine
	version, savedVersion        int
	applied, uncertain, visible  bool
	frontends                    []core.TLSFrontend
}

func (s *Store) PreviewTLSFrontend(ctx context.Context, agent core.Agent, engine core.Engine, tag string, input TLSFrontendSelection) (TLSFrontendPreview, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return TLSFrontendPreview{}, err
	}
	defer tx.Rollback(ctx)
	return s.previewTLSFrontendTx(ctx, tx, agent, engine, tag, input)
}

func (s *Store) previewTLSFrontendTx(ctx context.Context, tx pgx.Tx, agent core.Agent, engine core.Engine, tag string, input TLSFrontendSelection) (TLSFrontendPreview, error) {
	p := TLSFrontendPreview{AgentID: agent.ID, TargetMode: input.TargetMode, Members: []TLSFrontendMember{}, Blockers: []string{}, RuntimeChecks: []string{"agent_listener_ownership_ipv4_ipv6", "proxy_config_and_service", "original_client_round_trip", "restore_original_paths"}}
	if input.ExpectedVersion < 1 || input.ExpectedInstanceID == nil || tag == "" {
		return p, ErrInvalid
	}
	if input.TargetMode != "nginx" && input.TargetMode != "caddy" {
		return p, &PortConflictError{Reason: "unsupported_mode", Field: "target_mode", Cause: ErrInvalid}
	}
	if err := requireAgentEngineAccess(ctx, tx, agent.ID, engine); err != nil {
		return p, err
	}
	owner := scopeForConfig(ctx).OwnerID
	instance := ""
	shared, err := sharedInstanceRecipient(ctx, tx, agent.ID)
	if err != nil {
		return p, err
	}
	if shared {
		if err = tx.QueryRow(ctx, `SELECT id FROM agent_shares WHERE agent_id=$1 AND user_id=$2 AND enabled AND status='accepted' AND $3=ANY(engines)`, agent.ID, owner, engine).Scan(&instance); err != nil {
			return p, mapError(err)
		}
	}
	if instance != *input.ExpectedInstanceID {
		return p, &PortConflictError{Reason: "instance_changed", Field: "expected_instance_id", Cause: ErrConflict}
	}
	items, err := s.frontendWorkspaces(ctx, tx, agent.ID, owner, instance)
	if err != nil {
		return p, err
	}
	block := func(code string) {
		if !slices.Contains(p.Blockers, code) {
			p.Blockers = append(p.Blockers, code)
		}
	}
	selected := false
	for i := range items {
		w := &items[i]
		w.visible = w.owner == owner && w.instance == instance && slices.Contains(agent.Capabilities, w.engine)
		if w.applied && w.uncertain {
			block("port_group_incomplete")
		}
		if w.content == "" {
			if w.uncertain {
				block("port_group_incomplete")
			}
			continue
		}
		w.content, err = s.decryptContent(w.content)
		if err != nil {
			return p, err
		}
		w.frontends, err = s.managedTLSFrontendsTx(ctx, tx, w.id, w.version, w.engine, w.content)
		if err != nil {
			block("port_group_incomplete")
			continue
		}
		if w.applied || !w.visible || w.engine != engine {
			continue
		}
		if w.version != input.ExpectedVersion {
			return p, &PortConflictError{Reason: "stale_version", Field: "expected_version", Cause: ErrConflict}
		}
		for _, f := range w.frontends {
			if f.Tag != tag || f.LocalOnly {
				continue
			}
			if selected {
				return p, ErrConflict
			}
			selected = true
			p.PublicPort = f.PublicPort
		}
	}
	if !selected {
		return p, ErrNotFound
	}
	var candidates []core.TLSFrontend
	for _, w := range items {
		if w.applied {
			for _, port := range serverconfig.DiscoverTrafficPorts(w.engine, w.content) {
				if port.Port == p.PublicPort {
					block("port_group_incomplete")
				}
			}
		}
		for _, f := range w.frontends {
			if f.PublicPort != p.PublicPort {
				continue
			}
			if !w.visible {
				block("port_group_unauthorized")
				continue
			}
			if w.uncertain || f.LocalOnly {
				block("port_group_incomplete")
			}
			if w.applied {
				if w.savedVersion != w.version {
					block("deployment_drift")
				}
				continue
			}
			m := TLSFrontendMember{ConfigID: w.id, Engine: w.engine, InstanceID: w.instance, Tag: f.Tag, Version: w.version, Domain: f.Domain, Path: f.Path, BackendPort: f.BackendPort, Transport: f.Transport, From: f.Mode, To: input.TargetMode}
			for _, other := range items {
				if other.applied && other.id == w.id && other.instance == w.instance {
					m.DeployedVersion = other.version
				}
			}
			if m.DeployedVersion != m.Version {
				block("deployment_drift")
			}
			if f.Mode != input.TargetMode && !f.UsesPanelCertificate() {
				block("certificate_change_required")
			}
			if f.UsesPanelCertificate() {
				id := strings.SplitN(strings.TrimPrefix(f.CertificatePath, core.PanelCertificatePrefix), "/", 2)[0]
				m.CertificateID = id
				if err = certificateUseTx(ctx, tx, id, owner); err != nil {
					if !errors.Is(err, ErrForbidden) {
						return p, err
					}
					block("certificate_unauthorized")
				} else {
					var bound int
					if err = tx.QueryRow(ctx, `SELECT c.version,COALESCE(b.version,0) FROM panel_certificates c LEFT JOIN certificate_bindings b ON b.certificate_id=c.id AND b.config_id=$2 AND b.enabled AND b.config_version=$3 WHERE c.id=$1`, id, w.id, w.version).Scan(&m.CertificateVersion, &bound); err != nil {
						return p, err
					}
					if bound != m.CertificateVersion {
						block("certificate_binding_changed")
					}
				}
			}
			p.ConnectionInterruptionPossible = p.ConnectionInterruptionPossible || f.Mode != input.TargetMode
			f.Mode = input.TargetMode
			f.StaticPageOwner = w.id
			if shared {
				generation, allowed, e := proxyIngressGrantTx(ctx, tx, agent.ID, owner, f)
				if e != nil && !errors.Is(e, ErrConflict) {
					return p, e
				}
				m.GrantGeneration = generation
				if !allowed {
					block("ingress_unauthorized")
				}
			}
			candidates = append(candidates, f)
			p.Members = append(p.Members, m)
		}
	}
	if err := core.ValidateTLSFrontendRoutes(candidates); err != nil {
		var conflict *core.TLSFrontendConflict
		if errors.As(err, &conflict) {
			block(conflict.Reason)
		} else {
			block("unsupported_target_configuration")
		}
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE agent_id=$1 AND status IN ('pending','running') AND action NOT IN ('status','read-config','read-managed-config'))`, agent.ID).Scan(&pending); err != nil {
		return p, err
	}
	if pending {
		block("pending_tasks")
	}
	if !slices.Contains(agent.Features, core.AgentFeatureTLSMigration) {
		block("agent_upgrade_required")
	}
	if agent.Status != "online" {
		block("agent_offline")
	}
	proxy := agent.ManagedProxies[input.TargetMode]
	if proxy.State == "missing" {
		block("dependency_missing")
	}
	sort.Slice(p.Members, func(i, j int) bool {
		a, b := p.Members[i], p.Members[j]
		if a.ConfigID != b.ConfigID {
			return a.ConfigID < b.ConfigID
		}
		return a.Tag < b.Tag
	})
	sort.Strings(p.Blockers)
	p.Status = "runtime_evidence_required"
	if len(p.Blockers) > 0 {
		p.Status = "blocked"
	}
	data, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	sum := sha256.Sum256(data)
	p.PlanID = hex.EncodeToString(sum[:])
	return p, nil
}

func (s *Store) frontendWorkspaces(ctx context.Context, tx pgx.Tx, agentID, owner, instance string) ([]frontendWorkspace, error) {
	// Include encrypted saved drafts owned by the caller and every retained
	// runtime revision on this node. A newer draft cannot hide an old listener.
	rows, err := tx.Query(ctx, `
	WITH states AS (
	SELECT engine,config_id,config_version,owner_id,'' AS instance,uncertain OR config_uncertain OR NOT running AS uncertain
	FROM agent_engine_ownership WHERE agent_id=$1
	UNION ALL SELECT engine,config_id,config_version,owner_id,share_id,uncertain OR config_uncertain OR NOT running
	FROM agent_shared_instance_ownership WHERE agent_id=$1
	)
	SELECT c.id,c.owner_id,c.engine,$3,c.version,c.version,c.content,false,false
	FROM configs c WHERE c.agent_id=$1 AND c.owner_id=$2 AND c.deleted_at IS NULL
	UNION ALL SELECT st.config_id,st.owner_id,st.engine,st.instance,st.config_version,COALESCE(c.version,0),
	COALESCE(r.content,CASE WHEN c.version=st.config_version THEN c.content END,''),true,st.uncertain OR c.deleted_at IS NOT NULL
	FROM states st LEFT JOIN configs c ON c.id=st.config_id LEFT JOIN config_revisions r ON r.config_id=st.config_id AND r.version=st.config_version`, agentID, owner, instance)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (frontendWorkspace, error) {
		var w frontendWorkspace
		err := row.Scan(&w.id, &w.owner, &w.engine, &w.instance, &w.version, &w.savedVersion, &w.content, &w.applied, &w.uncertain)
		return w, err
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

// Port state describes the last attributable observation, without exposing
// another user's configuration, port list, hostnames, or instance identifiers.
type TLSPortSummary struct {
	AgentID       string `json:"agent_id"`
	PublicPort    int    `json:"public_port"`
	VisibleRoutes int    `json:"visible_routes"`
	State         string `json:"state"`
}

func (s *Store) TLSPortOccupancy(ctx context.Context, agent core.Agent, port int) (TLSPortSummary, error) {
	p := TLSPortSummary{AgentID: agent.ID, PublicPort: port, State: "unconfirmed"}
	if port < 1 || port > 65535 {
		return p, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return p, err
	}
	defer tx.Rollback(ctx)
	owner := scopeForConfig(ctx).OwnerID
	items, err := s.frontendWorkspaces(ctx, tx, agent.ID, owner, "")
	if err != nil {
		return p, err
	}
	incomplete := false
	for _, w := range items {
		if !w.applied {
			continue
		}
		if w.uncertain || w.content == "" {
			incomplete = true
			continue
		}
		content, err := s.decryptContent(w.content)
		if err != nil {
			return p, err
		}
		routes, err := s.managedTLSFrontendsTx(ctx, tx, w.id, w.version, w.engine, content)
		if err != nil {
			incomplete = true
			continue
		}
		for _, f := range routes {
			if f.PublicPort != port {
				continue
			}
			if w.owner != owner || !slices.Contains(agent.Capabilities, w.engine) || (w.instance != "" && !slices.Contains(agent.SharedEngines, w.engine)) {
				incomplete = true
				continue
			}
			p.VisibleRoutes++
		}
	}
	seen, err := observedHostPortsTx(ctx, tx, agent.ID, "")
	if err != nil {
		return p, err
	}
	switch {
	case incomplete:
		p.State = "partial"
	case p.VisibleRoutes > 0:
		p.State = "visible_in_use"
	case agent.Status == "online" && seen != nil && !seen[port]:
		p.State = "observed_free"
	case seen[port]:
		p.State = "partial"
	}
	return p, nil
}
