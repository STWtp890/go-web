-- plugin/cache_revision_verify.sql — mutable entity cache revision fencing acceptance
DO $verify$
DECLARE
    test_user_email text := format('cache-revision-user-%s@example.invalid', pg_backend_pid());
    test_manager_username text := format('cache_revision_manager_%s', pg_backend_pid());
    test_user_id bigint;
    test_manager_id bigint;
    observed_revision bigint;
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'users'
          AND column_name = 'cache_revision' AND data_type = 'bigint'
    ) THEN
        RAISE EXCEPTION 'users.cache_revision bigint is missing';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'public.users'::regclass
          AND tgname = 'trg_users_cache_revision' AND NOT tgisinternal
    ) THEN
        RAISE EXCEPTION 'trg_users_cache_revision is missing';
    END IF;

    INSERT INTO users (email, password, nickname, avatar, banned, created_at, updated_at)
    VALUES (test_user_email, 'test-only', 'v1', '', false, 0, 0)
    RETURNING id, cache_revision INTO test_user_id, observed_revision;
    IF observed_revision <> 1 THEN
        RAISE EXCEPTION 'initial user cache_revision %, want 1', observed_revision;
    END IF;
    UPDATE users SET nickname = 'v2' WHERE id = test_user_id;
    UPDATE users SET avatar = 'v3' WHERE id = test_user_id;
    SELECT cache_revision INTO observed_revision FROM users WHERE id = test_user_id;
    IF observed_revision <> 3 THEN
        RAISE EXCEPTION 'user cache_revision after two immediate updates %, want 3', observed_revision;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'managers'
          AND column_name = 'cache_revision' AND data_type = 'bigint'
    ) THEN
        RAISE EXCEPTION 'managers.cache_revision bigint is missing';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'public.managers'::regclass
          AND tgname = 'trg_managers_cache_revision' AND NOT tgisinternal
    ) THEN
        RAISE EXCEPTION 'trg_managers_cache_revision is missing';
    END IF;

    INSERT INTO managers (username, password, email, status, created_at, updated_at)
    VALUES (test_manager_username, 'test-only', 'v1@example.invalid', 'active', 0, 0)
    RETURNING id, cache_revision INTO test_manager_id, observed_revision;
    IF observed_revision <> 1 THEN
        RAISE EXCEPTION 'initial manager cache_revision %, want 1', observed_revision;
    END IF;
    UPDATE managers SET email = 'v2@example.invalid' WHERE id = test_manager_id;
    UPDATE managers SET status = 'disabled' WHERE id = test_manager_id;
    SELECT cache_revision INTO observed_revision FROM managers WHERE id = test_manager_id;
    IF observed_revision <> 3 THEN
        RAISE EXCEPTION 'manager cache_revision after two immediate updates %, want 3', observed_revision;
    END IF;

    DELETE FROM users WHERE id = test_user_id;
    DELETE FROM managers WHERE id = test_manager_id;
END
$verify$;

SELECT 'ENTITY_CACHE_REVISION_OK' AS verification;
