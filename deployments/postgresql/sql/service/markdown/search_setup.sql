-- service/markdown/search_setup.sql — Markdown 全文检索业务索引
-- 前置：已执行 service/markdown/schema_init.sql 与 plugin/pg_search_setup.sql。

CREATE INDEX IF NOT EXISTS idx_markdowns_paradedb
    ON markdowns USING bm25 (markdown_id, (search_text::pdb.jieba))
    WITH (key_field = 'markdown_id');

-- 存量数据回填示例：
-- UPDATE markdowns SET search_text = title || ' ' || summary || ' ' ||
--     (SELECT content FROM markdown_contents WHERE markdown_id = markdowns.markdown_id);
