-- auth/schema_init.sql — 用户认证业务表
CREATE TABLE IF NOT EXISTS users (
    id         bigserial PRIMARY KEY,
    email      varchar(128) NOT NULL,
    password   varchar(128) NOT NULL,
    nickname   varchar(64),
    avatar     varchar(255) DEFAULT '',
    banned     boolean      DEFAULT false,
    cache_revision bigint   NOT NULL DEFAULT 1 CHECK (cache_revision > 0),
    created_at bigint,
    updated_at bigint,
    deleted_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS uni_users_email ON users (email);

CREATE OR REPLACE FUNCTION bump_user_cache_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.cache_revision := OLD.cache_revision + 1;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS trg_users_cache_revision ON users;
CREATE TRIGGER trg_users_cache_revision
    BEFORE UPDATE OF email, password, nickname, avatar, banned, deleted_at, cache_revision ON users
    FOR EACH ROW EXECUTE FUNCTION bump_user_cache_revision();
