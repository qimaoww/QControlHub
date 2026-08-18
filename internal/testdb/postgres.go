// Package testdb provides isolated PostgreSQL schemas for integration tests.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
)

type Schema struct {
	URL        string
	connection *pgx.Conn
	name       string
}

func IsolatePostgres(ctx context.Context, databaseURL string) (*Schema, error) {
	identifier := make([]byte, 8)
	if _, err := rand.Read(identifier); err != nil {
		return nil, fmt.Errorf("generate test schema name: %w", err)
	}
	name := "qcontrolhub_test_" + hex.EncodeToString(identifier)
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect for test schema: %w", err)
	}
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); err != nil {
		connection.Close(ctx)
		return nil, fmt.Errorf("create test schema: %w", err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		connection.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{name}.Sanitize()+" CASCADE")
		connection.Close(ctx)
		return nil, fmt.Errorf("parse test database URL: %w", err)
	}
	query := parsed.Query()
	query.Set("search_path", name)
	parsed.RawQuery = query.Encode()
	return &Schema{URL: parsed.String(), connection: connection, name: name}, nil
}

func (schema *Schema) Close(ctx context.Context) error {
	if schema == nil || schema.connection == nil {
		return nil
	}
	_, dropErr := schema.connection.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema.name}.Sanitize()+" CASCADE")
	closeErr := schema.connection.Close(ctx)
	if dropErr != nil {
		return fmt.Errorf("drop test schema: %w", dropErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close test schema connection: %w", closeErr)
	}
	return nil
}
