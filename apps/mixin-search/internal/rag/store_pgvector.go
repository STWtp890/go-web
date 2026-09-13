package rag

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
	pgxvector "github.com/pgvector/pgvector-go/pgx"
)

type PGVectorConfig struct {
	DSN        string
	Dimensions int
}

type PGVectorStore struct {
	pool *pgxpool.Pool
}

func NewPGVectorStore(ctx context.Context, config PGVectorConfig) (*PGVectorStore, error) {
	if config.DSN == "" {
		return nil, errors.New("pgvector DSN is required")
	}
	if config.Dimensions == 0 {
		config.Dimensions = localEmbeddingDimensions
	}
	if config.Dimensions < 1 || config.Dimensions > 2000 {
		return nil, fmt.Errorf("pgvector dimensions must be between 1 and 2000, got %d", config.Dimensions)
	}

	setupConnection, err := pgx.Connect(ctx, config.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect postgres for setup: %w", err)
	}
	if _, err := setupConnection.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		_ = setupConnection.Close(ctx)
		return nil, fmt.Errorf("enable pgvector extension: %w", err)
	}
	if err := setupConnection.Close(ctx); err != nil {
		return nil, fmt.Errorf("close postgres setup connection: %w", err)
	}

	poolConfig, err := pgxpool.ParseConfig(config.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse pgvector DSN: %w", err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvector.RegisterTypes(ctx, conn)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create pgvector pool: %w", err)
	}
	store := &PGVectorStore{pool: pool}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping pgvector: %w", err)
	}

	createTable := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS rag_chunks (
    id text PRIMARY KEY,
    document_id text NOT NULL,
    title text NOT NULL,
    content text NOT NULL,
    position integer NOT NULL,
	format text NOT NULL DEFAULT '',
	source text NOT NULL DEFAULT '',
	section text NOT NULL DEFAULT '',
    embedding vector(%d) NOT NULL,
    tokens_text text NOT NULL,
    textsearch tsvector GENERATED ALWAYS AS (to_tsvector('simple', tokens_text)) STORED
)`, config.Dimensions)
	statements := []string{
		createTable,
		"ALTER TABLE rag_chunks ADD COLUMN IF NOT EXISTS format text NOT NULL DEFAULT ''",
		"ALTER TABLE rag_chunks ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT ''",
		"ALTER TABLE rag_chunks ADD COLUMN IF NOT EXISTS section text NOT NULL DEFAULT ''",
		"CREATE INDEX IF NOT EXISTS rag_chunks_embedding_hnsw ON rag_chunks USING hnsw (embedding vector_cosine_ops)",
		"CREATE INDEX IF NOT EXISTS rag_chunks_textsearch_gin ON rag_chunks USING gin (textsearch)",
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			pool.Close()
			return nil, fmt.Errorf("initialize pgvector schema: %w", err)
		}
	}
	return store, nil
}

func (s *PGVectorStore) ReplaceDocument(ctx context.Context, documentID string, chunks []IndexedChunk) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin pgvector replace: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "DELETE FROM rag_chunks WHERE document_id = $1", documentID); err != nil {
		return fmt.Errorf("delete old pgvector chunks: %w", err)
	}
	for _, chunk := range chunks {
		_, err := tx.Exec(ctx, `
INSERT INTO rag_chunks (id, document_id, title, content, position, format, source, section, embedding, tokens_text)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			chunk.Chunk.ID,
			chunk.Chunk.DocumentID,
			chunk.Chunk.Title,
			chunk.Chunk.Content,
			chunk.Chunk.Position,
			chunk.Chunk.Format,
			chunk.Chunk.Source,
			chunk.Chunk.Section,
			pgvector.NewVector(toFloat32(chunk.Dense)),
			strings.Join(tokenize(chunk.Chunk.Content), " "),
		)
		if err != nil {
			return fmt.Errorf("insert pgvector chunk %s: %w", chunk.Chunk.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit pgvector replace: %w", err)
	}
	return nil
}

func (s *PGVectorStore) DenseSearch(ctx context.Context, query []float64, limit int) ([]ScoredChunk, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, document_id, title, content, position, format, source, section, 1 - (embedding <=> $1) AS score
FROM rag_chunks
ORDER BY embedding <=> $1, id
LIMIT $2`, pgvector.NewVector(toFloat32(query)), limit)
	if err != nil {
		return nil, fmt.Errorf("query pgvector dense index: %w", err)
	}
	defer rows.Close()
	return scanPGVectorHits(rows)
}

func (s *PGVectorStore) SparseSearch(ctx context.Context, queryTokens []string, limit int) ([]ScoredChunk, error) {
	if limit <= 0 || len(queryTokens) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
WITH query AS (SELECT to_tsquery('simple', $1) AS value)
SELECT id, document_id, title, content, position, format, source, section, ts_rank_cd(textsearch, query.value) AS score
FROM rag_chunks, query
WHERE textsearch @@ query.value
ORDER BY score DESC, id
LIMIT $2`, buildTSQuery(queryTokens), limit)
	if err != nil {
		return nil, fmt.Errorf("query pgvector text index: %w", err)
	}
	defer rows.Close()
	return scanPGVectorHits(rows)
}

func (s *PGVectorStore) Close() error {
	s.pool.Close()
	return nil
}

func scanPGVectorHits(rows pgx.Rows) ([]ScoredChunk, error) {
	var hits []ScoredChunk
	for rows.Next() {
		var hit ScoredChunk
		if err := rows.Scan(
			&hit.Chunk.ID,
			&hit.Chunk.DocumentID,
			&hit.Chunk.Title,
			&hit.Chunk.Content,
			&hit.Chunk.Position,
			&hit.Chunk.Format,
			&hit.Chunk.Source,
			&hit.Chunk.Section,
			&hit.Score,
		); err != nil {
			return nil, fmt.Errorf("scan pgvector result: %w", err)
		}
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pgvector results: %w", err)
	}
	return hits, nil
}

func buildTSQuery(tokens []string) string {
	unique := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if token != "" {
			unique[token] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(unique))
	for token := range unique {
		ordered = append(ordered, token)
	}
	sort.Strings(ordered)
	return strings.Join(ordered, " | ")
}
