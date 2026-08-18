package api

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestMain(tests *testing.M) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		os.Exit(tests.Run())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare isolated API test database: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("QCH_TEST_DATABASE_URL", schema.URL); err != nil {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = schema.Close(cleanupContext)
		cleanupCancel()
		fmt.Fprintf(os.Stderr, "configure isolated API test database: %v\n", err)
		os.Exit(1)
	}
	code := tests.Run()
	cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := schema.Close(cleanupContext); err != nil {
		fmt.Fprintf(os.Stderr, "clean isolated API test database: %v\n", err)
		code = 1
	}
	cleanupCancel()
	os.Exit(code)
}
