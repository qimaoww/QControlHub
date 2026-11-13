package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func (s *Store) configClientMetadataTx(ctx context.Context, tx pgx.Tx, id string, version int) (map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT profile_tag,content FROM config_client_metadata WHERE config_id=$1 AND config_version=$2`, id, version)
	if err != nil {
		return nil, err
	}
	metadata := map[string]string{}
	for rows.Next() {
		var tag, value string
		if err = rows.Scan(&tag, &value); err != nil {
			rows.Close()
			return nil, err
		}
		value, err = s.decryptContent(value)
		if err != nil {
			rows.Close()
			return nil, err
		}
		metadata[tag] = value
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return metadata, nil
}

func (s *Store) managedTLSFrontendsTx(ctx context.Context, tx pgx.Tx, id string, version int, engine core.Engine, content string) ([]core.TLSFrontend, error) {
	metadata, err := s.configClientMetadataTx(ctx, tx, id, version)
	if err != nil {
		return nil, err
	}
	frontends, err := serverconfig.ManagedTLSFrontends(engine, content, metadata)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return frontends, nil
}

// Reserve frontend ports separately; traffic is attributed to the core's
// existing independent egress, so reverse proxy traffic is not counted twice.
func (s *Store) prepareTLSFrontendPortsTx(ctx context.Context, tx pgx.Tx, task *core.Task, owner string) error {
	if len(task.TLSFrontends) == 0 {
		return nil
	}
	fieldForPort := func(port int) string {
		for _, f := range task.TLSFrontends {
			if f.LocalOnly && port == f.PublicPort {
				return "xhttp_tls_port"
			}
			if f.LocalOnly && port == f.BackendPort {
				return "xhttp_backend_port"
			}
		}
		return "tls_public_port"
	}
	granted := map[int]bool{}
	for i := range task.TLSFrontends {
		f := &task.TLSFrontends[i]
		generation, allowed, err := proxyIngressGrantTx(ctx, tx, task.AgentID, owner, *f)
		if err != nil {
			return err
		}
		if !f.LocalOnly && task.SharedTrafficID != "" && !allowed {
			return &PortConflictError{Port: f.PublicPort, Field: "tls_public_port", Reason: "ingress_unauthorized", Cause: fmt.Errorf("%w: 未获授权使用 %s 反代", ErrConflict, f.Mode)}
		}
		if f.SharedIngressGeneration > 0 && (!allowed || generation != f.SharedIngressGeneration) {
			return &PortConflictError{Port: f.PublicPort, Field: "tls_public_port", Reason: "ingress_unauthorized", Cause: fmt.Errorf("%w: 托管入口授权已撤销或变更，请重新部署", ErrConflict)}
		}
		if generation > 0 && allowed {
			f.SharedIngressGeneration = generation
			granted[f.PublicPort] = true
		}
	}
	ports := []int{}
	for _, f := range task.TLSFrontends {
		ports = append(ports, f.PublicPort)
		if f.LocalOnly {
			ports = append(ports, f.BackendPort)
		}
	}
	if err := s.checkSharedPortsAvailableTx(ctx, tx, task.SharedTrafficID, owner, task.AgentID, ports, task); err != nil {
		var conflict *PortConflictError
		if errors.As(err, &conflict) {
			field := fieldForPort(conflict.Port)
			return &PortConflictError{Port: conflict.Port, Field: field, Cause: conflict.Cause, RecoveryEngine: conflict.RecoveryEngine, Reason: conflict.Reason}
		}
		return err
	}
	if task.SharedTrafficID == "" {
		return nil
	}
	for _, f := range task.TLSFrontends {
		var reservedOwner string
		err := tx.QueryRow(ctx, `SELECT s.user_id FROM agent_share_ports p JOIN agent_shares s ON s.id=p.share_id WHERE p.agent_id=$1 AND p.port=$2 AND p.share_id<>$3`, task.AgentID, f.PublicPort, task.SharedTrafficID).Scan(&reservedOwner)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if reservedOwner != "" {
			_, otherAllowed, e := proxyIngressGrantTx(ctx, tx, task.AgentID, reservedOwner, f)
			if e != nil {
				return e
			}
			_, allowed, e := proxyIngressGrantTx(ctx, tx, task.AgentID, owner, f)
			if e != nil {
				return e
			}
			if !allowed || !otherAllowed {
				return &PortConflictError{Port: f.PublicPort, Field: fieldForPort(f.PublicPort), Reason: "ingress_unauthorized", Cause: fmt.Errorf("%w: 该入口已有独占端口预留，请先取得共享入口授权", ErrConflict)}
			}
		}
	}
	var unrestricted bool
	if err := tx.QueryRow(ctx, `SELECT ports_unrestricted FROM agent_shares WHERE id=$1`, task.SharedTrafficID).Scan(&unrestricted); err != nil {
		return err
	}
	for _, port := range ports {
		if granted[port] {
			continue
		}
		var allocated bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_share_ports WHERE share_id=$1 AND port=$2)`, task.SharedTrafficID, port).Scan(&allocated); err != nil {
			return err
		}
		if !unrestricted && !allocated {
			return &PortConflictError{Port: port, Field: fieldForPort(port), Cause: fmt.Errorf("%w: 入口或内部端口 %d 未分配给此用户", ErrConflict, port)}
		}
		if unrestricted && task.Action == core.ActionDeploy {
			if _, err := tx.Exec(ctx, `INSERT INTO agent_share_ports(agent_id,port,share_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, task.AgentID, port, task.SharedTrafficID); err != nil {
				return mapError(err)
			}
		}
	}
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM agent_share_ports WHERE agent_id=$1`, task.AgentID).Scan(&total); err != nil {
		return err
	}
	if total > 256 {
		return fmt.Errorf("%w: an Agent supports at most 256 reserved shared ports", ErrConflict)
	}
	return nil
}
