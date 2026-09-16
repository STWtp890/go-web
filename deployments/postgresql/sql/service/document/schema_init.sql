-- document/schema_init.sql - development baseline for the document domain.
-- A fresh database is initialized directly; no upgrade ledger or compatibility tables are maintained.
CREATE TABLE knowledge_spaces (
    space_id uuid PRIMARY KEY,
    owner_id bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    space_type varchar(16) NOT NULL CHECK (space_type IN ('private', 'team')),
    name varchar(128) NOT NULL CHECK (btrim(name) <> ''),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX uq_knowledge_spaces_private_owner ON knowledge_spaces(owner_id) WHERE space_type = 'private';

CREATE TABLE space_members (
    space_id uuid NOT NULL REFERENCES knowledge_spaces(space_id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    member_role varchar(16) NOT NULL CHECK (member_role IN ('owner', 'admin', 'member')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    revoked_at timestamptz,
    PRIMARY KEY (space_id, user_id),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);
CREATE UNIQUE INDEX uq_space_members_owner_role ON space_members(space_id) WHERE member_role = 'owner' AND revoked_at IS NULL;

CREATE TABLE documents (
    document_id uuid PRIMARY KEY,
    owner_id bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    owner_space_id uuid NOT NULL REFERENCES knowledge_spaces(space_id) ON DELETE RESTRICT,
    lifecycle_status varchar(16) NOT NULL DEFAULT 'active' CHECK (lifecycle_status IN ('active', 'archived', 'trashed')),
    active_version_id uuid,
    activation_revision bigint NOT NULL DEFAULT 0 CHECK (activation_revision >= 0),
    access_revision bigint NOT NULL DEFAULT 0 CHECK (access_revision >= 0),
    lifecycle_revision bigint NOT NULL DEFAULT 0 CHECK (lifecycle_revision >= 0),
    aggregate_revision bigint NOT NULL DEFAULT 0 CHECK (aggregate_revision >= 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    trashed_at timestamptz,
    CHECK ((lifecycle_status = 'trashed') = (trashed_at IS NOT NULL))
);
CREATE INDEX idx_documents_owner ON documents(owner_id, created_at DESC);
CREATE INDEX idx_documents_space ON documents(owner_space_id, created_at DESC);
CREATE INDEX idx_documents_lifecycle ON documents(lifecycle_status, updated_at DESC);

CREATE TABLE document_versions (
    version_id uuid PRIMARY KEY,
    document_id uuid NOT NULL REFERENCES documents(document_id) ON DELETE CASCADE,
    revision bigint NOT NULL CHECK (revision > 0),
    publication_status varchar(16) NOT NULL CHECK (publication_status IN ('draft', 'published', 'superseded', 'withdrawn')),
    title varchar(255) NOT NULL CHECK (btrim(title) <> ''),
    summary varchar(512) NOT NULL DEFAULT '',
    content text NOT NULL CHECK (btrim(content) <> ''),
    content_format varchar(16) NOT NULL DEFAULT 'markdown' CHECK (content_format IN ('markdown', 'doc', 'docx')),
    content_sha256 char(64) NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    created_by bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (document_id, revision),
    UNIQUE (document_id, version_id)
);
ALTER TABLE documents ADD CONSTRAINT fk_documents_active_version
    FOREIGN KEY (document_id, active_version_id)
    REFERENCES document_versions(document_id, version_id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE document_access_policies (
    document_id uuid PRIMARY KEY REFERENCES documents(document_id) ON DELETE CASCADE,
    authenticated_public boolean NOT NULL DEFAULT false,
    access_revision bigint NOT NULL CHECK (access_revision >= 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE document_grants (
    grant_id uuid PRIMARY KEY,
    document_id uuid NOT NULL REFERENCES documents(document_id) ON DELETE CASCADE,
    subject_type varchar(16) NOT NULL CHECK (subject_type IN ('user', 'space')),
    grantee_user_id bigint REFERENCES users(id) ON DELETE RESTRICT,
    grantee_space_id uuid REFERENCES knowledge_spaces(space_id) ON DELETE RESTRICT,
    access_revision bigint NOT NULL CHECK (access_revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    revoked_at timestamptz,
    CHECK ((subject_type = 'user' AND grantee_user_id IS NOT NULL AND grantee_space_id IS NULL)
        OR (subject_type = 'space' AND grantee_user_id IS NULL AND grantee_space_id IS NOT NULL)),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);
CREATE UNIQUE INDEX uq_document_grants_active_user ON document_grants(document_id, grantee_user_id)
    WHERE subject_type = 'user' AND revoked_at IS NULL;
CREATE UNIQUE INDEX uq_document_grants_active_space ON document_grants(document_id, grantee_space_id)
    WHERE subject_type = 'space' AND revoked_at IS NULL;

CREATE TABLE document_search_projection (
    document_id uuid PRIMARY KEY REFERENCES documents(document_id) ON DELETE CASCADE,
    version_id uuid NOT NULL,
    owner_id bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    owner_space_id uuid NOT NULL REFERENCES knowledge_spaces(space_id) ON DELETE RESTRICT,
    authenticated_public boolean NOT NULL,
    title varchar(255) NOT NULL,
    summary varchar(512) NOT NULL DEFAULT '',
    search_text text NOT NULL,
    activation_revision bigint NOT NULL CHECK (activation_revision > 0),
    access_revision bigint NOT NULL CHECK (access_revision >= 0),
    lifecycle_revision bigint NOT NULL CHECK (lifecycle_revision >= 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (document_id, version_id) REFERENCES document_versions(document_id, version_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX idx_document_search_projection_owner ON document_search_projection(owner_id);
CREATE INDEX idx_document_search_projection_space ON document_search_projection(owner_space_id);

CREATE TABLE document_index_rebuild_runs (
    run_id uuid PRIMARY KEY,
    state varchar(16) NOT NULL DEFAULT 'preparing'
        CHECK (state IN ('preparing', 'running', 'succeeded', 'failed')),
    snapshot_started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    event_count bigint NOT NULL DEFAULT 0 CHECK (event_count >= 0),
    failure_count bigint NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((state = 'succeeded') = (completed_at IS NOT NULL))
);

CREATE TABLE document_index_delivery_events (
    event_id uuid PRIMARY KEY,
    dedupe_key varchar(255) NOT NULL UNIQUE CHECK (btrim(dedupe_key) <> ''),
    source varchar(16) NOT NULL CHECK (source IN ('transaction', 'reconcile', 'rebuild')),
    source_run_id uuid REFERENCES document_index_rebuild_runs(run_id) ON DELETE RESTRICT,
    document_id uuid NOT NULL REFERENCES documents(document_id) ON DELETE RESTRICT,
    aggregate_revision bigint NOT NULL CHECK (aggregate_revision >= 0),
    event_kind varchar(24) NOT NULL
        CHECK (event_kind IN ('sync_document', 'sync_access', 'delete_version', 'delete_document')),
    version_id uuid REFERENCES document_versions(version_id) ON DELETE RESTRICT,
    previous_version_id uuid REFERENCES document_versions(version_id) ON DELETE RESTRICT,
    owner_space_id uuid REFERENCES knowledge_spaces(space_id) ON DELETE RESTRICT,
    activation_revision bigint NOT NULL CHECK (activation_revision >= 0),
    access_revision bigint NOT NULL CHECK (access_revision >= 0),
    lifecycle_revision bigint NOT NULL CHECK (lifecycle_revision >= 0),
    authenticated_public boolean NOT NULL DEFAULT false,
    granted_space_ids jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(granted_space_ids) = 'array'),
    content_sha256 varchar(64) NOT NULL DEFAULT ''
        CHECK (content_sha256 = '' OR content_sha256 ~ '^[0-9a-f]{64}$'),
    index_profile varchar(32) NOT NULL DEFAULT 'markdown-v1' CHECK (btrim(index_profile) <> ''),
    state varchar(16) NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'processing', 'retry', 'succeeded', 'dead_letter')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    lease_owner varchar(128),
    lease_token uuid,
    lease_expires_at timestamptz,
    last_grpc_code varchar(32) NOT NULL DEFAULT '',
    last_error text NOT NULL DEFAULT '',
    last_attempt_at timestamptz,
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((event_kind IN ('sync_document', 'delete_version')) = (version_id IS NOT NULL)),
    CHECK (event_kind <> 'sync_document' OR owner_space_id IS NOT NULL),
    CHECK (source = 'rebuild' OR source_run_id IS NULL),
    CHECK (source <> 'rebuild' OR source_run_id IS NOT NULL),
    CHECK ((state = 'processing') = (lease_owner IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK ((state = 'succeeded') = (delivered_at IS NOT NULL))
);
CREATE INDEX idx_document_index_delivery_claim
    ON document_index_delivery_events(state, available_at, aggregate_revision, created_at);
CREATE INDEX idx_document_index_delivery_document_order
    ON document_index_delivery_events(document_id, aggregate_revision, created_at, event_id);
CREATE INDEX idx_document_index_delivery_expired_lease
    ON document_index_delivery_events(lease_expires_at)
    WHERE state = 'processing';
CREATE INDEX idx_document_index_delivery_failures
    ON document_index_delivery_events(updated_at DESC)
    WHERE state IN ('retry', 'dead_letter');

CREATE TABLE document_search_shadow_observations (
    observation_id uuid PRIMARY KEY,
    source varchar(16) NOT NULL CHECK (source IN ('runtime', 'evaluation')),
    owner_id bigint NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    query_sha256 char(64) NOT NULL CHECK (query_sha256 ~ '^[0-9a-f]{64}$'),
    query_rune_count integer NOT NULL CHECK (query_rune_count > 0 AND query_rune_count <= 100),
    page integer NOT NULL CHECK (page > 0),
    page_size integer NOT NULL CHECK (page_size > 0 AND page_size <= 100),
    requested_top_k integer NOT NULL CHECK (requested_top_k > 0 AND requested_top_k <= 100),
    bm25_total bigint NOT NULL CHECK (bm25_total >= 0),
    bm25_latency_micros bigint NOT NULL CHECK (bm25_latency_micros >= 0),
    shadow_latency_micros bigint NOT NULL CHECK (shadow_latency_micros >= 0),
    status varchar(16) NOT NULL CHECK (status IN ('succeeded', 'timed_out', 'failed')),
    grpc_code varchar(32) NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    truncated boolean NOT NULL DEFAULT false,
    bm25_document_ids jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(bm25_document_ids) = 'array'),
    shadow_document_ids jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(shadow_document_ids) = 'array'),
    comparable_shadow_document_ids jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(comparable_shadow_document_ids) = 'array'),
    only_bm25_document_ids jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(only_bm25_document_ids) = 'array'),
    only_shadow_document_ids jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(only_shadow_document_ids) = 'array'),
    overlap_count integer NOT NULL DEFAULT 0 CHECK (overlap_count >= 0),
    permission_violation_count integer NOT NULL DEFAULT 0 CHECK (permission_violation_count >= 0),
    lifecycle_violation_count integer NOT NULL DEFAULT 0 CHECK (lifecycle_violation_count >= 0),
    active_version_violation_count integer NOT NULL DEFAULT 0 CHECK (active_version_violation_count >= 0),
    formal_scope_mismatch_count integer NOT NULL DEFAULT 0 CHECK (formal_scope_mismatch_count >= 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX idx_document_search_shadow_observations_created
    ON document_search_shadow_observations(created_at DESC);
CREATE INDEX idx_document_search_shadow_observations_status
    ON document_search_shadow_observations(status, created_at DESC);

CREATE OR REPLACE FUNCTION validate_space_membership() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE checked_space_id uuid; current_space knowledge_spaces%ROWTYPE;
BEGIN
    checked_space_id := COALESCE(NEW.space_id, OLD.space_id);
    SELECT * INTO current_space FROM knowledge_spaces WHERE space_id = checked_space_id;
    IF NOT FOUND THEN RETURN NULL; END IF;
    IF NOT EXISTS (SELECT 1 FROM space_members WHERE space_id = checked_space_id AND user_id = current_space.owner_id AND member_role = 'owner' AND revoked_at IS NULL) THEN
        RAISE EXCEPTION 'space % must have its owner as an active owner member', checked_space_id;
    END IF;
    IF current_space.space_type = 'private' AND EXISTS (SELECT 1 FROM space_members WHERE space_id = checked_space_id AND (user_id <> current_space.owner_id OR member_role <> 'owner' OR revoked_at IS NOT NULL)) THEN
        RAISE EXCEPTION 'private space % may only contain its active owner', checked_space_id;
    END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_knowledge_spaces_validate_membership AFTER INSERT OR UPDATE ON knowledge_spaces DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_space_membership();
CREATE CONSTRAINT TRIGGER trg_space_members_validate_membership AFTER INSERT OR UPDATE OR DELETE ON space_members DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_space_membership();

CREATE OR REPLACE FUNCTION validate_document_active_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.active_version_id IS NULL THEN RETURN NULL; END IF;
    IF NOT EXISTS (SELECT 1 FROM document_versions WHERE document_id = NEW.document_id AND version_id = NEW.active_version_id AND publication_status = 'published') THEN
        RAISE EXCEPTION 'active version % for document % must be published', NEW.active_version_id, NEW.document_id;
    END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_documents_validate_active_version AFTER INSERT OR UPDATE OF active_version_id ON documents DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_document_active_version();

CREATE OR REPLACE FUNCTION protect_document_version_content() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.document_id, NEW.revision, NEW.title, NEW.summary, NEW.content, NEW.content_format, NEW.content_sha256, NEW.created_by, NEW.created_at)
       IS DISTINCT FROM
       ROW(OLD.document_id, OLD.revision, OLD.title, OLD.summary, OLD.content, OLD.content_format, OLD.content_sha256, OLD.created_by, OLD.created_at) THEN
        RAISE EXCEPTION 'document version content is immutable';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER trg_document_versions_immutable_content BEFORE UPDATE ON document_versions FOR EACH ROW EXECUTE FUNCTION protect_document_version_content();
