package store

import (
	"testing"
	"time"
)

func TestDatabasePoolConfig(t *testing.T) {
	for _, test := range []struct {
		name, dsn string
		max, min  int32
	}{
		{"defaults", "postgresql://localhost/test", 20, 2},
		{"URI overrides", "postgresql://localhost/test?pool_max_conns=7&pool_min_conns=3", 7, 3},
		{"keyword overrides", "host=localhost dbname=test pool_max_conns=8 pool_min_conns=0", 8, 0},
		{"small pool", "postgresql://localhost/test?pool_max_conns=1", 1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := databasePoolConfig(test.dsn)
			if err != nil {
				t.Fatal(err)
			}
			if config.MaxConns != test.max || config.MinConns != test.min {
				t.Fatalf("pool = %d/%d, want %d/%d", config.MaxConns, config.MinConns, test.max, test.min)
			}
			if config.MaxConnLifetime != 30*time.Minute || config.MaxConnIdleTime != 5*time.Minute || config.HealthCheckPeriod != 30*time.Second {
				t.Fatal("established timeout defaults changed")
			}
		})
	}
	config, err := databasePoolConfig("postgresql://localhost/test?pool_max_conn_lifetime=10m&pool_max_conn_idle_time=2m&pool_health_check_period=10s&pool_min_idle_conns=3")
	if err != nil || config.MaxConnLifetime != 10*time.Minute || config.MaxConnIdleTime != 2*time.Minute || config.HealthCheckPeriod != 10*time.Second || config.MinIdleConns != 3 {
		t.Fatalf("explicit timeouts/idle reserve not honored: %v", err)
	}
	for _, query := range []string{"pool_max_conns=0", "pool_min_conns=-1", "pool_max_conns=1&pool_min_conns=2", "pool_min_idle_conns=21", "pool_health_check_period=0s", "pool_max_conn_idle_time=-1s"} {
		if _, err := databasePoolConfig("postgresql://localhost/test?" + query); err == nil {
			t.Errorf("invalid pool settings accepted: %s", query)
		}
	}
}
