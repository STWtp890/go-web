#!/usr/bin/env bash
# Deterministic initialization for a disposable development database.
set -Eeuo pipefail

psql_args=(-X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB")

apply_sql() {
  local script="$1"
  echo "[init] apply ${script}"
  psql "${psql_args[@]}" -f "$script"
}

apply_sql /database/sql/plugin/pg_search_setup.sql
apply_sql /database/sql/plugin/timescaledb_setup.sql
apply_sql /database/sql/service/auth/schema_init.sql
apply_sql /database/sql/service/manager/schema_init.sql
apply_sql /database/sql/service/chat/schema_init.sql
apply_sql /database/sql/service/chat/timescaledb_setup.sql
apply_sql /database/sql/service/document/schema_init.sql
apply_sql /database/sql/service/document/search_setup.sql
apply_sql /database/sql/plugin/bm25_only_verify.sql

echo "[init] done"