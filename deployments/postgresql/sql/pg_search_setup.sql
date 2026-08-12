-- ============================================================
-- pg_search_setup.sql — ParadeDB pg_search 扩展初始化
--
-- 适用: gin-backend 的 markdown 业务 (默认库 gin_demo)
-- 前置: 推荐直接使用官方镜像 paradedb/paradedb (已内置 pg_search)
--       自建 PostgreSQL 则需按 https://docs.paradedb.com/deploy/self-hosted/extension
--       编译安装 pg_search 扩展后, 以超级用户执行本脚本:
--       psql -U postgres -d gin_demo -f deployments/postgresql/sql/pg_search_setup.sql
-- 说明: 脚本可重复执行 (幂等); BM25 索引由迁移工具 utils/automigrate -index 幂等创建,
--       本脚本亦附索引 DDL 供参考
-- ============================================================

-- 1. 安装 pg_search 扩展 (已安装则跳过)
CREATE EXTENSION IF NOT EXISTS pg_search;

-- 2. 验证 jieba 中文分词 (应输出 {中文,全文,检索,测试} 等分词结果)
SELECT '中文全文检索测试'::pdb.jieba::text[];

-- 3. (参考) markdowns 表的 BM25 索引 DDL, 应用启动时自动执行等价语句:
--    key_field 必须唯一 (markdown_id), 且为索引列列表首列;
--    search_text = 标题 + 摘要 + 正文, 供全文检索; 中文用 jieba 分词
CREATE INDEX IF NOT EXISTS idx_markdowns_paradedb
    ON markdowns USING bm25 (markdown_id, (search_text::pdb.jieba))
    WITH (key_field = 'markdown_id');

-- 4. (参考) 存量数据回填 search_text (新增列后历史文章需执行):
--    UPDATE markdowns
--       SET search_text = title || ' ' || summary || ' ' ||
--           (SELECT content FROM markdown_contents
--             WHERE markdown_id = markdowns.markdown_id);

-- 5. (参考) 搜索验证:
--    SELECT markdown_id, pdb.score(markdown_id)
--      FROM markdowns
--     WHERE search_text ||| '全文检索'
--     ORDER BY pdb.score(markdown_id) DESC;
