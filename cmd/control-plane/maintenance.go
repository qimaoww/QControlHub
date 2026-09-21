//go:build linux

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func janitor(ctx context.Context, dataStore *store.Store) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	pruneCounter := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			operationContext, cancel := context.WithTimeout(ctx, 15*time.Second)
			pruneCounter++
			if err := dataStore.MaintainAccountData(operationContext, time.Now().UTC(), pruneCounter%60 == 0); err != nil {
				slog.Error("maintain account data", "error", err)
			}
			if err := dataStore.CleanupNonces(operationContext); err != nil {
				slog.Error("clean signed-request nonces", "error", err)
			}
			if _, err := dataStore.CleanupExpiredEnrollmentTokens(operationContext); err != nil {
				slog.Error("clean expired enrollment credentials", "error", err)
			}
			if _, err := dataStore.RecordAgentMetricSamples(operationContext, time.Now().UTC()); err != nil {
				slog.Warn("record agent metric samples", "error", err)
			}
			// Inserting a log row without a matching partition is an error, so
			// the daily partitions are kept a few days ahead on every tick. The
			// statement is idempotent and normally creates nothing.
			if err := dataStore.EnsureCoreLogPartitions(operationContext, time.Now().UTC()); err != nil {
				slog.Error("ensure core log partitions", "error", err)
			}
			if err := dataStore.QueueDueIPQualityChecks(operationContext, time.Now().UTC()); err != nil {
				slog.Error("queue daily IP quality checks", "error", err)
			}
			cancel()
		}
	}
}

// Run independently of other maintenance so large deletion jobs cannot delay
// heartbeats, scheduled checks or retention. Drain persisted jobs on startup.
func cleanDeletedAgents(ctx context.Context, dataStore *store.Store) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err := dataStore.CleanupDeletedAgents(ctx); err != nil && ctx.Err() == nil {
			slog.Error("clean deleted agents", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Existing retained logs are indexed independently of requests and maintenance.
func backfillClientConnectionLogs(ctx context.Context, dataStore *store.Store) {
	for ctx.Err() == nil {
		operationContext, cancel := context.WithTimeout(ctx, 15*time.Second)
		done, err := dataStore.BackfillClientConnectionLogs(operationContext)
		cancel()
		if err == nil {
			if done {
				return
			}
			continue
		}
		if ctx.Err() != nil {
			return
		}
		slog.Error("backfill client IPs from panel logs", "error", err)
		timer := time.NewTimer(15 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
