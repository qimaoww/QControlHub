package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) BatchPortTraffic(ctx context.Context, input core.TrafficBatchRequest) ([]core.TrafficBatchResult, error) {
	if len(input.RequestID) < 16 || len(input.RequestID) > 100 || strings.ContainsAny(input.RequestID, "\x00\r\n") || len(input.IDs) == 0 || len(input.IDs) > 4096 {
		return nil, fmt.Errorf("%w: invalid batch request", ErrInvalid)
	}
	switch input.Action {
	case "delete", "reset", "remove-quota":
		if input.Quota != nil {
			return nil, ErrInvalid
		}
	case "quota":
		if input.Quota == nil || *input.Quota == (core.TrafficQuotaPatch{}) {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range input.IDs {
		if !core.ValidPortTrafficPolicyID(id) || seen[id] {
			return nil, fmt.Errorf("%w: invalid or duplicate record ID", ErrInvalid)
		}
		seen[id] = true
	}
	raw, _ := json.Marshal(input)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	account := scopeForConfig(ctx).OwnerID
	// Claim the request before processing. Durable item receipts make lost HTTP
	// responses and concurrent duplicates safe, including across panel restarts.
	if _, err := s.pool.Exec(ctx, `INSERT INTO traffic_batch_requests(account_id,request_id,payload_hash) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, account, input.RequestID, hash); err != nil {
		return nil, err
	}
	var previous string
	if err := s.pool.QueryRow(ctx, `SELECT payload_hash FROM traffic_batch_requests WHERE account_id=$1 AND request_id=$2`, account, input.RequestID).Scan(&previous); err != nil {
		return nil, err
	}
	if previous != hash {
		return nil, fmt.Errorf("%w: batch request ID already used", ErrConflict)
	}
	results := make([]core.TrafficBatchResult, 0, len(input.IDs))
	for _, id := range input.IDs {
		if ctx.Err() != nil {
			results = append(results, core.TrafficBatchResult{ID: id, Status: "not_executed", Message: "请求已中断"})
			continue
		}
		result, err := s.batchPortTrafficItem(ctx, account, input, id)
		if err != nil {
			result = core.TrafficBatchResult{ID: id, Status: "failed", Message: "操作失败，请重试"}
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Store) batchPortTrafficItem(ctx context.Context, account string, input core.TrafficBatchRequest, id string) (core.TrafficBatchResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.TrafficBatchResult{}, err
	}
	defer tx.Rollback(ctx)
	// Each item releases locks before the next. Concurrent copies may interleave
	// but a receipt and its mutation always commit atomically.
	if _, err = tx.Exec(ctx, `SELECT request_id FROM traffic_batch_requests WHERE account_id=$1 AND request_id=$2 FOR UPDATE`, account, input.RequestID); err != nil {
		return core.TrafficBatchResult{}, err
	}
	var stored []byte
	err = tx.QueryRow(ctx, `SELECT result FROM traffic_batch_results WHERE account_id=$1 AND request_id=$2 AND policy_id=$3`, account, input.RequestID, id).Scan(&stored)
	if err == nil {
		var result core.TrafficBatchResult
		var receipt struct {
			core.TrafficBatchResult
			AgentID string `json:"agent_id"`
		}
		err = json.Unmarshal(stored, &receipt)
		result = receipt.TrafficBatchResult
		result.AgentID = receipt.AgentID
		return result, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return core.TrafficBatchResult{}, err
	}
	result := core.TrafficBatchResult{ID: id, Status: "succeeded"}
	item, err := tx.Begin(ctx)
	if err != nil {
		return result, err
	}
	result.AgentID, err = lockTrafficMutationPolicy(ctx, item, id)
	if err == nil {
		err = s.requireCurrentTrafficPolicyTx(ctx, item, id, input.Action)
	}
	if err == nil {
		switch input.Action {
		case "delete":
			_, err = deletePortTrafficMonitoringTx(ctx, item, id)
		case "reset":
			_, err = resetPortTrafficPolicyTx(ctx, item, id)
		case "remove-quota":
			_, err = deletePortTrafficPolicyTx(ctx, item, id)
		case "quota":
			err = s.patchTrafficQuotaTx(ctx, item, id, *input.Quota)
		}
	}
	if err != nil {
		_ = item.Rollback(ctx)
		result.AgentID = ""
		result.Status = "not_executed"
		switch {
		case errors.Is(err, ErrNotFound), errors.Is(err, ErrForbidden):
			result.Message = "记录不存在或无权操作"
		case errors.Is(err, ErrConflict):
			result.Message = "记录已变化或受共享计费限制，请刷新"
		case errors.Is(err, ErrInvalid):
			result.Message = "配额字段无效，请检查后重试"
		default:
			result.Status = "failed"
			result.Message = "操作失败，请重试"
		}
	} else if err = item.Commit(ctx); err != nil {
		return result, err
	}
	raw, _ := json.Marshal(struct {
		core.TrafficBatchResult
		AgentID string `json:"agent_id"`
	}{result, result.AgentID})
	if _, err = tx.Exec(ctx, `INSERT INTO traffic_batch_results(account_id,request_id,policy_id,result) VALUES($1,$2,$3,$4)`, account, input.RequestID, id, raw); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (s *Store) requireCurrentTrafficPolicyTx(ctx context.Context, tx pgx.Tx, id, action string) error {
	p, err := scanTrafficPolicy(tx.QueryRow(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies WHERE id=$1 AND monitoring_enabled`, id))
	if err != nil {
		return mapError(err)
	}
	if action == "remove-quota" && !p.QuotaEnabled {
		return fmt.Errorf("%w: record has no quota", ErrConflict)
	}
	configs, err := s.trafficConfigurations(ctx, tx, false)
	if err != nil {
		return err
	}
	for _, c := range configs {
		if c.AppliedVersion > 0 && c.AppliedContent != "" {
			for _, ep := range c.endpoints(c.AppliedContent, c.AppliedVersion) {
				if trafficPolicyMatchesEndpoint(p, ep) {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("%w: record no longer belongs to a confirmed deployment", ErrConflict)
}

func (s *Store) patchTrafficQuotaTx(ctx context.Context, tx pgx.Tx, id string, patch core.TrafficQuotaPatch) error {
	p, err := scanTrafficPolicy(tx.QueryRow(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies WHERE id=$1`, id))
	if err != nil {
		return mapError(err)
	}
	limit := p.LimitBytes
	if !p.QuotaEnabled {
		limit = 0
	}
	request := core.PortTrafficPolicyRequest{AgentID: p.AgentID, Name: p.Name, Engine: p.Engine, Port: p.Port, Protocol: p.Protocol, Cycle: p.Cycle, CycleAnchor: p.CycleAnchor, LimitBytes: limit, AutoBlock: &p.AutoBlock}
	if patch.Cycle != nil {
		request.Cycle = *patch.Cycle
	}
	if patch.CycleAnchor != nil {
		request.CycleAnchor = *patch.CycleAnchor
	}
	if patch.LimitBytes != nil {
		request.LimitBytes = *patch.LimitBytes
	}
	if patch.AutoBlock != nil {
		request.AutoBlock = patch.AutoBlock
	}
	request, err = core.NormalizePortTrafficPolicyRequest(request, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	_, err = updatePortTrafficPolicyRow(ctx, tx, id, request)
	return err
}
