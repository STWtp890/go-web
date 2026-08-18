-- ============================================================
-- plugin/pg_search_setup.sql — ParadeDB pg_search 扩展初始化
--
-- 适用: gin-backend 的 markdown 业务 (默认库 gin_demo)
-- 前置: 推荐使用部署镜像 deployments/postgresql/Dockerfile (内置 pg_search + pgvector)
--       或自装扩展: 按 https://docs.paradedb.com/deploy/self-hosted/extension
--       下载 GitHub Releases 预编译 .deb 安装后, 以超级用户执行本脚本:
--       psql -U postgres -d gin_demo -f deployments/postgresql/sql/plugin/pg_search_setup.sql
-- 说明: 仅安装和验证插件；Markdown 的 BM25 业务索引见
--       deployments/postgresql/sql/service/markdown/search_setup.sql。
-- ============================================================

-- 1. 安装 pg_search 扩展 (已安装则跳过)
--    pg_search 0.25+ 依赖 pgvector (vector 类型), CASCADE 自动创建 vector 扩展
CREATE EXTENSION IF NOT EXISTS pg_search CASCADE;

-- 2. 验证 jieba 中文分词 (应输出 {中文,全文,检索,测试} 等分词结果)
SELECT '中文全文检索测试'::pdb.jieba::text[];

