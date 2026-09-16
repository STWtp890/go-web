# SQL 目录约定

- `plugin/`：PostgreSQL 扩展安装与目标态验证，不包含业务数据。
- `service/auth/`：用户认证表。
- `service/manager/`：管理员与审批表；`seed_admin.sql` 仅用于本地引导和测试。
- `service/chat/`：Chat 保留表与 TimescaleDB 配置；当前服务未注册。
- `service/document/`：文档事实表、版本、访问策略、查询投影和 BM25 索引。

全新数据卷只通过 `../entryscript/00-init.sh` 按固定顺序初始化。仓库不维护 `schema_migrations`、manifest、升级 runner、反向导出、备份恢复或旧 Markdown schema；结构变化时直接重建开发数据卷。

Documents 正式搜索边界固定为 ParadeDB `pg_search` + BM25/jieba，索引载体为 `document_search_projection`。P2.3 新增的 `document_index_delivery_events` 和 `document_index_rebuild_runs` 只负责向 mixin-search 可靠传播已提交事实，不参与正式查询，也不复制正文。P2.5 的 `document_search_shadow_observations` 只保存查询 SHA-256、结果差异、延迟、错误分类和事实复核计数，不保存查询正文。`plugin/bm25_only_verify.sql` 同时验证 BM25 索引、P2.3 投递表、不含正文的 Outbox、P2.5 观测表不含查询正文列以及业务 schema 不含 vector 列/索引，并继续拒绝 `schema_migrations`、旧 Markdown 表/索引和旧 `index_outbox` 重新出现。`plugin/cache_revision_verify.sql` 验证 User/Manager 的 `cache_revision` 字段、触发器和同秒连续更新的单调性。pg_search 自动安装的 pgvector 依赖不代表 go-web 在 PostgreSQL 内提供向量检索能力。
