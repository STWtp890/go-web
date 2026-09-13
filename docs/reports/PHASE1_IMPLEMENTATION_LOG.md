# 阶段 1 实施日志

> 状态：已完成
> 启动日期：2026-09-11
> 完成日期：2026-09-13
> 已完成实施包：P1.0-P1.5
> 后续状态：阶段 1 已关闭；新工作需建立新的阶段计划
> 计划来源：[`CURRENT_IMPLEMENTATION_PLAN.md`](../planning/CURRENT_IMPLEMENTATION_PLAN.md)
> 文档职责：只记录完成事实与实测证据，不维护后续实施细节

## 阶段 1 项目结构基线

- 后端业务代码由全局 `internal/service` 迁入 `internal/modules/<domain>`，按 document、auth、manager、markdown、chat、aiagent 领域纵向归组。
- document 领域进一步拆分为 `domain`、`application`、`infrastructure/postgresql`；P1.3 的 HTTP 适配器将落入同领域的 `interfaces/http`。
- `internal/app` 保持唯一组合根，业务模块禁止反向依赖；document 的领域层和应用层禁止依赖 Gin、GORM 或旧 Markdown 存储。
- 新增 `internal/architecture/dependencies_test.go`，将包依赖方向、废弃路径和旧存储隔离变成可执行门禁。
- auth、manager、markdown、chat 仍保留原有内部子层；`common`、`model` 仍是过渡兼容层，后续随具体领域切换渐进收口。
- 结构规则及 P1.3 目标落点记录在 `docs/architecture/PROJECT_STRUCTURE.md`。本次目录迁移不启用 Chat/WebSocket，也不提前接入 mixin-search RPC。
结构基线实测结果：

- 废弃 Go 导入路径数量为 0，旧 `internal/service` 目录已退出仓库结构；
- `go test ./...` 与 `go vet ./...` 通过，新增 architecture 门禁通过；
- `verify-p1.2.ps1` 再次输出 `P1.2=PASS`，事务、并发修订和失败回滚语义保持不变；
- `deployments/verify.ps1` 完成后端与前端构建、三个 Go module 测试、Vue 类型检查、PostgreSQL 迁移、Compose 部署和 HTTP 健康验收；
- 验收容器已停止，镜像与数据卷保留供后续阶段复用。

## P1.0 已实现

- 新增统一迁移执行器 deployments/postgresql/migrations/run.sh。
- 新增 schema_migrations(version, name, checksum, applied_at) 账本。
- 使用 PostgreSQL session advisory lock 拒绝并发迁移。
- 每个 manifest 版本在独立事务中执行。
- checksum 由版本、名称、文件路径和所有 SQL 文件内容共同计算。
- 已执行迁移发生内容漂移时，runner 抛出数据库异常并非零退出。
- 空库 00-init.sh 与已有数据库升级共用同一 runner。
- 根 Compose 新增 tools profile 下的一次性 postgres-migrate 服务；无端口、只读根文件系统、临时 /tmp、移除 Linux capabilities。
- 总体验证顺序调整为 PostgreSQL 启动、迁移、应用栈启动。
- 新增旧 Markdown 数据前置审计、逻辑备份入口和反向导出骨架。
- 新增隔离 roundtrip Compose 与可重复验收脚本。

## P1.0 实测结果

执行 deployments/postgresql/migrations/verify-roundtrip.ps1，结果：

- 空库自动建立 6 项不依赖扩展的 core migration 账本（含 P1.1 领域 schema）；
- 第二次执行全部报告 already applied；
- 10 项旧 Markdown 数据审计全部为 0；
- 注入孤立正文后，preflight 报告 orphan_content=1 并非零退出；删除测试行后恢复通过；
- pg_dump/pg_restore 前后均为 users=1、markdowns=1、contents=1；
- 前后 document_hash 均为 1c972e061f261482188824639767c73c；
- 篡改 0002_auth_baseline checksum 后迁移器非零退出；
- 60 秒持有 advisory lock 时，并发迁移器非零退出；
- 自动化验收输出 IDEMPOTENCY=PASS、CHECKSUM_DRIFT=PASS、ROUNDTRIP=PASS；
- 自动化和手工演练产生的临时容器、网络与数据卷均已删除。

## 演练中发现并修复

1. 隔离 Compose 最初没有挂载 docker-entrypoint-initdb.d，导致空库未调用迁移器。
2. Bash strict mode 的默认环境变量展开被错误转义。
3. manifest 最后一行无换行时会被 read 循环漏掉。
4. psql 的 quit 命令不支持预期的自定义退出码，已改为数据库异常。
5. 动态 here-doc 中的 dollar quote 会被 shell 展开为 PID，已显式转义。
6. Windows 逐行写回会产生 CRLF，已增加 .gitattributes 固定 shell 和 SQL 为 LF。
7. preflight 临时表使用 ON COMMIT DROP 时会在自动提交后过早删除，已改为会话级临时表。

## P1.1 已实现

- 新增 0009_document_domain_schema，建立 knowledge_spaces、space_members、documents、document_versions、document_access_policies、document_grants、document_search_projection、index_outbox、document_index_states。
- 用延迟约束触发器保证私人空间只能包含活动 owner、空间始终存在活动 owner，活动版本必须属于当前文档且已发布。
- 用唯一约束阻止重复版本修订，用不可变触发器阻止历史标题、摘要、正文、格式和哈希被覆盖。
- 新增与 GORM 解耦的 internal/modules/document/domain 领域类型及 Repository 接口。
- 新增 internal/modules/document/infrastructure/postgresql GORM 映射和仓储实现；DDL 是唯一 schema 来源，Repository 不调用 AutoMigrate。
- Repository 已覆盖显式事务、文档行锁、版本发布、活动版本切换、访问策略/搜索投影/索引状态 upsert、授权和 Outbox 写入。
- 新增 verify-p1.1.ps1，使用随机 Compose project、随机本机端口和独立数据卷运行真实 PostgreSQL 集成测试并自动清理。

P1.1 集成验收结果：

- MIGRATION_COUNT=6；
- ORM_MAPPING=PASS；
- DATABASE_CONSTRAINTS=PASS；
- TRANSACTION_ROLLBACK=PASS；
- P1.1=PASS；
- gin-backend 执行 go test ./... 全部通过。

## P1.2 已实现

- 新增 internal/modules/document/application CommandService，以独立应用层编排 Create、Update、Trash，不依赖 HTTP、GORM 或检索传输层。
- Repository 新增私有空间幂等复用、访问/生命周期修订推进、搜索投影删除、索引期望状态推进和当前策略/最新版本读取原语。
- Create 在单事务内建立私有空间成员、文档、首个不可变版本、活动指针、访问策略、搜索投影、索引状态和 pending Outbox。
- Update 使用文档行锁串行化并发写入；创建新版本、废止旧版本、切换活动指针，并分别推进 activation_revision、access_revision 和逐事件 aggregate_revision。
- Trash 保留所有版本正文，推进 lifecycle_revision、删除搜索投影、标记索引删除期望并写入 document_trashed Outbox。
- 阶段 1 仍只积压 Outbox，不调用 mixin-search RPC；旧 Markdown HTTP 写路径不切换、不双写。
- 新增 verify-p1.2.ps1，并将 P1.2 隔离 PostgreSQL 门禁接入 deployments/verify.ps1。

P1.2 集成验收结果：

- MIGRATION_COUNT=6；
- CREATE_UPDATE_TRASH=PASS；
- CONCURRENT_REVISION_ALLOCATION=PASS；
- OUTBOX_ORDERING=PASS；
- FAILURE_ROLLBACK=PASS；
- 同一 owner 的多个文档复用唯一私有空间；
- Create、Update、Trash 在末步骤 Outbox 冲突时均完整回滚；
- go test ./... 与 go vet ./... 全部通过；
- 纳入 P1.2 门禁后的 deployments/verify.ps1 完整构建、部署和 HTTP 健康验收通过；
- P1.2=PASS。
## P1.0 镜像构建门禁

新增 deployments/postgresql/verify-image.ps1，一次性执行：

1. 使用根 Compose 构建 gin-postgres:local；
2. 校验 timescaledb、pg_search、vector control 文件；
3. 在隔离数据卷执行完整 9 项 manifest；
4. 校验三个扩展、idx_markdowns_paradedb、chat_messages hypertable；
5. 执行 bm25_only_verify.sql，确认业务 schema 没有 vector 列或向量索引。
实测结果：

- IMAGE_ID=sha256:b5ccf02270dc8a4dd8103779016d3b904645943ae43be9112192f4ab286cd1fb；
- MIGRATION_COUNT=9；
- EXTENSIONS=timescaledb,pg_search,vector；
- BM25_ONLY=PASS；
- CHAT_HYPERTABLE=PASS；
- P1.0_IMAGE=PASS。

镜像改为显式固定 TimescaleDB 2.30.0、pgvector 0.8.6 和 pg_search 0.25.2。pg_search 使用本地制品并在安装前校验 SHA-256，避免 GitHub Release 成为构建时单点；UseCachedBase 允许在可信基础镜像已缓存时绕过不可用的 registry mirror。

## 完整应用栈部署复测

执行 deployments/verify.ps1 后，以下步骤通过：Compose 配置、mixin-search/v1 生成一致性、9 项迁移清单、gin-backend 全量 Go 测试、mixin-search 全量 Go 测试、Vue/TypeScript 检查、前端生产构建、PostgreSQL 镜像缓存重建、PostgreSQL 健康检查及版本化迁移。

Go 基线已统一升级至 1.26.8：go.work、gin-backend、mixin-search、packages/gen 与后端 Docker builder 使用同一补丁版本。本地 golang:1.26.8-alpine 镜像 ID 为 sha256:ce864e7223ac17b1775e6fd0b4c0db580c2eb50e7953a427916379e4b92a1628，容器内 go version 返回 go1.26.8 linux/amd64。

后端 Docker 构建通过可覆盖的 GOPROXY 参数适配受限网络；使用 GOPROXY=https://goproxy.cn,direct 完成 gin-backend:local 构建，镜像 ID 为 sha256:608ee4f7918a7e45cc58f65210b3697e252669176195dd2b41fdb04da57ead6d。三个 Go 模块测试全部通过；启动 PostgreSQL、Redis、执行 9 项迁移后，gin-backend 容器健康，/healthz 与 /readyz 均返回 HTTP 200。

前端 Docker builder 已升级为 node:24.21.0-alpine，容器内版本为 Node.js v24.21.0、npm 11.19.0。补充 .dockerignore 后，前端构建上下文由约 98.7 MB 降至 2.63 KB，Vue/TypeScript 检查与 Vite 生产构建通过。

deployments/verify.ps1 已完成全栈复测：三个应用镜像构建成功，PostgreSQL、Redis、gin-backend、simple-frontend 全部健康；BM25_ONLY_OK，前端入口、经 Nginx 转发的 /healthz 与 /readyz 均返回 HTTP 200。Go 与 Node builder 镜像阻塞均已解除。

## P1.3 已实现

- 新增 document QueryService 与 QueryRepository/QueryCache 端口，覆盖详情、我的列表、公开列表和 PostgreSQL BM25 搜索。
- 新增 PostgreSQL 查询仓储，所有运行时读取均来自 documents、document_versions、document_access_policies 与 document_search_projection。
- 新增版本化文档缓存，键包含 document_id、active_version_id、activation_revision、access_revision 与 lifecycle_revision；事务提交后的修订推进会使旧快照自然失效。
- 新增 Gin HTTP 兼容适配器，保持 /api/v1/protected/markdown/* 路径、字段、状态码与 public/private 语义；JWT subject 显式解析为 users.id。
- 写入端一次切换到 P1.2 CommandService，查询端一次切换到新文档内核，不保留新旧表双写或运行时回读。
- 删除旧 modules/markdown、model/orm/markdown、model/store/markdown 与 model/cache/markdown 运行实现；Chat 使用独立 ServiceChat 且继续不注册。
- 新增 0010_document_projection_bm25，为 document_search_projection 建立 pg_search BM25/jieba 索引；旧 idx_markdowns_paradedb 仅保留作 P1.6 回滚窗口资产。
- 新增 verify-p1.3.ps1，并接入 deployments/verify.ps1。

P1.3 实测结果：

- MIGRATION_COUNT=10；
- QUERY_DETAIL_AND_PERMISSIONS=PASS；
- QUERY_LISTS=PASS；
- BM25_PROJECTION_SEARCH=PASS；
- VERSIONED_DOCUMENT_CACHE=PASS；
- MARKDOWN_HTTP_ADAPTER=PASS；
- 运行时完整 API 回归为 passed=92、failed=0、total=92，含缓存新鲜度、权限矩阵、BM25 更新/删除可见性和 Chat/WS 404；
- go test ./...、go vet ./... 与 deployments/verify.ps1 全部通过；
- P1.3=PASS。

## Gin 项目结构快速优化

- 将 Gin 引擎、健康/就绪探针、全局 middleware 与路由组装配从 `internal/app` 迁入 `internal/platform/httpserver`。
- `internal/app` 只保留配置加载、运行时依赖装配、启动与关闭生命周期；readiness 的基础设施检查由组合根注入 HTTP 层。
- 全局 middleware 迁入 `internal/platform/httpserver/middleware`，业务模块继续位于 `internal/modules/<domain>`。
- auth、manager、document 和保留的 chat 模块统一使用 `RegisterRoutes` 命名；Chat 仍不由主 Router 注册。
- 删除已确认为空的 `internal/application`、`internal/domain`、`internal/infra` 遗留目录。
- architecture 门禁新增 HTTP 平台目录存在性及旧 `internal/middleware`、`internal/app/routes.go` 路径禁止检查。
- 完整 `deployments/verify.ps1` 通过，重新构建的 gin-backend 镜像健康；运行时 API 回归为 92 passed、0 failed。
- 外部 HTTP 路径、鉴权/CSRF 中间件顺序、数据库 schema 和 RPC 契约均保持不变。

## P1.3 后置清理（非破坏性）

- 移除根目录与 gin-backend 中对 `*.sum` 的错误忽略，使 `go.work.sum` 及三个 Go module 的 `go.sum` 进入版本管理候选，保证新检出环境能够校验依赖内容。
- architecture 门禁新增 `interfaces` 层存在性与依赖方向检查，并明确阻止已删除的 `internal/service` 目录回流。
- 同步 Chat/TimescaleDB 保留文档到独立 `ServiceChat` 与版本化迁移现状；Chat 仍不注册。
- 整理仅涉及版本管理规则、文档、命名和测试门禁，不改变数据库、HTTP/RPC 契约或运行时业务语义。

## P1.4 实施前核对（历史）

- 阶段 0 与 P1.0-P1.3 已通过当时门禁；本日志保留这些历史实测事实。
- P1.3 已停止运行时读写旧 Markdown 表和缓存，旧 Go 运行实现已删除；当前仍存在的旧数据库与 HTTP 兼容资产尚未清理。
- 2026-09-12 接受 ADR-005，后续不再把版本升级、备份恢复、反向导出、回滚演练和旧接口兼容作为阶段 1 目标。
- Chat/WebSocket 继续保留代码但不注册；阶段 1 不调用 mixin-search RPC。

## P1.4 实施入口（历史）

P1.4 的范围、非目标、实施流程和验收门禁由 [`CURRENT_IMPLEMENTATION_PLAN.md`](../planning/CURRENT_IMPLEMENTATION_PLAN.md) 维护；该实施包完成后已在本日志追加最终事实与实测证据。

## P1.4 已实现

- PostgreSQL 改为单一空库初始化入口：依次创建 pg_search/TimescaleDB、auth、manager、保留的 Chat schema、document 领域表和 BM25 投影。
- 删除版本化迁移目录、manifest/runner、升级预检、备份/反向导出/往返恢复脚本、`postgres-migrate` Compose 服务与阶段迁移入口。
- 删除旧 `markdowns`、`markdown_contents`、`idx_markdowns_paradedb` 初始化资产；删除无消费者的 `index_outbox`、`document_index_states` 及 CommandService/Repository 写入。
- HTTP 直接切换为 `/api/v1/protected/documents*`，JSON 使用 `documentId`、`ownerId`、`documentList`；前端 API、类型、路由和详情视图同步切换，不提供旧路径兼容。
- Chat/WebSocket 源码和数据库表继续保留，但组合根不注册服务或路由；mixin-search 仍不进入根 Compose，不产生正式 RPC 流量。
- 根验证脚本合并为一个随机命名的临时 Compose 环境，从空卷完成构建、schema、Go、Vue、HTTP 验证并默认自动删除容器和测试卷。
- 修复文档仓储集成测试的全表计数断言，使 Go 并行包测试按当前 document_id 隔离，不再与 CommandService 集成样本互相污染。

P1.4 实测结果：

- `verify-image.ps1 -UseCachedBase`：`SCHEMA_BASELINE=PASS`、`BM25_ONLY=PASS`、三扩展存在、Chat hypertable 存在、旧关系数为 0；镜像 ID 为 `sha256:d21b5f991faeb090b3325600a0b6d1f9091b79ed90016d53d219a7c1e88d6b5f`。
- `deployments/verify.ps1 -SkipImageBuild`：三个 Go module 的 `go test ./...` 与 `go vet ./...` 通过，Vue `npm run build` 通过，四个 Compose 服务 healthy。
- 运行时 API 回归：`95 passed / 0 failed / 95 total`，覆盖认证、Documents、权限、BM25 更新/回收、管理员、Nginx 代理、5 项 Chat 路由 404 和 3 项旧 Markdown 路由 404。
- 结果报告：`deployments/test-results/full-api-p14_20260912_215904.json` 与 `deployments/test-results/full-api-p14_20260912_215904.md`。
- 验证结束后临时容器、网络与数据卷已自动删除。
- `P1.4_BUILD_TEST_DEPLOYMENT=PASS`，P1.4 完成；该时点转入 P1.5 mixin-search/v1 契约定型。

## P1.5 已实现

- `mixin-search/v1` 增加 `UpdateDocumentAccess`，RPC 总数固定为 7；协议统一使用不可变的 `owner_space_id`，并完整返回 activation、access、lifecycle 三类 fencing revision。
- 访问策略更新使用完整快照：`authenticated_public` 与规范化后的 `granted_space_ids`；搜索授权采用“公开、允许空间交集、显式 `allowed_document_ids`”三者 OR 语义。
- 内存核心保留 `operation_id` 载荷/首次响应账本、文档与版本自然键，以及三类修订号的高水位；同一操作精确重放，同 ID 改绑会冲突，索引、激活、访问、删除分别拒绝过期事件。
- 删除未知文档也建立 tombstone；更高 lifecycle 与 activation 修订可重新发布，同一历史删除请求重放不会再次删除已重新发布状态。
- gRPC 适配器、演示客户端、契约文档和测试同步更新；正式 gin-backend 写入/查询链路仍不调用 mixin-search RPC，也没有引入 Outbox 消费、重试或重放。
- 前端阶段边界门禁纳入 `npm run build`，阻止 Chat 路由、组件或 API 重新进入构建；概览页残留的 Chat 请求和入口已移除。
- 合并验证脚本使用随机 Compose project，输出 P1.5 标识，并默认销毁临时容器、网络和数据卷。

P1.5 实测结果（2026-09-13）：

- Proto 生成一致性检查通过，7 个 RPC 及生成文件无漂移。
- gin-backend、mixin-search、packages/gen 三个 Go module 的 `go test ./...` 与 `go vet ./...` 全部通过。
- 前端 `npm run build` 通过：`PHASE_BOUNDARY=PASS`，`vue-tsc` 通过，Vite 转换 1862 个模块并完成生产构建。
- 根 Compose 与 mixin-search Compose 配置检查通过；从空卷构建并启动 PostgreSQL、Redis、gin-backend、simple-frontend，四个服务全部 healthy。
- PostgreSQL 基线输出 `BM25_ONLY_OK`，pg_search 版本为 0.25.2；前端入口和经 Nginx 转发的存活、就绪探针均返回 HTTP 200。
- 运行时 API 回归为 `95 passed / 0 failed / 95 total`；最终结果报告为 `deployments/test-results/full-api-p15_20260913_160226.json` 与 `deployments/test-results/full-api-p15_20260913_160226.md`。
- 验证结束后随机项目 `go-web-p15-d9942a3a400c` 的容器、网络和三个数据卷全部删除。
- 最终标识为 `P1.5_BUILD_TEST_DEPLOYMENT=PASS`；P1.5 与 go-web 阶段 1 完成。

附加的 race 检查未列为 P1.5 必需门禁：当前主机默认禁用 CGO，显式启用后又因缺少 `gcc` 无法启动 race 构建；常规测试、并发语义测试和全部既定门禁均已通过。
