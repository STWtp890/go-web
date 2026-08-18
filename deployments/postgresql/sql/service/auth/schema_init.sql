-- auth/schema_init.sql — 用户认证业务表
CREATE TABLE IF NOT EXISTS users (
    id         bigserial PRIMARY KEY,
    email      varchar(128) NOT NULL,
    password   varchar(128) NOT NULL,
    nickname   varchar(64),
    avatar     varchar(255) DEFAULT '',
    banned     boolean      DEFAULT false,
    created_at bigint,
    updated_at bigint,
    deleted_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS uni_users_email ON users (email);
