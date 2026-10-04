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
apply_sql /database/sql/plugin/trigram_setup.sql
apply_sql /database/sql/service/auth/schema_init.sql
apply_sql /database/sql/service/manager/schema_init.sql
apply_sql /database/sql/service/chat/schema_init.sql
apply_sql /database/sql/service/chat/timescaledb_setup.sql

# The legacy public.* document baseline (knowledge_spaces, documents,
# document_versions, the search projection, its delivery ledger, the shadow
# observations and the QQ binding tables) is gone: ADR-017 moved those objects
# into the source-owned service schemas below, and bm25_only_verify.sql now
# asserts their absence.

# Source-owned document and search services. Each schema baseline lives inside
# its own module (mounted at /service-schema/<service>) because the service owns
# its storage definition; this bootstrap only applies them in order and then
# hands out the accounts and cross-schema grants.
apply_sql /service-schema/document-service/schema_init.sql
apply_sql /service-schema/document-search/schema_init.sql
apply_sql /service-schema/qq-search/schema_init.sql
apply_sql /database/sql/service/service_roles.sql

apply_sql /database/sql/plugin/bm25_only_verify.sql
apply_sql /database/sql/plugin/cache_revision_verify.sql

echo "[init] done"
