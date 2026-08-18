-- markdown/schema_init.sql — Markdown 文档业务表
CREATE TABLE IF NOT EXISTS markdowns (
    id          bigserial PRIMARY KEY,
    markdown_id varchar(64)  NOT NULL,
    author_id   varchar(64)  NOT NULL,
    title       varchar(255) NOT NULL,
    summary     varchar(512) DEFAULT '',
    visibility  varchar(16)  NOT NULL DEFAULT 'private',
    search_text text         NOT NULL DEFAULT '',
    created_at  bigint,
    updated_at  bigint,
    deleted_at  timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS uni_markdowns_markdown_id ON markdowns (markdown_id);
ALTER INDEX IF EXISTS idx_markdowns_author_user_id RENAME TO idx_markdowns_author_id;
CREATE INDEX IF NOT EXISTS idx_markdowns_author_id ON markdowns (author_id);

CREATE TABLE IF NOT EXISTS markdown_contents (
    id          bigserial PRIMARY KEY,
    markdown_id varchar(64) NOT NULL,
    content     text        NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uni_markdown_contents_markdown_id
    ON markdown_contents (markdown_id);
