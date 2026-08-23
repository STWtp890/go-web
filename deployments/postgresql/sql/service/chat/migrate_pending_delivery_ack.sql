-- 将旧的 acknowledged_at 状态模型迁移为“表内只有 pending delivery”的应用层 ACK 模型。
-- 可重复执行；先删除已 ACK 历史记录，再移除不再使用的状态列。
BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'chat_message_deliveries'
          AND column_name = 'acknowledged_at'
    ) THEN
        DELETE FROM chat_message_deliveries WHERE acknowledged_at IS NOT NULL;
    END IF;
END
$$;

DROP INDEX IF EXISTS idx_delivery_pending;

ALTER TABLE chat_message_deliveries
    DROP COLUMN IF EXISTS acknowledged_at,
    DROP COLUMN IF EXISTS delivered_at;

CREATE UNIQUE INDEX IF NOT EXISTS uq_delivery_recipient_message
    ON chat_message_deliveries (recipient_id, message_id, message_created_at);

CREATE INDEX IF NOT EXISTS idx_delivery_pending
    ON chat_message_deliveries (recipient_id, created_at, delivery_id);

COMMIT;
