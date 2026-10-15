package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func (s *Store) managedTLSFrontendsTx(ctx context.Context, tx pgx.Tx, id string, version int, engine core.Engine, content string) ([]core.TLSFrontend, error) {
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
	ports := []int{}
	for _, f := range task.TLSFrontends {
		ports = append(ports, f.PublicPort)
		if f.LocalOnly {
			ports = append(ports, f.BackendPort)
		}
	}
	if err := s.checkSharedPortsAvailableTx(ctx, tx, task.SharedTrafficID, owner, task.AgentID, ports); err != nil {
		var conflict *PortConflictError
		if errors.As(err, &conflict) {
			field := fieldForPort(conflict.Port)
			return &PortConflictError{Port: conflict.Port, Field: field, Cause: conflict.Cause}
		}
		return err
	}
	if task.SharedTrafficID == "" {
		return nil
	}
	var unrestricted bool
	if err := tx.QueryRow(ctx, `SELECT ports_unrestricted FROM agent_shares WHERE id=$1`, task.SharedTrafficID).Scan(&unrestricted); err != nil {
		return err
	}
	for _, port := range ports {
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
