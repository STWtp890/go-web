package rag

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultControlNamespace = "default"

//go:embed control_schema.sql
var controlSchemaSQL string

type PostgresControlStoreConfig struct {
	DSN       string
	Namespace string
	Bootstrap bool
}

// PostgresControlStore persists one complete, generation-guarded snapshot per
// namespace. The single-row model deliberately serializes P2.1 writes; later
// slices may split aggregates without changing the application-facing port.
type PostgresControlStore struct {
	pool      *pgxpool.Pool
	namespace string
}

func NewPostgresControlStore(
	ctx context.Context,
	config PostgresControlStoreConfig,
) (*PostgresControlStore, error) {
	config.DSN = strings.TrimSpace(config.DSN)
	if config.DSN == "" {
		return nil, errors.New("control store DSN is required")
	}
	config.Namespace = strings.TrimSpace(config.Namespace)
	if config.Namespace == "" {
		config.Namespace = defaultControlNamespace
	}
	if len(config.Namespace) > 255 {
		return nil, errors.New("control store namespace must not exceed 255 bytes")
	}

	pool, err := pgxpool.New(ctx, config.DSN)
	if err != nil {
		return nil, fmt.Errorf("create control store pool: %w", err)
	}
	store := &PostgresControlStore{pool: pool, namespace: config.Namespace}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping control store: %w", err)
	}
	if _, err := pool.Exec(ctx, controlSchemaSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("initialize control store schema: %w", err)
	}

	if config.Bootstrap {
		initialPayload, err := json.Marshal(newControlState())
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("marshal initial control state: %w", err)
		}
		if _, err := pool.Exec(ctx, `
INSERT INTO mixin_search_control.control_states (namespace, generation, payload)
VALUES ($1, 0, $2)
ON CONFLICT (namespace) DO NOTHING`, config.Namespace, initialPayload); err != nil {
			pool.Close()
			return nil, fmt.Errorf("initialize control store namespace: %w", err)
		}
	}
	var exists bool
	if err := pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM mixin_search_control.control_states WHERE namespace = $1
)`, config.Namespace).Scan(&exists); err != nil {
		pool.Close()
		return nil, fmt.Errorf("check control store namespace: %w", err)
	}
	if !exists {
		pool.Close()
		return nil, fmt.Errorf("%w: %q", ErrControlStoreUninitialized, config.Namespace)
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
FROM mixin_search_control.control_states
WHERE namespace = $1`, s.namespace).Scan(&generation, &payload); err != nil {
		return ControlState{}, fmt.Errorf("load control state: %w", err)
	}
	if generation < 0 {
		return ControlState{}, fmt.Errorf("load control state: negative generation %d", generation)
	}

	var state ControlState
	if err := json.Unmarshal(payload, &state); err != nil {
		return ControlState{}, fmt.Errorf("decode control state: %w", err)
	}
	state.Generation = uint64(generation)
	normalizeControlState(&state)
	if err := validateControlState(state); err != nil {
		return ControlState{}, err
	}
	return cloneControlState(state)
}

func (s *PostgresControlStore) Save(
	ctx context.Context,
	expectedGeneration uint64,
	state ControlState,
) (uint64, error) {
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
		return 0, fmt.Errorf("encode control state: %w", err)
	}

	var nextGeneration int64
	err = s.pool.QueryRow(ctx, `
UPDATE mixin_search_control.control_states
SET generation = generation + 1,
    payload = $1,
    updated_at = now()
WHERE namespace = $2 AND generation = $3
RETURNING generation`, payload, s.namespace, int64(expectedGeneration)).Scan(&nextGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("%w: namespace=%q expected=%d", ErrControlStoreConflict, s.namespace, expectedGeneration)
	}
	if err != nil {
		return 0, fmt.Errorf("save control state: %w", err)
	}
	if nextGeneration != int64(expectedGeneration)+1 {
		return 0, fmt.Errorf("save control state: next generation=%d expected=%d", nextGeneration, expectedGeneration+1)
	}
	return uint64(nextGeneration), nil
}

func (s *PostgresControlStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresControlStore) StorageDomain() string { return "postgres:" + s.namespace }
