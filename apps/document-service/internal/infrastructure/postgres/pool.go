// Package postgres owns the document service database connection.
//
// The service writes one schema with one dedicated database role. The search
// path is pinned explicitly so a role whose default search_path differs can
// never silently address another service's tables.
package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"document-service/internal/config"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool wraps the pgx pool with the service's schema pinning.
type Pool struct {
	pool   *pgxpool.Pool
	schema string
}

// Open connects to the configured database and verifies the schema is reachable
// before returning, so a misconfigured service fails at startup.
func Open(ctx context.Context, cfg config.PostgresConfig) (*Pool, error) {
	dsn, err := pinSearchPath(cfg.DSN, cfg.Schema)
	if err != nil {
		return nil, err
	}
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("document-service postgres: parse dsn: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		poolConfig.MaxConns = cfg.MaxOpenConns
	}
	if cfg.MaxIdleConns > 0 {
		poolConfig.MinConns = cfg.MaxIdleConns
	}
	if lifetime, err := time.ParseDuration(cfg.ConnMaxLifetime); err == nil && lifetime > 0 {
		poolConfig.MaxConnLifetime = lifetime
	}
	if timeout, err := time.ParseDuration(cfg.ConnectTimeout); err == nil && timeout > 0 {
		poolConfig.ConnConfig.ConnectTimeout = timeout
	}
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "SET search_path TO "+quoteIdentifier(cfg.Schema)+", public"); err != nil {
			return fmt.Errorf("document-service postgres: set search_path: %w", err)
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("document-service postgres: connect: %w", err)
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
		return fmt.Errorf("document-service postgres: pool is not initialized")
	}
	if err := pool.pool.Ping(ctx); err != nil {
		return fmt.Errorf("document-service postgres: ping: %w", err)
	}
	return nil
}

// Acquire returns a connection from the pool.
func (pool *Pool) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	if pool == nil || pool.pool == nil {
		return nil, fmt.Errorf("document-service postgres: pool is not initialized")
	}
	return pool.pool.Acquire(ctx)
}

// Schema reports the pinned schema name.
func (pool *Pool) Schema() string {
	if pool == nil {
		return ""
	}
	return pool.schema
}

// Pgx exposes the underlying pool for repositories. It is deliberately narrow:
// repositories live in this module and are the only callers.
func (pool *Pool) Pgx() *pgxpool.Pool {
	if pool == nil {
		return nil
	}
	return pool.pool
}

// Close releases every connection.
func (pool *Pool) Close() error {
	if pool == nil || pool.pool == nil {
		return nil
	}
	pool.pool.Close()
	return nil
}

// pinSearchPath appends an explicit search_path option unless the DSN already
// sets one. An explicit value in the DSN always wins, because operators may have
// a reason to include public for extensions.
func pinSearchPath(dsn, schema string) (string, error) {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return "", fmt.Errorf("document-service postgres: schema is required")
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
