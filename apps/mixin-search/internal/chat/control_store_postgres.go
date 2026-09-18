package chat

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultControlNamespace = "chat-v1"

//go:embed control_schema.sql
var controlSchemaSQL string

// PostgresControlStoreConfig configures the chat corpus's persistent control store.
type PostgresControlStoreConfig struct {
	DSN       string
	Namespace string
	// Bootstrap creates the namespace row once. A missing namespace without
	// bootstrap fails closed: an empty control plane must never be mistaken for
	// an authoritative one after data loss.
	Bootstrap bool
}

// PostgresControlStore persists one complete, generation-guarded chat snapshot
// per namespace in its own table.
type PostgresControlStore struct {
	pool      *pgxpool.Pool
	namespace string
	// lastSnapshotBytes carries the size of the last written payload to the
	// admission guard without re-encoding the state.
	lastSnapshotBytes atomic.Int64
}

// NewPostgresControlStore opens the chat control store and verifies that its
// namespace exists.
func NewPostgresControlStore(ctx context.Context, config PostgresControlStoreConfig) (*PostgresControlStore, error) {
	config.DSN = strings.TrimSpace(config.DSN)
	if config.DSN == "" {
		return nil, errors.New("chat control store DSN is required")
	}
	config.Namespace = strings.TrimSpace(config.Namespace)
	if config.Namespace == "" {
		config.Namespace = defaultControlNamespace
	}
	if len(config.Namespace) > 255 {
		return nil, errors.New("chat control store namespace must not exceed 255 bytes")
	}

	pool, err := pgxpool.New(ctx, config.DSN)
	if err != nil {
		return nil, fmt.Errorf("create chat control store pool: %w", err)
	}
	store := &PostgresControlStore{pool: pool, namespace: config.Namespace}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping chat control store: %w", err)
	}
	if _, err := pool.Exec(ctx, controlSchemaSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("initialize chat control schema: %w", err)
	}

	if config.Bootstrap {
		initialPayload, err := json.Marshal(newControlState())
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("marshal initial chat control state: %w", err)
		}
		if _, err := pool.Exec(ctx, `
INSERT INTO mixin_search_control.chat_control_states (namespace, generation, payload)
VALUES ($1, 0, $2)
ON CONFLICT (namespace) DO NOTHING`, config.Namespace, initialPayload); err != nil {
			pool.Close()
			return nil, fmt.Errorf("initialize chat control namespace: %w", err)
		}
	}

	var exists bool
	if err := pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM mixin_search_control.chat_control_states WHERE namespace = $1
)`, config.Namespace).Scan(&exists); err != nil {
		pool.Close()
		return nil, fmt.Errorf("check chat control namespace: %w", err)
	}
	if !exists {
		pool.Close()
		return nil, fmt.Errorf("chat control store namespace %q is not initialized", config.Namespace)
	}
	return store, nil
}

func (s *PostgresControlStore) Load(ctx context.Context) (ControlState, error) {
	var (
		generation int64
		payload    []byte
	)
	if err := s.pool.QueryRow(ctx, `
SELECT generation, payload
FROM mixin_search_control.chat_control_states
WHERE namespace = $1`, s.namespace).Scan(&generation, &payload); err != nil {
		return ControlState{}, fmt.Errorf("load chat control state: %w", err)
	}
	if generation < 0 {
		return ControlState{}, fmt.Errorf("load chat control state: negative generation %d", generation)
	}
	var state ControlState
	if err := json.Unmarshal(payload, &state); err != nil {
		return ControlState{}, fmt.Errorf("decode chat control state: %w", err)
	}
	state.Generation = uint64(generation)
	normalizeControlState(&state)
	if err := validateControlState(state); err != nil {
		return ControlState{}, err
	}
	// Record what was actually in the row. The capacity guard compares against this
	// size, and on a namespace shared with another instance the row may be larger
	// than anything this instance ever wrote.
	s.lastSnapshotBytes.Store(int64(len(payload)))
	return cloneControlState(state)
}

// Generation reads only the generation column: a reader uses it to decide
// whether its published snapshot is still current without transferring or
// decoding the payload.
func (s *PostgresControlStore) Generation(ctx context.Context) (uint64, error) {
	var generation int64
	if err := s.pool.QueryRow(ctx, `
SELECT generation
FROM mixin_search_control.chat_control_states
WHERE namespace = $1`, s.namespace).Scan(&generation); err != nil {
		return 0, fmt.Errorf("read chat control generation: %w", err)
	}
	if generation < 0 {
		return 0, fmt.Errorf("read chat control generation: negative generation %d", generation)
	}
	return uint64(generation), nil
}

func (s *PostgresControlStore) Save(ctx context.Context, expectedGeneration uint64, state ControlState) (uint64, error) {
	if state.Generation != expectedGeneration {
		return 0, fmt.Errorf("%w: snapshot=%d expected=%d", ErrControlStoreConflict, state.Generation, expectedGeneration)
	}
	if expectedGeneration >= math.MaxInt64 {
		return 0, fmt.Errorf("%w: generation overflow", ErrControlStoreConflict)
	}
	state, err := cloneControlState(state)
	if err != nil {
		return 0, err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return 0, fmt.Errorf("encode chat control state: %w", err)
	}

	var nextGeneration int64
	err = s.pool.QueryRow(ctx, `
UPDATE mixin_search_control.chat_control_states
SET generation = generation + 1,
    payload = $1,
    updated_at = now()
WHERE namespace = $2 AND generation = $3
RETURNING generation`, payload, s.namespace, int64(expectedGeneration)).Scan(&nextGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("%w: namespace=%q expected=%d", ErrControlStoreConflict, s.namespace, expectedGeneration)
	}
	if err != nil {
		return 0, fmt.Errorf("save chat control state: %w", err)
	}
	if nextGeneration != int64(expectedGeneration)+1 {
		return 0, fmt.Errorf("save chat control state: next generation=%d expected=%d", nextGeneration, expectedGeneration+1)
	}
	s.lastSnapshotBytes.Store(int64(len(payload)))
	return uint64(nextGeneration), nil
}

// LastSnapshotBytes reports the encoded size of the last persisted snapshot. The
// payload is serialized to be written, so reporting its length costs nothing.
func (s *PostgresControlStore) LastSnapshotBytes() int64 {
	if s == nil {
		return 0
	}
	return s.lastSnapshotBytes.Load()
}

// Close releases the connection pool.
func (s *PostgresControlStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresControlStore) StorageDomain() string { return "chat-postgres:" + s.namespace }
