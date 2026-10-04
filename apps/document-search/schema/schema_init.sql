-- document-search/schema_init.sql
--
-- Owned by apps/document-search. Holds only derived index data for formal
-- documents plus its own consumption cursor and applied event log.
--
-- Invariants:
--   * The service consumes document-service change events only. It never calls
--     back into document-service, go-web or py-agent while answering a query.
--   * Its own tables live in the document_search schema and are written with its
--     own database role. Its only privilege on another schema is a column-scoped
--     read grant on the document service Outbox.
--   * The applied event log stores the event payload verbatim, so a rebuild
--     replays local data and never refetches content from the fact source.
--
-- Every object and reference is schema-qualified because the bootstrap
-- superuser's search_path is not this schema.

CREATE SCHEMA IF NOT EXISTS document_search;

-- One row per consumer stream. The cursor is the last applied Outbox sequence;
-- it advances only inside the same transaction that applies the event, so a
-- crash cannot skip an event. Re-delivery after a crash is expected and handled
-- by the idempotent apply path.
CREATE TABLE document_search.consumer_cursors (
    stream varchar(64) PRIMARY KEY,
    last_sequence bigint NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Raw applied events. event_id is unique, so replaying the same event twice is a
-- no-op, and the payload is kept so a rebuild never needs the fact source.
CREATE TABLE document_search.document_index_events (
    sequence bigint PRIMARY KEY CHECK (sequence > 0),
    event_id uuid NOT NULL UNIQUE,
    document_id uuid NOT NULL,
    event_kind varchar(24) NOT NULL CHECK (event_kind IN ('upsert', 'delete')),
    aggregate_revision bigint NOT NULL CHECK (aggregate_revision >= 0),
    payload bytea NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX idx_document_index_events_document
    ON document_search.document_index_events(document_id, aggregate_revision DESC);

-- The indexable document projection. access_revision and lifecycle_revision are
-- monotonic fences: a lower revision must never overwrite a newer one, which is
-- what makes out-of-order delivery harmless.
CREATE TABLE document_search.document_index (
    document_id uuid PRIMARY KEY,
    version_id uuid NOT NULL,
    owner_subject_key varchar(192) NOT NULL,
    owner_space_id uuid NOT NULL,
    authenticated_public boolean NOT NULL DEFAULT false,
    allowed_space_ids jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(allowed_space_ids) = 'array'),
    lifecycle_status varchar(16) NOT NULL,
    publication_status varchar(16) NOT NULL,
    title varchar(255) NOT NULL,
    summary varchar(512) NOT NULL DEFAULT '',
    content text NOT NULL DEFAULT '',
    content_format varchar(16) NOT NULL DEFAULT 'markdown',
    content_sha256 char(64) NOT NULL DEFAULT '',
    chunk_count integer NOT NULL DEFAULT 0 CHECK (chunk_count >= 0),
    index_profile varchar(32) NOT NULL DEFAULT 'markdown-v1',
    -- Which embedding produced this document's points, and how wide they are.
    -- It is the per-document half of the generation record: a row still says what
    -- its stored vector means after the configured profile has moved on.
    vector_profile varchar(32) NOT NULL DEFAULT 'local-hash-v1',
    vector_dimensions integer NOT NULL DEFAULT 64 CHECK (vector_dimensions > 0),
    aggregate_revision bigint NOT NULL CHECK (aggregate_revision >= 0),
    activation_revision bigint NOT NULL CHECK (activation_revision >= 0),
    access_revision bigint NOT NULL CHECK (access_revision >= 0),
    lifecycle_revision bigint NOT NULL CHECK (lifecycle_revision >= 0),
    search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('simple', coalesce(summary, '')), 'B') ||
        setweight(to_tsvector('simple', coalesce(content, '')), 'C')
    ) STORED,
    -- The document's creation instant as the fact source stated it. The default
    -- only covers an event that states none: the apply path keeps the stored
    -- value when a later event omits it, so a redelivery cannot move it.
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    -- The document's own update instant: the fact source stamps
    -- documents.updated_at in the transaction whose instant is the event's
    -- occurred_at, and that is what a hit must present. It is deliberately a
    -- separate column from updated_at below, which is only when this index wrote
    -- the row (what DocumentIndexState.indexed_at reports).
    document_updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX idx_document_index_search ON document_search.document_index USING gin(search_vector);
CREATE INDEX idx_document_index_owner_space ON document_search.document_index(owner_space_id);
CREATE INDEX idx_document_index_owner_subject ON document_search.document_index(owner_subject_key);
CREATE INDEX idx_document_index_allowed_spaces ON document_search.document_index USING gin(allowed_space_ids);
-- Substring recall for short queries, where a lexeme match is too strict.
CREATE INDEX idx_document_index_title_trgm ON document_search.document_index USING gin(title gin_trgm_ops);
CREATE INDEX idx_document_index_scope
    ON document_search.document_index(lifecycle_status, publication_status);

-- Deleted documents stay as tombstones: a late event for an older lifecycle
-- revision must not resurrect them, and operators can still see that the
-- document exists in the source system.
CREATE TABLE document_search.document_index_tombstones (
    document_id uuid PRIMARY KEY,
    lifecycle_revision bigint NOT NULL CHECK (lifecycle_revision >= 0),
    aggregate_revision bigint NOT NULL CHECK (aggregate_revision >= 0),
    deleted_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Rebuild bookkeeping. Rebuilds replay the applied event log already stored in
-- this schema; no fact source is contacted.
CREATE TABLE document_search.rebuild_runs (
    run_id uuid PRIMARY KEY,
    state varchar(16) NOT NULL DEFAULT 'running'
        CHECK (state IN ('running', 'succeeded', 'failed')),
    documents_rebuilt integer NOT NULL DEFAULT 0 CHECK (documents_rebuilt >= 0),
    documents_skipped integer NOT NULL DEFAULT 0 CHECK (documents_skipped >= 0),
    documents_failed integer NOT NULL DEFAULT 0 CHECK (documents_failed >= 0),
    last_error text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz
);

-- The vector collection is a separate Qdrant namespace. This table records which
-- collection alias, storage domain, embedding profile and vector width each index
-- generation uses, so two services can never share a physical collection by
-- accident and a rebuild can find the corpus without a second naming convention
-- in code.
--
-- The physical collection of a generation is <collection_alias>_<generation>.
-- A collection under the alias prefix that no row here names is a leftover and is
-- what a rebuild's cleanup removes.
CREATE TABLE document_search.index_generations (
    generation varchar(64) PRIMARY KEY,
    collection_alias varchar(128) NOT NULL UNIQUE CHECK (btrim(collection_alias) <> ''),
    storage_domain varchar(128) NOT NULL CHECK (btrim(storage_domain) <> ''),
    vector_profile varchar(32) NOT NULL DEFAULT 'local-hash-v1',
    vector_dimensions integer NOT NULL DEFAULT 64 CHECK (vector_dimensions > 0),
    active boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX uq_document_search_active_generation
    ON document_search.index_generations(active) WHERE active;

-- Development databases initialized before the vector flow existed are brought
-- up to the same baseline. Each statement is idempotent, so applying this file
-- again on a current database changes nothing.
ALTER TABLE document_search.document_index
    ADD COLUMN IF NOT EXISTS vector_profile varchar(32) NOT NULL DEFAULT 'local-hash-v1';
ALTER TABLE document_search.document_index
    ADD COLUMN IF NOT EXISTS vector_dimensions integer NOT NULL DEFAULT 64 CHECK (vector_dimensions > 0);
ALTER TABLE document_search.index_generations
    ADD COLUMN IF NOT EXISTS vector_profile varchar(32) NOT NULL DEFAULT 'local-hash-v1';
ALTER TABLE document_search.index_generations
    ADD COLUMN IF NOT EXISTS vector_dimensions integer NOT NULL DEFAULT 64 CHECK (vector_dimensions > 0);

INSERT INTO document_search.consumer_cursors(stream, last_sequence)
VALUES ('document-events', 0)
ON CONFLICT (stream) DO NOTHING;

INSERT INTO document_search.index_generations(generation, collection_alias, storage_domain, vector_profile, vector_dimensions, active)
VALUES ('g1', 'go_web_document_v1', 'document-search:documents:v1', 'local-hash-v1', 64, true)
ON CONFLICT (generation) DO NOTHING;
