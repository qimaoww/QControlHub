package store

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Preserve the established defaults, but honor pgx pool settings supplied in
// either URI or keyword DSNs. Previously Open silently overwrote every knob.
func databasePoolConfig(databaseURL string) (*pgxpool.Config, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	// Cache prepared statements per connection.
	//
	// pgx defaults to the unnamed statement, so PostgreSQL parses and plans
	// again on every execution. On a live instance the traffic accounting
	// statement spent 8.4 ms planning against 1.7 ms executing, and it runs
	// about 26 times per second, which made planning rather than execution the
	// database's largest CPU cost. Every statement here has a fixed shape with
	// typed arguments, so a server-side plan is reusable as-is.
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	// pgxpool removes its settings from RuntimeParams. Parse with pgx as well
	// to distinguish an explicit value (including zero) from a driver default.
	raw, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	defaults := map[string]func(){
		"pool_min_conns":           func() { config.MinConns = min(2, config.MaxConns) },
		"pool_max_conn_lifetime":   func() { config.MaxConnLifetime = 30 * time.Minute },
		"pool_max_conn_idle_time":  func() { config.MaxConnIdleTime = 5 * time.Minute },
		"pool_health_check_period": func() { config.HealthCheckPeriod = 30 * time.Second },
	}
	// MaxConns must be resolved before clamping the default minimum.
	if _, configured := raw.RuntimeParams["pool_max_conns"]; !configured {
		config.MaxConns = 20
	}
	for key, apply := range defaults {
		if _, configured := raw.RuntimeParams[key]; !configured {
			apply()
		}
	}
	if config.MinConns < 0 || config.MinConns > config.MaxConns || config.MinIdleConns < 0 || config.MinIdleConns > config.MaxConns {
		return nil, fmt.Errorf("PostgreSQL pool minimum connections must be between zero and pool_max_conns")
	}
	if config.MaxConnLifetime <= 0 || config.MaxConnIdleTime <= 0 || config.HealthCheckPeriod <= 0 {
		return nil, fmt.Errorf("PostgreSQL pool lifetime, idle timeout, and health check period must be positive")
	}
	return config, nil
}
