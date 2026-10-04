-- document-service/schema_init.sql
--
-- Owned by apps/document-service. This schema is the only business write
-- boundary for formal documents, knowledge spaces, membership, group-to-space
-- bindings, resource authorization and the document change Outbox.
--
-- Identity: the document service owns its own resource access subject registry.
-- It never references another service's user table. A subject key is a canonical
-- namespace-qualified string such as 'web:user:42' or 'qq:user:10001/20002'.
-- Web subjects and QQ subjects are separate identities and are NOT required to be
-- bound to each other; the two request families only need to prove their own
-- identity at a trusted entry point.
--
-- The baseline is applied by the bootstrap superuser, whose search_path is not
-- this service schema, so every object and reference below is schema-qualified on
-- purpose. Leaving one unqualified would silently create or look up the object in
-- `public`, which is exactly the cross-service coupling this schema removes.
--
-- A fresh development database is initialized directly through
-- deployments/postgresql/entryscript/00-init.sh. No upgrade runner is kept.

CREATE SCHEMA IF NOT EXISTS document_service;

CREATE TABLE document_service.access_subjects (
    subject_key varchar(192) PRIMARY KEY CHECK (btrim(subject_key) <> ''),
    subject_type varchar(16) NOT NULL CHECK (subject_type IN ('user', 'group')),
    origin varchar(32) NOT NULL CHECK (btrim(origin) <> ''),
    display_name varchar(128) NOT NULL DEFAULT '',
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    -- The key must start with its own origin segment, so a row can never claim
    -- to be a web subject while carrying a qq prefix.
    CHECK (subject_key LIKE origin || ':%')
);
CREATE INDEX idx_access_subjects_origin ON document_service.access_subjects(origin, subject_type);

CREATE TABLE document_service.knowledge_spaces (
    space_id uuid PRIMARY KEY,
    owner_subject_key varchar(192) NOT NULL REFERENCES document_service.access_subjects(subject_key) ON DELETE RESTRICT,
    space_type varchar(16) NOT NULL CHECK (space_type IN ('private', 'team')),
    name varchar(128) NOT NULL CHECK (btrim(name) <> ''),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- A subject has at most one private space. A QQ subject without one still keeps
-- a resource envelope: its team memberships.
CREATE UNIQUE INDEX uq_knowledge_spaces_private_owner
    ON document_service.knowledge_spaces(owner_subject_key) WHERE space_type = 'private';

CREATE TABLE document_service.space_members (
    space_id uuid NOT NULL REFERENCES document_service.knowledge_spaces(space_id) ON DELETE CASCADE,
    subject_key varchar(192) NOT NULL REFERENCES document_service.access_subjects(subject_key) ON DELETE RESTRICT,
    member_role varchar(16) NOT NULL CHECK (member_role IN ('owner', 'admin', 'member')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    revoked_at timestamptz,
    PRIMARY KEY (space_id, subject_key),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);
CREATE UNIQUE INDEX uq_space_members_owner_role
    ON document_service.space_members(space_id) WHERE member_role = 'owner' AND revoked_at IS NULL;
CREATE INDEX idx_space_members_subject_active
    ON document_service.space_members(subject_key, space_id) WHERE revoked_at IS NULL;
-- The deferred membership trigger probes "does an active owner exist for this
-- space" once per affected row, so it needs its own covering index rather than
-- scanning the whole membership table each time.
CREATE INDEX idx_space_members_active_owner
    ON document_service.space_members(space_id, subject_key) WHERE member_role = 'owner' AND revoked_at IS NULL;

CREATE TABLE document_service.documents (
    document_id uuid PRIMARY KEY,
    owner_subject_key varchar(192) NOT NULL REFERENCES document_service.access_subjects(subject_key) ON DELETE RESTRICT,
    owner_space_id uuid NOT NULL REFERENCES document_service.knowledge_spaces(space_id) ON DELETE RESTRICT,
    lifecycle_status varchar(16) NOT NULL DEFAULT 'active' CHECK (lifecycle_status IN ('active', 'archived', 'trashed')),
    active_version_id uuid,
    activation_revision bigint NOT NULL DEFAULT 0 CHECK (activation_revision >= 0),
    access_revision bigint NOT NULL DEFAULT 0 CHECK (access_revision >= 0),
    lifecycle_revision bigint NOT NULL DEFAULT 0 CHECK (lifecycle_revision >= 0),
    aggregate_revision bigint NOT NULL DEFAULT 0 CHECK (aggregate_revision >= 0),
    create_request_id varchar(128),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    trashed_at timestamptz,
    CHECK ((lifecycle_status = 'trashed') = (trashed_at IS NOT NULL))
);
CREATE INDEX idx_documents_owner ON document_service.documents(owner_subject_key, created_at DESC);
CREATE INDEX idx_documents_space ON document_service.documents(owner_space_id, created_at DESC);
CREATE INDEX idx_documents_lifecycle ON document_service.documents(lifecycle_status, updated_at DESC);
-- Idempotent creation: the same requester reusing a request id gets the document
-- it already created instead of a second one.
CREATE UNIQUE INDEX uq_documents_create_request
    ON document_service.documents(owner_subject_key, create_request_id) WHERE create_request_id IS NOT NULL;

CREATE TABLE document_service.document_versions (
    version_id uuid PRIMARY KEY,
    document_id uuid NOT NULL REFERENCES document_service.documents(document_id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK (revision > 0),
    publication_status varchar(16) NOT NULL CHECK (publication_status IN ('draft', 'published', 'superseded', 'withdrawn')),
    title varchar(255) NOT NULL CHECK (btrim(title) <> ''),
    summary varchar(512) NOT NULL DEFAULT '',
    content text NOT NULL CHECK (btrim(content) <> ''),
    content_format varchar(16) NOT NULL DEFAULT 'markdown' CHECK (content_format IN ('markdown', 'doc', 'docx')),
    content_sha256 char(64) NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    created_by_subject_key varchar(192) NOT NULL REFERENCES document_service.access_subjects(subject_key) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (document_id, revision),
    UNIQUE (document_id, version_id)
);
ALTER TABLE document_service.documents ADD CONSTRAINT fk_documents_active_version
    FOREIGN KEY (document_id, active_version_id)
    REFERENCES document_service.document_versions(document_id, version_id)
    DEFERRABLE INITIALLY DEFERRED;

-- The durable SaveDocument idempotency ledger. One immutable row per committed
-- (document, request id): the version the first attempt produced and the
-- fingerprint of the payload it was committed with.
--
-- This table, not a column on documents, is the source of truth for "has this
-- request id already been committed". A single last_save_request_id column only
-- remembered the most recent save, so an older request redelivered after a newer
-- save was executed again and overwrote the newer body. A keyed ledger recognizes
-- a retry whenever it arrives.
--
-- The row is written in the same transaction as the version it names, so a rolled
-- back save leaves no "already handled" record behind. The composite foreign key
-- proves the named version really belongs to the same document.
CREATE TABLE document_service.document_save_requests (
    document_id uuid NOT NULL REFERENCES document_service.documents(document_id) ON DELETE CASCADE,
    request_id varchar(128) NOT NULL CHECK (btrim(request_id) <> ''),
    version_id uuid NOT NULL,
    payload_fingerprint char(64) NOT NULL CHECK (payload_fingerprint ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (document_id, request_id),
    FOREIGN KEY (document_id, version_id)
        REFERENCES document_service.document_versions(document_id, version_id) ON DELETE CASCADE
);
CREATE INDEX idx_document_save_requests_version
    ON document_service.document_save_requests(document_id, version_id);

-- Origin tracing for content promoted from a raw source such as a QQ record.
-- The source record id gives idempotent submission and traceability; it never
-- replaces document_id and never makes raw content a formal document by itself.
CREATE TABLE document_service.document_sources (
    document_id uuid PRIMARY KEY REFERENCES document_service.documents(document_id) ON DELETE CASCADE,
    origin varchar(32) NOT NULL CHECK (btrim(origin) <> ''),
    bot_id varchar(64) NOT NULL DEFAULT '',
    conversation_id varchar(192) NOT NULL DEFAULT '',
    source_record_id varchar(192) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX uq_document_sources_record
    ON document_service.document_sources(origin, bot_id, conversation_id, source_record_id)
    WHERE source_record_id <> '';

CREATE TABLE document_service.document_access_policies (
    document_id uuid PRIMARY KEY REFERENCES document_service.documents(document_id) ON DELETE CASCADE,
    authenticated_public boolean NOT NULL DEFAULT false,
    access_revision bigint NOT NULL CHECK (access_revision >= 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Explicit document level grants. Both grantee kinds are document-service
-- subjects: there is no reference to any other service's user table.
CREATE TABLE document_service.document_grants (
    grant_id uuid PRIMARY KEY,
    document_id uuid NOT NULL REFERENCES document_service.documents(document_id) ON DELETE CASCADE,
    subject_type varchar(16) NOT NULL CHECK (subject_type IN ('subject', 'space')),
    grantee_subject_key varchar(192) REFERENCES document_service.access_subjects(subject_key) ON DELETE RESTRICT,
    grantee_space_id uuid REFERENCES document_service.knowledge_spaces(space_id) ON DELETE RESTRICT,
    access_revision bigint NOT NULL CHECK (access_revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    revoked_at timestamptz,
    CHECK ((subject_type = 'subject' AND grantee_subject_key IS NOT NULL AND grantee_space_id IS NULL)
        OR (subject_type = 'space' AND grantee_subject_key IS NULL AND grantee_space_id IS NOT NULL)),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);
CREATE UNIQUE INDEX uq_document_grants_active_subject ON document_service.document_grants(document_id, grantee_subject_key)
    WHERE subject_type = 'subject' AND revoked_at IS NULL;
CREATE UNIQUE INDEX uq_document_grants_active_space ON document_service.document_grants(document_id, grantee_space_id)
    WHERE subject_type = 'space' AND revoked_at IS NULL;

-- QQ group to space bindings. One active binding per (channel, bot_id, group)
-- and per (channel, bot_id, space); rebinding closes the old row and appends a
-- new one so space migration stays auditable.
CREATE TABLE document_service.group_space_bindings (
    binding_id uuid PRIMARY KEY,
    channel varchar(16) NOT NULL DEFAULT 'qq' CHECK (channel = 'qq'),
    bot_id varchar(64) NOT NULL CHECK (btrim(bot_id) <> ''),
    external_group_id varchar(64) NOT NULL CHECK (btrim(external_group_id) <> ''),
    space_id uuid NOT NULL REFERENCES document_service.knowledge_spaces(space_id) ON DELETE RESTRICT,
    actor varchar(128) NOT NULL CHECK (btrim(actor) <> ''),
    source varchar(32) NOT NULL CHECK (btrim(source) <> ''),
    reason varchar(512) NOT NULL CHECK (btrim(reason) <> ''),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    revoked_at timestamptz,
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);
CREATE UNIQUE INDEX uq_group_space_bindings_active_group
    ON document_service.group_space_bindings(channel, bot_id, external_group_id) WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX uq_group_space_bindings_active_space_per_bot
    ON document_service.group_space_bindings(channel, bot_id, space_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_group_space_bindings_space_active
    ON document_service.group_space_bindings(space_id) WHERE revoked_at IS NULL;

-- Append-only audit. Every subject, binding, space and member mutation commits
-- its audit row in the same transaction; a failed mutation leaves no audit
-- history.
CREATE TABLE document_service.space_audit_events (
    event_id uuid PRIMARY KEY,
    subject_type varchar(32) NOT NULL CHECK (subject_type IN ('access_subject', 'group_space_binding', 'space', 'space_member')),
    action varchar(16) NOT NULL CHECK (action IN ('bind', 'rebind', 'revoke', 'create', 'grant', 'register')),
    subject_key varchar(192),
    channel varchar(16),
    bot_id varchar(64),
    external_id varchar(64),
    space_id uuid REFERENCES document_service.knowledge_spaces(space_id) ON DELETE RESTRICT,
    previous_target varchar(192),
    new_target varchar(192),
    actor varchar(128) NOT NULL CHECK (btrim(actor) <> ''),
    source varchar(32) NOT NULL CHECK (btrim(source) <> ''),
    reason varchar(512) NOT NULL CHECK (btrim(reason) <> ''),
    request_id varchar(128) NOT NULL DEFAULT '',
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (
        (subject_type = 'group_space_binding' AND channel = 'qq' AND btrim(bot_id) <> '' AND btrim(external_id) <> '')
        OR subject_type IN ('access_subject', 'space', 'space_member')
    )
);
CREATE INDEX idx_space_audit_events_subject ON document_service.space_audit_events(subject_type, occurred_at DESC);
CREATE INDEX idx_space_audit_events_qq_identity ON document_service.space_audit_events(channel, bot_id, external_id, occurred_at DESC);

-- The document change Outbox. Business rows and their events commit in one
-- transaction, so a document write is never conditional on a search service
-- being reachable. document-search consumes this table through the
-- ListDocumentEvents stream with its own cursor and tolerates redelivery.
CREATE TABLE document_service.document_events (
    sequence bigserial PRIMARY KEY,
    event_id uuid NOT NULL UNIQUE,
    document_id uuid NOT NULL REFERENCES document_service.documents(document_id) ON DELETE RESTRICT,
    event_kind varchar(24) NOT NULL CHECK (event_kind IN ('upsert', 'delete')),
    aggregate_revision bigint NOT NULL CHECK (aggregate_revision >= 0),
    dedupe_key varchar(255) NOT NULL UNIQUE CHECK (btrim(dedupe_key) <> ''),
    payload bytea NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX idx_document_events_document_order
    ON document_service.document_events(document_id, aggregate_revision, sequence);

CREATE OR REPLACE FUNCTION document_service.validate_space_membership() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE checked_space_id uuid; current_space document_service.knowledge_spaces%ROWTYPE;
BEGIN
    checked_space_id := COALESCE(NEW.space_id, OLD.space_id);
    SELECT * INTO current_space FROM document_service.knowledge_spaces WHERE space_id = checked_space_id;
    IF NOT FOUND THEN RETURN NULL; END IF;
    IF NOT EXISTS (
        SELECT 1 FROM document_service.space_members
        WHERE space_id = checked_space_id
          AND subject_key = current_space.owner_subject_key
          AND member_role = 'owner'
          AND revoked_at IS NULL
    ) THEN
        RAISE EXCEPTION 'space % must have its owner as an active owner member', checked_space_id;
    END IF;
    IF current_space.space_type = 'private' AND EXISTS (
        SELECT 1 FROM document_service.space_members
        WHERE space_id = checked_space_id
          AND (subject_key <> current_space.owner_subject_key OR member_role <> 'owner' OR revoked_at IS NOT NULL)
    ) THEN
        RAISE EXCEPTION 'private space % may only contain its active owner', checked_space_id;
    END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_knowledge_spaces_validate_membership
    AFTER INSERT OR UPDATE ON document_service.knowledge_spaces
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
    EXECUTE FUNCTION document_service.validate_space_membership();
CREATE CONSTRAINT TRIGGER trg_space_members_validate_membership
    AFTER INSERT OR UPDATE OR DELETE ON document_service.space_members
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
    EXECUTE FUNCTION document_service.validate_space_membership();

CREATE OR REPLACE FUNCTION document_service.validate_document_active_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.active_version_id IS NULL THEN RETURN NULL; END IF;
    IF NOT EXISTS (
        SELECT 1 FROM document_service.document_versions
        WHERE document_id = NEW.document_id
          AND version_id = NEW.active_version_id
          AND publication_status = 'published'
    ) THEN
        RAISE EXCEPTION 'active version % for document % must be published', NEW.active_version_id, NEW.document_id;
    END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_documents_validate_active_version
    AFTER INSERT OR UPDATE OF active_version_id ON document_service.documents
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
    EXECUTE FUNCTION document_service.validate_document_active_version();

CREATE OR REPLACE FUNCTION document_service.protect_document_version_content() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.document_id, NEW.revision, NEW.title, NEW.summary, NEW.content, NEW.content_format, NEW.content_sha256, NEW.created_by_subject_key, NEW.created_at)
       IS DISTINCT FROM
       ROW(OLD.document_id, OLD.revision, OLD.title, OLD.summary, OLD.content, OLD.content_format, OLD.content_sha256, OLD.created_by_subject_key, OLD.created_at) THEN
        RAISE EXCEPTION 'document version content is immutable';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER trg_document_versions_immutable_content
    BEFORE UPDATE ON document_service.document_versions
    FOR EACH ROW EXECUTE FUNCTION document_service.protect_document_version_content();

CREATE OR REPLACE FUNCTION document_service.validate_group_space_binding() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.revoked_at IS NULL AND NOT EXISTS (
        SELECT 1 FROM document_service.knowledge_spaces
        WHERE space_id = NEW.space_id AND space_type = 'team'
    ) THEN
        RAISE EXCEPTION 'QQ groups may only bind to team knowledge spaces';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER trg_group_space_bindings_team_only
    BEFORE INSERT OR UPDATE OF space_id, revoked_at ON document_service.group_space_bindings
    FOR EACH ROW EXECUTE FUNCTION document_service.validate_group_space_binding();

CREATE OR REPLACE FUNCTION document_service.protect_document_events() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'document events are append-only';
END $$;
CREATE TRIGGER trg_document_events_append_only
    BEFORE UPDATE OR DELETE ON document_service.document_events
    FOR EACH ROW EXECUTE FUNCTION document_service.protect_document_events();
