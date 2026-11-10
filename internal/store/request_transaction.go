package store

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// requestPool preserves existing store transaction boundaries as savepoints
// inside an API Key mutation. Authorization is rechecked before the outer
// commit, so rejected or concurrently revoked writes cannot partially persist.
type requestTransactionKey struct{}
type requestPool struct{ *pgxpool.Pool }
type storePool interface {
	Begin(context.Context) (pgx.Tx, error)
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	SendBatch(context.Context, *pgx.Batch) pgx.BatchResults
	Acquire(context.Context) (*pgxpool.Conn, error)
	Close()
	Ping(context.Context) error
	Config() *pgxpool.Config
	Stat() *pgxpool.Stat
}

func (p *requestPool) Begin(ctx context.Context) (pgx.Tx, error) {
	if tx, ok := ctx.Value(requestTransactionKey{}).(pgx.Tx); ok {
		return tx.Begin(ctx)
	}
	return p.Pool.Begin(ctx)
}
func (p *requestPool) BeginTx(ctx context.Context, o pgx.TxOptions) (pgx.Tx, error) {
	if tx, ok := ctx.Value(requestTransactionKey{}).(pgx.Tx); ok {
		return tx.Begin(ctx)
	}
	return p.Pool.BeginTx(ctx, o)
}
func (p *requestPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if tx, ok := ctx.Value(requestTransactionKey{}).(pgx.Tx); ok {
		return tx.Exec(ctx, sql, args...)
	}
	return p.Pool.Exec(ctx, sql, args...)
}
func (p *requestPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if tx, ok := ctx.Value(requestTransactionKey{}).(pgx.Tx); ok {
		return tx.Query(ctx, sql, args...)
	}
	return p.Pool.Query(ctx, sql, args...)
}
func (p *requestPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if tx, ok := ctx.Value(requestTransactionKey{}).(pgx.Tx); ok {
		return tx.QueryRow(ctx, sql, args...)
	}
	return p.Pool.QueryRow(ctx, sql, args...)
}
func (p *requestPool) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	if tx, ok := ctx.Value(requestTransactionKey{}).(pgx.Tx); ok {
		return tx.SendBatch(ctx, b)
	}
	return p.Pool.SendBatch(ctx, b)
}

// WithoutRequestTransaction is used for post-commit audit and last-use writes.
func WithoutRequestTransaction(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestTransactionKey{}, nil)
}

func HasRequestTransaction(ctx context.Context) bool {
	_, ok := ctx.Value(requestTransactionKey{}).(pgx.Tx)
	return ok
}

type requestCommitCallbacksKey struct{}

// AfterRequestCommit queues notifications that must observe committed state.
// It returns false for ordinary requests, which keep their existing behavior.
func AfterRequestCommit(ctx context.Context, callback func()) bool {
	if !HasRequestTransaction(ctx) {
		return false
	}
	if callbacks, ok := ctx.Value(requestCommitCallbacksKey{}).(*[]func()); ok {
		*callbacks = append(*callbacks, callback)
		return true
	}
	return false
}
func (s *Store) signalTaskReadyAfterCommit(ctx context.Context, agentID string) {
	if AfterRequestCommit(ctx, func() { s.signalTaskReady(agentID) }) {
		return
	}
	s.signalTaskReady(agentID)
}
