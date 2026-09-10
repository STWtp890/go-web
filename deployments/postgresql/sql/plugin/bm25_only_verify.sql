-- plugin/bm25_only_verify.sql — go-web Markdown 搜索边界验收
--
-- go-web 仅使用 pg_search 的 BM25/jieba 全文检索。pg_search 上游虽然依赖
-- pgvector，但业务 schema 不允许出现 vector 列、向量索引或向量检索结构。

DO $$
DECLARE
    vector_columns text;
    vector_indexes text;
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM pg_extension
         WHERE extname = 'pg_search'
    ) THEN
        RAISE EXCEPTION 'BM25 boundary violation: pg_search extension is missing';
    END IF;

    IF NOT EXISTS (
        SELECT 1
          FROM pg_index AS index_state
          JOIN pg_class AS index_relation
            ON index_relation.oid = index_state.indexrelid
          JOIN pg_class AS table_relation
            ON table_relation.oid = index_state.indrelid
          JOIN pg_namespace AS table_namespace
            ON table_namespace.oid = table_relation.relnamespace
         WHERE table_namespace.nspname = 'public'
           AND table_relation.relname = 'markdowns'
           AND index_relation.relname = 'idx_markdowns_paradedb'
           AND index_state.indisvalid
           AND index_state.indisready
    ) THEN
        RAISE EXCEPTION 'BM25 boundary violation: idx_markdowns_paradedb is missing or invalid';
    END IF;

    SELECT string_agg(
               format('%I.%I.%I', table_schema, table_name, column_name),
               ', ' ORDER BY table_name, ordinal_position
           )
      INTO vector_columns
      FROM information_schema.columns
     WHERE table_schema = 'public'
       AND udt_name = 'vector';

    IF vector_columns IS NOT NULL THEN
        RAISE EXCEPTION 'BM25 boundary violation: vector columns found: %', vector_columns;
    END IF;

    SELECT string_agg(indexname, ', ' ORDER BY indexname)
      INTO vector_indexes
      FROM pg_indexes
     WHERE schemaname = 'public'
       AND (
           indexdef ILIKE '%::vector%'
           OR indexdef ~* 'vector_(l2|ip|cosine)_ops'
       );

    IF vector_indexes IS NOT NULL THEN
        RAISE EXCEPTION 'BM25 boundary violation: vector indexes found: %', vector_indexes;
    END IF;
END
$$;

SELECT 'BM25_ONLY_OK' AS verification,
       extension_state.extversion AS pg_search_version,
       index_state.indexdef AS bm25_index
  FROM pg_extension AS extension_state
  JOIN pg_indexes AS index_state
    ON index_state.schemaname = 'public'
   AND index_state.indexname = 'idx_markdowns_paradedb'
 WHERE extension_state.extname = 'pg_search';
