// Package postgres owns the QQ search database connection. The service writes its
// own schema with its own role and reads no other service's tables.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"qq-search/internal/config"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool wraps the pgx pool with explicit schema pinning.
type Pool struct {
	pool   *pgxpool.Pool
	schema string
}

// Open connects to the configured database and verifies the schema is reachable.
func Open(ctx context.Context, cfg config.PostgresConfig) (*Pool, error) {
	dsn, err := pinSearchPath(cfg.DSN, cfg.Schema)
	if err != nil {
		return nil, err
	}
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("qq-search postgres: parse dsn: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		poolConfig.MaxConns = cfg.MaxOpenConns
	}
	if lifetime, err := time.ParseDuration(cfg.ConnMaxLifetime); err == nil && lifetime > 0 {
		poolConfig.MaxConnLifetime = lifetime
	}
	if timeout, err := time.ParseDuration(cfg.ConnectTimeout); err == nil && timeout > 0 {
		poolConfig.ConnConfig.ConnectTimeout = timeout
	}
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "SET search_path TO "+quoteIdentifier(cfg.Schema)+", public"); err != nil {
			return fmt.Errorf("qq-search postgres: set search_path: %w", err)
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("qq-search postgres: connect: %w", err)
	}
	wrapped := &Pool{pool: pool, schema: cfg.Schema}
	if err := wrapped.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return wrapped, nil
}

// Ping verifies the database answers.
func (pool *Pool) Ping(ctx context.Context) error {
	if pool == nil || pool.pool == nil {
		return errors.New("qq-search postgres: pool is not initialized")
	}
	if err := pool.pool.Ping(ctx); err != nil {
		return fmt.Errorf("qq-search postgres: ping: %w", err)
	}
	return nil
}

// Pgx exposes the underlying pool to repositories inside this module.
func (pool *Pool) Pgx() *pgxpool.Pool {
	if pool == nil {
		return nil
	}
	return pool.pool
}

// Schema reports the pinned schema name.
func (pool *Pool) Schema() string {
	if pool == nil {
		return ""
	}
	return pool.schema
}

// Close releases every connection.
func (pool *Pool) Close() error {
	if pool == nil || pool.pool == nil {
		return nil
	}
	pool.pool.Close()
	return nil
}

func pinSearchPath(dsn, schema string) (string, error) {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return "", errors.New("qq-search postgres: schema is required")
	}
	if strings.Contains(dsn, "search_path=") {
		return dsn, nil
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "search_path=" + quoteIdentifier(schema), nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
