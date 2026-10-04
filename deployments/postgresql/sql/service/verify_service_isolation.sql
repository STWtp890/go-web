-- service/verify_service_isolation.sql
--
-- Executable proof of the service write isolation matrix. Run it as the
-- bootstrap superuser with SET ROLE, which exercises the real privilege system
-- without needing password authentication for development accounts:
--
--   docker compose exec -T postgres psql -U postgres -d gin_demo \
--     -v ON_ERROR_STOP=1 -f /sql/service/verify_service_isolation.sql
--
-- Every case states the expected outcome and raises when reality differs, so a
-- silent privilege regression fails the gate instead of passing it.
--
-- Keep this file ASCII-only: psql reads it with the container locale and a
-- multi-byte comment could otherwise swallow the following newline.

DO $$
DECLARE
    unexpected integer;
BEGIN
    -- 1. Each service role writes its own schema ------------------------------
    SET LOCAL ROLE document_service_writer;
    INSERT INTO document_service.access_subjects(subject_key, subject_type, origin, display_name)
    VALUES ('web:user:isolation-probe', 'user', 'web', 'isolation probe')
    ON CONFLICT (subject_key) DO NOTHING;
    DELETE FROM document_service.access_subjects WHERE subject_key = 'web:user:isolation-probe';
    RESET ROLE;

    SET LOCAL ROLE document_search_writer;
    INSERT INTO document_search.consumer_cursors(stream, last_sequence) VALUES ('isolation-probe', 1)
    ON CONFLICT (stream) DO UPDATE SET last_sequence = EXCLUDED.last_sequence;
    DELETE FROM document_search.consumer_cursors WHERE stream = 'isolation-probe';
    RESET ROLE;

    SET LOCAL ROLE qq_search_writer;
    INSERT INTO qq_search.consumer_state(stream, last_sequence) VALUES ('isolation-probe', 1)
    ON CONFLICT (stream) DO UPDATE SET last_sequence = EXCLUDED.last_sequence;
    DELETE FROM qq_search.consumer_state WHERE stream = 'isolation-probe';
    RESET ROLE;

    -- 2. document-search may READ the Outbox it consumes ----------------------
    SET LOCAL ROLE document_search_writer;
    PERFORM 1 FROM document_service.document_events LIMIT 1;
    RESET ROLE;

    -- 3. document-search may NOT write the document service business tables ---
    BEGIN
        SET LOCAL ROLE document_search_writer;
        INSERT INTO document_service.access_subjects(subject_key, subject_type, origin)
        VALUES ('web:user:forbidden', 'user', 'web');
        RESET ROLE;
        RAISE EXCEPTION 'isolation violation: document_search_writer inserted into document_service.access_subjects';
    EXCEPTION
        WHEN insufficient_privilege THEN
            RESET ROLE;
    END;

    BEGIN
        SET LOCAL ROLE document_search_writer;
        UPDATE document_service.document_events SET event_kind = 'delete' WHERE false;
        RESET ROLE;
        RAISE EXCEPTION 'isolation violation: document_search_writer updated document_service.document_events';
    EXCEPTION
        WHEN insufficient_privilege THEN
            RESET ROLE;
    END;

    BEGIN
        SET LOCAL ROLE document_search_writer;
        DELETE FROM document_service.document_events WHERE false;
        RESET ROLE;
        RAISE EXCEPTION 'isolation violation: document_search_writer deleted from document_service.document_events';
    EXCEPTION
        WHEN insufficient_privilege THEN
            RESET ROLE;
    END;

    -- 4. document-service may NOT write the document search schema ------------
    BEGIN
        SET LOCAL ROLE document_service_writer;
        INSERT INTO document_search.consumer_cursors(stream, last_sequence) VALUES ('forbidden', 1);
        RESET ROLE;
        RAISE EXCEPTION 'isolation violation: document_service_writer inserted into document_search.consumer_cursors';
    EXCEPTION
        WHEN insufficient_privilege THEN
            RESET ROLE;
    END;

    -- 5. qq-search is isolated from both other schemas ------------------------
    BEGIN
        SET LOCAL ROLE qq_search_writer;
        SELECT count(*) INTO unexpected FROM document_service.document_events;
        RESET ROLE;
        RAISE EXCEPTION 'isolation violation: qq_search_writer read document_service.document_events';
    EXCEPTION
        WHEN insufficient_privilege THEN
            RESET ROLE;
    END;

    BEGIN
        SET LOCAL ROLE qq_search_writer;
        INSERT INTO document_search.consumer_cursors(stream, last_sequence) VALUES ('forbidden', 1);
        RESET ROLE;
        RAISE EXCEPTION 'isolation violation: qq_search_writer inserted into document_search.consumer_cursors';
    EXCEPTION
        WHEN insufficient_privilege THEN
            RESET ROLE;
    END;

    BEGIN
        SET LOCAL ROLE qq_search_writer;
        INSERT INTO document_service.access_subjects(subject_key, subject_type, origin)
        VALUES ('web:user:forbidden', 'user', 'web');
        RESET ROLE;
        RAISE EXCEPTION 'isolation violation: qq_search_writer inserted into document_service.access_subjects';
    EXCEPTION
        WHEN insufficient_privilege THEN
            RESET ROLE;
    END;

    -- 6. go-web writes its own account tables and nothing in the service schemas
    -- Code review already stopped the Web surface from writing document tables;
    -- these cases prove the database refuses it too, so the boundary no longer
    -- depends on the code staying as it is today.
    SET LOCAL ROLE go_web_app;
    INSERT INTO public.users(email, password, nickname) VALUES ('isolation-probe@example.com', 'x', 'isolation probe')
    ON CONFLICT (email) DO NOTHING;
    DELETE FROM public.users WHERE email = 'isolation-probe@example.com';
    PERFORM 1 FROM public.managers LIMIT 1;
    RESET ROLE;

    BEGIN
        SET LOCAL ROLE go_web_app;
        INSERT INTO document_service.access_subjects(subject_key, subject_type, origin)
        VALUES ('web:user:forbidden', 'user', 'web');
        RESET ROLE;
        RAISE EXCEPTION 'isolation violation: go_web_app inserted into document_service.access_subjects';
    EXCEPTION
        WHEN insufficient_privilege THEN
            RESET ROLE;
    END;

    BEGIN
        SET LOCAL ROLE go_web_app;
        INSERT INTO document_search.consumer_cursors(stream, last_sequence) VALUES ('forbidden', 1);
        RESET ROLE;
        RAISE EXCEPTION 'isolation violation: go_web_app inserted into document_search.consumer_cursors';
    EXCEPTION
        WHEN insufficient_privilege THEN
            RESET ROLE;
    END;

    BEGIN
        SET LOCAL ROLE go_web_app;
        INSERT INTO qq_search.consumer_state(stream, last_sequence) VALUES ('forbidden', 1);
        RESET ROLE;
        RAISE EXCEPTION 'isolation violation: go_web_app inserted into qq_search.consumer_state';
    EXCEPTION
        WHEN insufficient_privilege THEN
            RESET ROLE;
    END;

    BEGIN
        SET LOCAL ROLE go_web_app;
        SELECT count(*) INTO unexpected FROM document_service.document_events;
        RESET ROLE;
        RAISE EXCEPTION 'isolation violation: go_web_app read document_service.document_events';
    EXCEPTION
        WHEN insufficient_privilege THEN
            RESET ROLE;
    END;

    -- 7. The document service owns its own identity: no foreign user table ----
    IF EXISTS (
        SELECT 1
          FROM pg_constraint AS constraint_state
          JOIN pg_class AS source_relation ON source_relation.oid = constraint_state.conrelid
          JOIN pg_namespace AS source_namespace ON source_namespace.oid = source_relation.relnamespace
          JOIN pg_class AS target_relation ON target_relation.oid = constraint_state.confrelid
          JOIN pg_namespace AS target_namespace ON target_namespace.oid = target_relation.relnamespace
         WHERE constraint_state.contype = 'f'
           AND source_namespace.nspname IN ('document_service', 'document_search', 'qq_search')
           AND target_namespace.nspname NOT IN ('document_service', 'document_search', 'qq_search')
    ) THEN
        RAISE EXCEPTION 'identity ownership violation: a service schema references another schema';
    END IF;

    RAISE NOTICE 'SERVICE_ISOLATION_OK';
END
$$;

SELECT 'SERVICE_ISOLATION_OK' AS verification;
