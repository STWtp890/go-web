// Package testkit holds the helpers the PostgreSQL integration suites of this
// module share.
//
// Both suites (internal/application and internal/interfaces/grpcapi) run as
// parallel packages inside one `go test ./...` invocation and mutate the same
// qq_search schema: every test indexes records, rebuilds the derived index and
// deletes its own rows, while some counters (applied events, indexed messages)
// are global to the schema. Two suites doing that at once is a property of the
// test run, not of the service, and it makes the counter assertions flaky: a
// cleanup in one package can land between the "before" and "after" reads of a
// test in the other. The suites therefore serialize on one session-level
// advisory lock - "one qq-search integration test at a time on this database".
//
// This package is test support. It carries no production path: nothing in
// cmd/qq-search imports it, and it does not exist to make the service behave
// differently under test.
package testkit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// integrationLockKey is a fixed key in the qq-search namespace ("qq_srch").
const integrationLockKey int64 = 0x71715f73726368

// integrationLockTimeout bounds the wait for another suite, so a stuck run
// fails the test with a clear message instead of hanging the whole build.
const integrationLockTimeout = 90 * time.Second

// Serialize takes the cross-package integration lock and returns the function
// that releases it. The lock lives on a session-level connection, so it is held
// for as long as the test runs; the caller must invoke the returned function
// (a t.Cleanup registered before the row cleanup keeps both in one critical
// section).
func Serialize(ctx context.Context, pool *pgxpool.Pool) (func(), error) {
	if pool == nil {
		return nil, fmt.Errorf("testkit: database pool is required")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("testkit: acquire the lock connection: %w", err)
	}
	deadline := time.Now().Add(integrationLockTimeout)
	for {
		var locked bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, integrationLockKey).Scan(&locked); err != nil {
			conn.Release()
			return nil, fmt.Errorf("testkit: take the integration lock: %w", err)
		}
		if locked {
			break
		}
		if time.Now().After(deadline) {
			conn.Release()
			return nil, fmt.Errorf("testkit: another qq-search integration suite held the lock for more than %s",
				integrationLockTimeout)
		}
		select {
		case <-ctx.Done():
			conn.Release()
			return nil, fmt.Errorf("testkit: waiting for the integration lock: %w", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(releaseCtx, `SELECT pg_advisory_unlock($1)`, integrationLockKey)
		conn.Release()
	}, nil
}
