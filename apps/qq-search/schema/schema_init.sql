-- qq-search/schema_init.sql
--
-- Owned by apps/qq-search. Holds the raw QQ search data only: original chat
-- messages and original files, each with its own model, table, lifecycle and
-- index namespace.
--
-- Invariants:
--   * Raw QQ content is never a formal document. Promoting a QQ record to a
--     formal document is a call py-agent makes to document-service; the formal
--     document then belongs to document-service and is indexed by
--     document-search.
--   * Recall is an append-only fact. A recalled record stays in the table marked
--     as recalled; it is never deleted, so the audit trail survives.
--   * Events arrive from py-agent through IndexQQSourceEvent. This service never
--     calls back into py-agent while answering a query, and it reads no other
--     service's tables.
--
-- Every object and reference is schema-qualified because the bootstrap
-- superuser's search_path is not this schema.

CREATE SCHEMA IF NOT EXISTS qq_search;

CREATE TABLE qq_search.qq_messages (
    record_id varchar(192) PRIMARY KEY,
    channel varchar(16) NOT NULL DEFAULT 'qq' CHECK (channel = 'qq'),
    bot_id varchar(64) NOT NULL CHECK (btrim(bot_id) <> ''),
    conversation_id varchar(192) NOT NULL CHECK (btrim(conversation_id) <> ''),
    conversation_kind varchar(16) NOT NULL CHECK (conversation_kind IN ('private', 'group')),
    external_user_id varchar(64) NOT NULL DEFAULT '',
    external_group_id varchar(64) NOT NULL DEFAULT '',
    sender_external_user_id varchar(64) NOT NULL DEFAULT '',
    text_content text NOT NULL DEFAULT '',
    sent_at timestamptz,
    platform_sequence bigint NOT NULL DEFAULT 0,
    record_revision bigint NOT NULL DEFAULT 1 CHECK (record_revision > 0),
    status varchar(16) NOT NULL DEFAULT 'indexed' CHECK (status IN ('indexed', 'recalled', 'deleted')),
    recalled_at timestamptz,
    last_event_id uuid,
    indexed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((status = 'recalled') = (recalled_at IS NOT NULL)),
    CHECK (conversation_kind <> 'group' OR btrim(external_group_id) <> '')
);
CREATE INDEX idx_qq_messages_conversation ON qq_search.qq_messages(bot_id, conversation_id, sent_at DESC NULLS LAST);
CREATE INDEX idx_qq_messages_status ON qq_search.qq_messages(status);
CREATE INDEX idx_qq_messages_search ON qq_search.qq_messages
    USING gin(to_tsvector('simple', coalesce(text_content, '')));

CREATE TABLE qq_search.qq_files (
    record_id varchar(192) PRIMARY KEY,
    channel varchar(16) NOT NULL DEFAULT 'qq' CHECK (channel = 'qq'),
    bot_id varchar(64) NOT NULL CHECK (btrim(bot_id) <> ''),
    conversation_id varchar(192) NOT NULL CHECK (btrim(conversation_id) <> ''),
    conversation_kind varchar(16) NOT NULL CHECK (conversation_kind IN ('private', 'group')),
    external_user_id varchar(64) NOT NULL DEFAULT '',
    external_group_id varchar(64) NOT NULL DEFAULT '',
    uploader_external_user_id varchar(64) NOT NULL DEFAULT '',
    file_name varchar(512) NOT NULL DEFAULT '',
    mime_type varchar(128) NOT NULL DEFAULT '',
    size_bytes bigint NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
    content_sha256 varchar(64) NOT NULL DEFAULT '',
    storage_ref varchar(512) NOT NULL DEFAULT '',
    uploaded_at timestamptz,
    record_revision bigint NOT NULL DEFAULT 1 CHECK (record_revision > 0),
    status varchar(16) NOT NULL DEFAULT 'indexed' CHECK (status IN ('indexed', 'recalled', 'deleted')),
    recalled_at timestamptz,
    last_event_id uuid,
    indexed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((status = 'recalled') = (recalled_at IS NOT NULL)),
    CHECK (conversation_kind <> 'group' OR btrim(external_group_id) <> '')
);
CREATE INDEX idx_qq_files_conversation ON qq_search.qq_files(bot_id, conversation_id, uploaded_at DESC NULLS LAST);
CREATE INDEX idx_qq_files_status ON qq_search.qq_files(status);
CREATE INDEX idx_qq_files_search ON qq_search.qq_files
    USING gin(to_tsvector('simple', coalesce(file_name, '') || ' ' || coalesce(mime_type, '')));
CREATE INDEX idx_qq_files_name_trgm ON qq_search.qq_files USING gin(file_name gin_trgm_ops);

-- Applied event ledger. Records which py-agent event produced which record
-- revision so duplicate delivery is a no-op and a rebuild can replay locally.
CREATE TABLE qq_search.qq_applied_events (
    event_id uuid PRIMARY KEY,
    sequence bigint NOT NULL DEFAULT 0,
    record_kind varchar(16) NOT NULL CHECK (record_kind IN ('message', 'file')),
    record_id varchar(192) NOT NULL,
    record_revision bigint NOT NULL CHECK (record_revision > 0),
    event_kind varchar(24) NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (record_kind, record_id, record_revision)
);

CREATE TABLE qq_search.consumer_state (
    stream varchar(64) PRIMARY KEY,
    last_sequence bigint NOT NULL DEFAULT 0 CHECK (last_sequence >= 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE qq_search.rebuild_runs (
    run_id uuid PRIMARY KEY,
    state varchar(16) NOT NULL DEFAULT 'running'
        CHECK (state IN ('running', 'succeeded', 'failed')),
    messages_rebuilt integer NOT NULL DEFAULT 0 CHECK (messages_rebuilt >= 0),
    files_rebuilt integer NOT NULL DEFAULT 0 CHECK (files_rebuilt >= 0),
    records_failed integer NOT NULL DEFAULT 0 CHECK (records_failed >= 0),
    last_error text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz
);

CREATE TABLE qq_search.index_collections (
    record_kind varchar(16) PRIMARY KEY CHECK (record_kind IN ('message', 'file')),
    collection_alias varchar(128) NOT NULL UNIQUE CHECK (btrim(collection_alias) <> ''),
    storage_domain varchar(128) NOT NULL CHECK (btrim(storage_domain) <> ''),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO qq_search.consumer_state(stream, last_sequence)
VALUES ('qq-source-events', 0)
ON CONFLICT (stream) DO NOTHING;

-- Two separate collections, never one shared collection: a message query must
-- not be able to return a file and the other way round.
INSERT INTO qq_search.index_collections(record_kind, collection_alias, storage_domain) VALUES
    ('message', 'qq_source_messages_v1', 'qq-search:messages:v1'),
    ('file', 'qq_source_files_v1', 'qq-search:files:v1')
ON CONFLICT (record_kind) DO NOTHING;
