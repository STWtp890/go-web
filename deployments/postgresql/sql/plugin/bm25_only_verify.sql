-- plugin/bm25_only_verify.sql - database boundary verification.
--
-- The repository keeps both the P1/P2 go-web document schema and the
-- source-owned service schemas in step during the ADR-017 migration. This file
-- only asserts the boundaries that hold for every layout:
--
--   1. ParadeDB pg_search is installed (formal document BM25 search exists);
--   2. no business schema stores vector columns or vector indexes: vector search
--      belongs to Qdrant, and the PostgreSQL database must not grow a second,
--      silently diverging retrieval path;
--   3. obsolete Phase 1 relations never come back;
--   4. the source-owned service schemas expose their own tables and do not
--      reference another service's user table.
--
-- Per-service write isolation is proven separately by
-- deployments/postgresql/verify-service-isolation.ps1, because privilege checks
-- must run as the service roles rather than as the bootstrap superuser.

DO $$
DECLARE
    vector_columns text;
    vector_indexes text;
    obsolete_relations text;
    obsolete_functions text;
    service_schemas text;
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_extension WHERE extname = 'pg_search'
    ) THEN
        RAISE EXCEPTION 'search boundary violation: pg_search extension is missing';
    END IF;

    -- Vector columns are forbidden in every business schema, including the new
    -- service schemas: document-search and qq-search keep their vectors in
    -- Qdrant collections, not in PostgreSQL.
    SELECT string_agg(
               format('%I.%I.%I', table_schema, table_name, column_name),
               ', ' ORDER BY table_schema, table_name, ordinal_position
           )
      INTO vector_columns
      FROM information_schema.columns
     WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
       AND table_schema NOT LIKE 'pg\_%'
       AND udt_name = 'vector';

    IF vector_columns IS NOT NULL THEN
        RAISE EXCEPTION 'search boundary violation: vector columns found: %', vector_columns;
    END IF;

    SELECT string_agg(format('%I.%I', schemaname, indexname), ', ' ORDER BY schemaname, indexname)
      INTO vector_indexes
      FROM pg_indexes
     WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
       AND schemaname NOT LIKE 'pg\_%'
       AND (
           indexdef ILIKE '%::vector%'
           OR indexdef ~* 'vector_(l2|ip|cosine)_ops'
       );

    IF vector_indexes IS NOT NULL THEN
        RAISE EXCEPTION 'search boundary violation: vector indexes found: %', vector_indexes;
    END IF;

    SELECT string_agg(relation_name, ', ' ORDER BY relation_name)
      INTO obsolete_relations
      FROM unnest(ARRAY[
          'schema_migrations',
          'markdowns',
          'markdown_contents',
          'index_outbox',
          'document_index_states',
          'idx_markdowns_paradedb',
          -- The ADR-017 stage C removals. Each of these lived in public and was
          -- replaced by a source-owned schema; a reappearing one means an older
          -- baseline was applied over the new structure.
          'knowledge_spaces',
          'space_members',
          'documents',
          'document_versions',
          'document_access_policies',
          'document_grants',
          'document_search_projection',
          'document_index_rebuild_runs',
          'document_index_delivery_events',
          'document_search_shadow_observations',
          'qq_user_bindings',
          'group_space_bindings',
          'space_audit_events'
      ]) AS relation_name
     WHERE to_regclass('public.' || relation_name) IS NOT NULL;

    IF obsolete_relations IS NOT NULL THEN
        RAISE EXCEPTION 'development baseline violation: obsolete relations found: %', obsolete_relations;
    END IF;

    -- The legacy trigger functions went with their tables. They are checked by
    -- name because a stray function would silently re-arm on a recreated table.
    SELECT string_agg(function_name, ', ' ORDER BY function_name)
      INTO obsolete_functions
      FROM unnest(ARRAY[
          'validate_space_membership',
          'validate_document_active_version',
          'protect_document_version_content',
          'validate_group_space_binding'
      ]) AS function_name
     WHERE to_regprocedure('public.' || function_name || '()') IS NOT NULL;

    IF obsolete_functions IS NOT NULL THEN
        RAISE EXCEPTION 'development baseline violation: obsolete functions found: %', obsolete_functions;
    END IF;

    SELECT string_agg(schema_name, ', ' ORDER BY schema_name)
      INTO service_schemas
      FROM unnest(ARRAY['document_service', 'document_search', 'qq_search']) AS schema_name
     WHERE to_regnamespace(schema_name) IS NULL;

    IF service_schemas IS NOT NULL THEN
        RAISE EXCEPTION 'source-owned service schemas are missing: %', service_schemas;
    END IF;

    -- The document service owns its own access subjects. A foreign key pointing
    -- back at another service's user table would put identity ownership in two
    -- places at once, which is exactly what ADR-017 removed.
    IF EXISTS (
        SELECT 1
          FROM pg_constraint AS constraint_state
          JOIN pg_class AS source_relation
            ON source_relation.oid = constraint_state.conrelid
          JOIN pg_namespace AS source_namespace
            ON source_namespace.oid = source_relation.relnamespace
          JOIN pg_class AS target_relation
            ON target_relation.oid = constraint_state.confrelid
          JOIN pg_namespace AS target_namespace
            ON target_namespace.oid = target_relation.relnamespace
         WHERE constraint_state.contype = 'f'
           AND source_namespace.nspname IN ('document_service', 'document_search', 'qq_search')
           AND target_namespace.nspname IN ('public', 'auth', 'manager', 'chat')
    ) THEN
        RAISE EXCEPTION 'identity ownership violation: a service schema references another service user table';
    END IF;
END
$$;

SELECT 'SEARCH_BOUNDARY_OK' AS verification,
       extension_state.extversion AS pg_search_version
  FROM pg_extension AS extension_state
 WHERE extension_state.extname = 'pg_search';
