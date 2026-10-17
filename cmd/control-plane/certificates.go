package main

import (
	"context"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"log/slog"
	"time"
)

func certificateMaintenance(ctx context.Context, db *store.Store) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := db.RunCertificateMaintenance(ctx); err != nil && ctx.Err() == nil {
			slog.Error("certificate maintenance failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
