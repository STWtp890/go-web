-- ============================================================
-- service/chat/timescaledb_setup.sql — chat_messages 的 TimescaleDB 业务配置
--
-- 适用: gin-backend chat 业务 (默认库 gin_demo)
-- 前置:
--   1) 已执行 ../plugin/timescaledb_setup.sql (镜像或自装扩展见 ../../chat-timescaledb.md)
--   2) shared_preload_libraries 已含 'timescaledb' (timescale 官方镜像默认配置)
--   3) 表结构已由 service/chat/schema_init.sql 建好；本脚本负责 hypertable 转换
-- 用法: psql -U postgres -d gin_demo -f deployments/postgresql/sql/service/chat/timescaledb_setup.sql
-- 说明: 脚本可重复执行 (幂等); 大表转换前请先备份 (见文档"回滚方案")
-- ============================================================

-- 1. 主键必须包含分区时间列 (TimescaleDB 约束: UNIQUE/PRIMARY KEY 必须含分区列)
--    现有表主键为 id 单键 → 重建为复合主键 (created_at, id)
--    created_at 为 bigint (Unix 秒, GORM autoCreateTime), 与 id 共同唯一且保持单调一致
DO $$
DECLARE
  tbl       regclass := to_regclass('chat_messages');
  pk_name   text;
  pk_has_time boolean;
BEGIN
  IF tbl IS NULL THEN
    RETURN; -- 表尚未创建 (先跑 automigrate)
  END IF;

  SELECT c.conname,
         EXISTS (
           SELECT 1
           FROM unnest(i.indkey) WITH ORDINALITY k(attnum, ord)
           JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
           WHERE a.attname = 'created_at'
         )
  INTO pk_name, pk_has_time
  FROM pg_index i
  JOIN pg_constraint c ON c.conindid = i.indexrelid AND c.contype = 'p'
  WHERE i.indrelid = 'chat_messages'::regclass;

  IF pk_name IS NULL THEN
    RAISE NOTICE 'chat_messages 无主键, 跳过主键重建';
  ELSIF NOT pk_has_time THEN
    EXECUTE format('ALTER TABLE chat_messages DROP CONSTRAINT %I', pk_name);
    ALTER TABLE chat_messages
      ADD CONSTRAINT chat_messages_pkey PRIMARY KEY (created_at, id);
  END IF;
END $$;

-- 2. 转换为 hypertable
--    时间列: created_at (bigint, Unix 秒) — TimescaleDB 官方支持整数时间
--    chunk 间隔: 86400 秒 = 1 天 (按消息量可调整, 目标每个 chunk 数百万行)
--    by_range 为 TimescaleDB 2.13+ 语法; 旧版本改为:
--      SELECT create_hypertable('chat_messages', 'created_at', chunk_time_interval => 86400);
--    migrate_data => TRUE: 已有数据一并搬入 (转换期间持锁, 大表请低峰执行)
SELECT create_hypertable(
    'chat_messages',
    by_range('created_at', 86400),
    if_not_exists => TRUE,
    migrate_data  => TRUE
);

-- 3. 覆盖索引: 支撑 "按 to_id 定位 + 时间倒序 LIMIT N" 的群历史/离线拉取
--    (分区裁剪后, chunk 内走该索引即可完成排序, 无需回表排序)
CREATE INDEX IF NOT EXISTS idx_message_to_type_time
  ON chat_messages (to_id, group_type, created_at DESC, id DESC);

-- 4. (可选) 压缩策略: 7 天前的 chunk 自动压缩
--    - compress_segmentby: 仅允许低基数列 (to_id 合适; payload 等变长高基数列不允许)
--    - compress_orderby: 与覆盖索引一致的时间倒序 (附 id 兜底, 压缩后仍可高效范围扫描)
--    - 注意: created_at 为整数时间 (bigint Unix 秒), compress_after 必须用整数秒
--      (INTERVAL 会报错: integer duration required for hypertables with integer time dimension)
--    - 整数时间维度还必须配置 integer_now 函数，否则压缩策略后台作业无法计算当前时间。
--    重复执行幂等 (相同参数); 若需修改压缩参数需先关再开
CREATE OR REPLACE FUNCTION chat_messages_integer_now()
RETURNS BIGINT
LANGUAGE SQL
STABLE
AS $$
  SELECT EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)::BIGINT;
$$;

SELECT set_integer_now_func(
  'chat_messages',
  'chat_messages_integer_now',
  replace_if_exists => TRUE
);

ALTER TABLE chat_messages SET (
  timescaledb.compress,
  timescaledb.compress_segmentby = 'to_id',
  timescaledb.compress_orderby   = 'created_at DESC, id DESC'
);
SELECT add_compression_policy('chat_messages', compress_after => 604800, if_not_exists => TRUE);

-- 5. (默认关闭) 保留策略: chat 消息默认全量保留 (业务决策, 见文档)
--    若后续确定留存周期, 放开下行并调整周期即可
-- SELECT add_retention_policy('chat_messages', INTERVAL '90 days', if_not_exists => TRUE);

-- 6. 验证
SELECT hypertable_name, num_dimensions
  FROM timescaledb_information.hypertables
 WHERE hypertable_name = 'chat_messages';

SELECT chunk_name, range_start, range_end
  FROM timescaledb_information.chunks
 WHERE hypertable_name = 'chat_messages'
 ORDER BY range_start
 LIMIT 5;
