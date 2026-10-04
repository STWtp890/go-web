# SQL 目录约定

- `plugin/`：PostgreSQL 扩展安装与目标态验证，不包含业务数据。
- `service/auth/`：用户认证表。
- `service/manager/`：管理员与审批表；`seed_admin.sql` 仅用于本地引导和测试。
- `service/chat/`：Chat 保留表与 TimescaleDB 配置；当前服务未注册。
- `service/document/`：**迁移期** go-web 文档事实表、版本、访问策略、查询投影和 BM25 索引；随 ADR-017 步骤 8 删除。
- `service/service_roles.sql`：三个来源专属服务的数据库账号与跨 schema 授权；`service/verify_service_isolation.sql` 是它的可执行验证。

ADR-017 之后，`document_service`、`document_search`、`qq_search` 三个 schema 的基线**不再放在本目录**：各自保存在所属服务的 `apps/<service>/schema/schema_init.sql`，由 `../entryscript/00-init.sh` 按固定顺序应用（Compose 把每个 module 的 `schema/` 目录只读挂载为 `/service-schema/<service>`）。这样服务拥有自己的存储定义，本目录只保留共享的账号与授权。

全新数据卷只通过 `../entryscript/00-init.sh` 按固定顺序初始化。仓库不维护 `schema_migrations`、manifest、升级 runner、反向导出、备份恢复或旧 Markdown schema；结构变化时直接重建开发数据卷。

正式文档的现有搜索边界仍是 ParadeDB `pg_search` + BM25/jieba，索引载体为 `public.document_search_projection`，该路径属于迁移期实现，目标由 `document-search` 服务承担。`document_service.document_events` 是文档服务的事务 Outbox，只保存序列化事件、不含正文副本，并由触发器保证只追加。`plugin/bm25_only_verify.sql` 现在验证：pg_search 存在、任何业务 schema 都不含 vector 列/向量索引、旧 Markdown 关系不复活、三个服务 schema 存在、且服务 schema 不引用其他服务的用户表。写入权限与 schema 隔离由 `../verify-service-isolation.ps1` 以服务角色实测；`plugin/cache_revision_verify.sql` 验证 User/Manager 的 `cache_revision` 字段、触发器和同秒连续更新的单调性。pg_search 自动安装的 pgvector 依赖不代表 go-web 在 PostgreSQL 内提供向量检索能力。
