# SQL 目录约定

- `plugin/`：PostgreSQL 扩展安装与目标态验证，不包含业务数据。
- `service/auth/`：用户认证表。
- `service/manager/`：管理员与审批表；`seed_admin.sql` 仅用于本地引导和测试。
- `service/chat/`：Chat 保留表与 TimescaleDB 配置；当前服务未注册。
- `service/document/`：文档事实表、版本、访问策略、查询投影和 BM25 索引。

全新数据卷只通过 `../entryscript/00-init.sh` 按固定顺序初始化。仓库不维护 `schema_migrations`、manifest、升级 runner、反向导出、备份恢复或旧 Markdown schema；结构变化时直接重建开发数据卷。

Documents 搜索边界固定为 ParadeDB `pg_search` + BM25/jieba，索引载体为 `document_search_projection`。`plugin/bm25_only_verify.sql` 同时验证目标索引存在、业务 schema 不含 vector 列/索引，并拒绝 `schema_migrations`、旧 Markdown 表/索引以及未接入的 Outbox 状态表重新出现。pg_search 自动安装的 pgvector 依赖不代表 go-web 提供向量检索能力。