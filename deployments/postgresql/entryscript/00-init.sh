#!/bin/bash
# ============================================================
# 00-init.sh — PostgreSQL 首次初始化入口 (docker-entrypoint-initdb.d)
# 仅数据卷为空时由官方 entrypoint 执行一次; 幂等可重跑
# 顺序: 各业务表 → 插件 → 依赖插件的业务索引/时序配置
# service/manager/seed_admin.sql 不在此执行 (需 -v admin_pass 密码变量)
# ============================================================
set -e

echo "[init] service/auth/schema_init.sql"
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f /sql/service/auth/schema_init.sql

echo "[init] service/markdown/schema_init.sql"
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f /sql/service/markdown/schema_init.sql

echo "[init] service/chat/schema_init.sql"
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f /sql/service/chat/schema_init.sql

echo "[init] service/manager/schema_init.sql"
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f /sql/service/manager/schema_init.sql

echo "[init] plugin/pg_search_setup.sql"
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f /sql/plugin/pg_search_setup.sql

echo "[init] plugin/timescaledb_setup.sql"
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f /sql/plugin/timescaledb_setup.sql

echo "[init] service/markdown/search_setup.sql (BM25 index)"
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f /sql/service/markdown/search_setup.sql

echo "[init] service/chat/timescaledb_setup.sql (hypertable)"
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f /sql/service/chat/timescaledb_setup.sql

echo "[init] done"
