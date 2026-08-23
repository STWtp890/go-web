-- chat/schema_init.sql — 聊天群组、消息及可靠投递表
CREATE TABLE IF NOT EXISTS chat_groups (
    id         bigserial PRIMARY KEY,
    group_id   varchar(64)  NOT NULL,
    name       varchar(128) NOT NULL,
    owner_id   varchar(64)  NOT NULL,
    created_at bigint,
    updated_at bigint,
    deleted_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS uni_chat_groups_group_id ON chat_groups (group_id);
CREATE INDEX IF NOT EXISTS idx_chat_groups_owner_id ON chat_groups (owner_id);

CREATE TABLE IF NOT EXISTS chat_group_members (
    id         bigserial PRIMARY KEY,
    group_id   varchar(64) NOT NULL,
    member_id  varchar(64) NOT NULL,
    created_at bigint,
    updated_at bigint,
    deleted_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_member ON chat_group_members (group_id, member_id);

CREATE TABLE IF NOT EXISTS chat_messages (
    id         bigserial PRIMARY KEY,
    group_type varchar(16) NOT NULL,
    from_id    varchar(64) NOT NULL,
    to_id      varchar(64) NOT NULL,
    payload    text        NOT NULL,
    created_at bigint,
    updated_at bigint,
    deleted_at timestamptz
);
CREATE INDEX IF NOT EXISTS idx_message_to_type ON chat_messages (to_id, group_type);
CREATE INDEX IF NOT EXISTS idx_chat_messages_from_id ON chat_messages (from_id);

-- 应用层 ACK 可靠投递：表中只保留尚未被接收端应用成功处理的 pending delivery。
CREATE TABLE IF NOT EXISTS chat_message_deliveries (
    delivery_id        uuid PRIMARY KEY,
    message_id         bigint      NOT NULL,
    message_created_at bigint      NOT NULL,
    recipient_id       varchar(64) NOT NULL,
    created_at         bigint      NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_delivery_recipient_message
    ON chat_message_deliveries (recipient_id, message_id, message_created_at);
CREATE INDEX IF NOT EXISTS idx_delivery_pending
    ON chat_message_deliveries (recipient_id, created_at, delivery_id);
