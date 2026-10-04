package application

// This file reports index status. The two collection identities are read from
// qq_search.index_collections rather than from configuration, because the table
// is the record of what the schema baseline actually created: a status answer
// built from config could claim two collections exist while the database holds
// one.

import (
	"context"
	"fmt"

	qqsearchv1 "packages/gen/qqsearch/v1"
)

const (
	// collectionsQuery reads the two index namespaces.
	collectionsQuery = `SELECT record_kind, collection_alias, storage_domain FROM qq_search.index_collections`

	// statusQuery counts indexed records per corpus, the applied-event ledger and
	// the consumer cursor.
	statusQuery = `
SELECT (SELECT count(*) FROM qq_search.qq_messages WHERE status = 'indexed'),
       (SELECT count(*) FROM qq_search.qq_files WHERE status = 'indexed'),
       (SELECT count(*) FROM qq_search.qq_applied_events),
       (SELECT COALESCE(MAX(last_sequence), 0) FROM qq_search.consumer_state WHERE stream = $1)`
)

// collectionIdentity is one corpus's index namespace.
type collectionIdentity struct {
	alias  string
	domain string
}

// validateCollectionIdentity refuses a status answer in which the two corpora
// share a collection. That is the failure the source split exists to prevent: a
// shared collection lets a message query return a file.
func validateCollectionIdentity(message, file collectionIdentity) error {
	if message.alias == "" || file.alias == "" {
		return fmt.Errorf("qq-search: index_collections must define an alias for both corpora")
	}
	if message.alias == file.alias {
		return fmt.Errorf("qq-search: message and file collections share the alias %q", message.alias)
	}
	if message.domain == "" || file.domain == "" {
		return fmt.Errorf("qq-search: index_collections must define a storage domain for both corpora")
	}
	if message.domain == file.domain {
		return fmt.Errorf("qq-search: message and file collections share the storage domain %q", message.domain)
	}
	return nil
}

// loadCollections reads both index namespaces.
func loadCollections(ctx context.Context, q querier) (map[RecordKind]collectionIdentity, error) {
	rows, err := q.Query(ctx, collectionsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	collections := make(map[RecordKind]collectionIdentity, 2)
	for rows.Next() {
		var (
			kind   string
			alias  string
			domain string
		)
		if err := rows.Scan(&kind, &alias, &domain); err != nil {
			return nil, err
		}
		collections[RecordKind(kind)] = collectionIdentity{alias: alias, domain: domain}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return collections, nil
}

// status reports the two collection identities and applied progress.
func (service *Service) status(ctx context.Context) (*qqsearchv1.GetIndexStatusResponse, error) {
	if err := service.requirePool(); err != nil {
		return nil, err
	}
	pool := service.pool.Pgx()
	collections, err := loadCollections(ctx, pool)
	if err != nil {
		return nil, internalError("read index collections: %v", err)
	}
	message, ok := collections[RecordKindMessage]
	if !ok {
		return nil, internalError("index_collections has no %q entry: the schema baseline was not applied", RecordKindMessage)
	}
	file, ok := collections[RecordKindFile]
	if !ok {
		return nil, internalError("index_collections has no %q entry: the schema baseline was not applied", RecordKindFile)
	}
	if err := validateCollectionIdentity(message, file); err != nil {
		return nil, internalError("%v", err)
	}

	var indexedMessages, indexedFiles, eventsApplied, lastSequence int64
	if err := pool.QueryRow(ctx, statusQuery, consumerStream).Scan(
		&indexedMessages, &indexedFiles, &eventsApplied, &lastSequence,
	); err != nil {
		return nil, internalError("read index status: %v", err)
	}
	return &qqsearchv1.GetIndexStatusResponse{
		MessageCollection: message.alias,
		FileCollection:    file.alias,
		IndexedMessages:   clampInt32(indexedMessages),
		IndexedFiles:      clampInt32(indexedFiles),
		EventsApplied:     eventsApplied,
		LastSequence:      lastSequence,
	}, nil
}
