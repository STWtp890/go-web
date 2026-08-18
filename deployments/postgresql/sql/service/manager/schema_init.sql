-- manager/schema_init.sql — 管理员与注册审批业务表
CREATE TABLE IF NOT EXISTS managers (
    id         bigserial PRIMARY KEY,
    username   varchar(64)  NOT NULL,
    password   varchar(128) NOT NULL,
    email      varchar(128) DEFAULT '',
    status     varchar(16)  NOT NULL DEFAULT 'active',
    created_at bigint,
    updated_at bigint,
    deleted_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS uni_managers_username ON managers (username);

CREATE TABLE IF NOT EXISTS manager_registration_requests (
    id             bigserial PRIMARY KEY,
    username       varchar(64)  NOT NULL,
    password_hash  varchar(128) NOT NULL,
    email          varchar(128) DEFAULT '',
    reason         varchar(512) DEFAULT '',
    status         varchar(16)  NOT NULL DEFAULT 'pending',
    reviewer_id    bigint       DEFAULT 0,
    review_comment varchar(512) DEFAULT '',
    reviewed_at    timestamptz,
    created_at     bigint,
    updated_at     bigint,
    deleted_at     timestamptz
);
-- P0/P1 并发注册保护（原 001_p0p1_reliability.sql 的 manager 部分）。
CREATE UNIQUE INDEX IF NOT EXISTS uni_manager_request_active_username
    ON manager_registration_requests (username)
    WHERE status IN ('pending', 'approved');
