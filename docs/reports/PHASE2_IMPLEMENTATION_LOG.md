# 阶段 2 实施日志

> 状态：已完成
> 启动日期：2026-09-13
> 当前实施包：无；阶段 2 已收口
> 已完成实施包：P2.0、P2.1、P2.2、P2.3、P2.4、P2.5
> 阶段起点提交：`a8ab94a7bd408ef49bbb85394dca3258dc653119`
> 计划来源：[`CURRENT_IMPLEMENTATION_PLAN.md`](../planning/CURRENT_IMPLEMENTATION_PLAN.md)
> 文档职责：只记录阶段 2 已完成事实、实测证据和当前有效限制，不提前宣告后续实施包完成

## P2.0 已完成

P2.0 已于 2026-09-13 完成。阶段 1 的目录迁移、document 领域改造、数据库基线与 `mixin-search/v1` 契约已经冻结为可独立审查和恢复的阶段 2 起点；当前分支中的起点提交为 `a8ab94a7bd408ef49bbb85394dca3258dc653119`。

P2.0 实测结果：

- `packages/proto/verify-generated.ps1` 通过，Proto 生成文件与七个 `mixin-search/v1` RPC 契约一致；
- gin-backend、mixin-search、packages/gen 三个 Go module 的 `go test ./...` 与 `go vet ./...` 全部通过；
- 前端 `npm run build` 通过，输出 `PHASE_BOUNDARY=PASS`，`vue-tsc` 通过，Vite 转换 1862 个模块并完成生产构建；
- 使用一次性、空数据卷的 Compose 环境启动 PostgreSQL、Redis、gin-backend、simple-frontend，四个服务全部达到 healthy；
- PostgreSQL 基线检查输出 `BM25_ONLY_OK`，正式搜索仍由 PostgreSQL BM25 提供；
- 运行时 API 回归为 `95 passed / 0 failed / 95 total`，同时确认五项 Chat 路由和三项旧 Markdown 路由保持 HTTP 404；
- 本次回归报告为 [`full-api-p15_20260913_193709.json`](../../deployments/test-results/full-api-p15_20260913_193709.json) 与 [`full-api-p15_20260913_193709.md`](../../deployments/test-results/full-api-p15_20260913_193709.md)。

## P2.1 已完成

P2.1 已于 2026-09-13 完成。mixin-search 现在默认使用独立 PostgreSQL 控制存储，服务实例恢复后仍可正确处理幂等、乱序、活动版本、访问快照和删除墓碑；memory 控制存储仅保留给单元测试和显式本地演示。

已完成实现：

- 建立与 Protobuf DTO 分离的 `ControlStore` 和完整持久化模型，保存文档清单、版本映射与指纹、三类修订高水位、访问快照、墓碑、删除结果和全部写 RPC 的 `operation_id` 首次响应；
- PostgreSQL 使用 `mixin_search_control.control_states`，按 namespace 隔离控制快照，并以 `generation` compare-and-swap 阻止多实例覆盖写入；
- 索引采用持久化 `pending_vector_write` 租约、向量写入、最终控制提交的两阶段顺序；到期且没有活动映射的模糊向量写入会在后续请求刷新时清理；
- 版本删除和文档删除先持久化逻辑删除、活动状态/墓碑、首次响应和 pending delete，再执行物理向量清理；清理失败只留下不可检索、可重试的物理孤儿；
- 每个请求刷新持久化 generation；控制 generation CAS 冲突映射为 `ABORTED`，业务 CAS/fencing 冲突保持 `FAILED_PRECONDITION`，控制存储不可用、generation 回退或损坏快照映射为 `UNAVAILABLE` 并失败关闭；
- 提交顺序和恢复边界已固化在 [ADR-006](../adr/006-mixin-search-control-state-commit-order.md)。

P2.1 实测结果：

- 在 `apps/mixin-search` 执行 `go test ./...` 通过；单元测试覆盖服务实例恢复、五类写操作首次响应重放、墓碑后的迟到事件、generation CAS、跨实例刷新、控制存储失败关闭、损坏快照拒绝、模糊索引提交清理，以及逻辑删除先于物理清理；
- 在 `apps/mixin-search` 执行 `go vet ./...` 通过；
- 执行 `./verify-control-store.ps1`，脚本使用随机 Compose project、随机本机端口和一次性 PostgreSQL 空数据卷运行真实数据库测试；
- `TestPostgresControlStoreIntegration` 输出 `PASS`，验证 PostgreSQL 重开后的活动版本检索、首次响应重放、陈旧 generation 拒绝、墓碑后的迟到事件拒绝和 namespace 隔离；
- 验收脚本最终输出 `P2.1_CONTROL_STORE=PASS`，并默认清理临时容器、网络和数据卷。

P2.1 的重建边界是：清空或更换控制 namespace/向量集合后，服务端可以接受来自 `go-web` 事实源的确定性、幂等 RPC 重放并重新建立派生状态。gin-backend 的持久化 Outbox、自动重试、对账和全量重建编排属于 P2.3，本实施包没有将其宣告为已完成。

## P2.2 已完成

P2.2 已于 2026-09-14 完成。Qdrant 现在是满足 `mixin-search/v1` 授权与生命周期候选语义的首个持久化向量后端；pgvector 仍是实验性替代，memory 仍只用于测试和显式演示。

已完成实现：

- Qdrant chunk payload 新增并索引 `storage_domain`、`storage_id`、`document_id`、`version_id`、`owner_space_id`、公开标记、授权空间、活动/墓碑状态、三类修订和内容摘要；新写入块默认非活动，控制状态投影完成前不会进入正式候选；
- `SearchDocuments` 在读取持久化控制 generation、收敛 pending 向量操作后，将完整控制投影同步到 Qdrant；dense 与 sparse 查询统一以 `storage_domain + active + not tombstoned` 为必须条件，并以公开、归属/授权空间、显式文档作为至少命中一项的 OR 授权；
- 同一 Qdrant collection 内以 `storage_domain` 隔离不同控制 namespace，避免其他 namespace 的合法公开块污染本次候选集合；
- Qdrant 下推之后仍执行契约层活动版本、墓碑和授权复核；候选不足时按几何增长扩大候选上限，并按公开 chunk ID 稳定去重，避免固定前 N 候选造成可复现的漏召回；
- 删除或模糊写入留下的 pending delete 会投影为墓碑状态并继续物理清理；版本切换、授权撤销、删除和更高 lifecycle 重新发布在下一次正式搜索准入时完成投影并在候选选择前生效；
- 提交与查询门禁已固化在 [ADR-007](../adr/007-qdrant-control-projection-and-filtering.md)。

P2.2 实测结果：

- 在 `apps/mixin-search` 执行 `go test ./... -count=1` 与 `go vet ./...` 通过；单元测试覆盖控制投影、allow-list 规范化、过滤结构、候选回填和契约层二次校验；
- 执行 `./verify-qdrant-control.ps1`，脚本使用随机 Compose project、随机本机 HTTP/gRPC 端口和一次性 Qdrant 空数据卷；容器通过实际 TCP 健康检查；
- `TestQdrantStoreIntegration`、`TestQdrantDocumentControlIntegration`、`TestQdrantStorageDomainIsolationIntegration` 全部输出 `PASS`，覆盖 dense/sparse 基线、空 allow-list 仅公开、owner/granted space、显式文档、授权撤销、非活动版本、版本切换、删除、重新发布、规范化 payload 和共享 collection 隔离；
- 验收脚本最终输出 `P2.2_QDRANT_CONTROL=PASS`，并默认清理临时容器、网络和数据卷；
- P2.2 完成后再次执行 `./verify-control-store.ps1`，`TestPostgresControlStoreIntegration` 通过并输出 `P2.1_CONTROL_STORE=PASS`，临时 PostgreSQL 环境完成清理。
- 从仓库根目录执行 `./deployments/verify.ps1`，Proto 生成一致、前端输出 `PHASE_BOUNDARY=PASS` 并构建 1862 个模块、三个 Go module 的 test/vet 通过、四个根 Compose 服务全部 healthy、数据库输出 `BM25_ONLY_OK`、运行时 API 回归为 `95 passed / 0 failed / 95 total`，最终输出 `P1.5_BUILD_TEST_DEPLOYMENT=PASS` 并清理一次性环境；
- 本次仓库级回归报告为 [`full-api-p15_20260914_020040.json`](../../deployments/test-results/full-api-p15_20260914_020040.json) 与 [`full-api-p15_20260914_020040.md`](../../deployments/test-results/full-api-p15_20260914_020040.md)；
- 本地 Markdown 链接检查覆盖 226 个文件、259 个相对链接，结果为 `MARKDOWN_LINKS=PASS`。

## P2.3 已完成

P2.3 已于 2026-09-14 完成。gin-backend 现在可以在不把远程 RPC 放入文档事务的前提下，把已提交的文档事实可靠传播到 mixin-search；正式 PostgreSQL BM25 查询路径没有改变。

已完成实现：

- 新建 `document_index_delivery_events` 与 `document_index_rebuild_runs`。投递事件只保存不可变版本引用、修订和小型 ACL 快照，不复制正文；事件与文档事实及 BM25 投影在同一事务提交；
- 文档创建、更新和回收分别记录同步或删除意图；事务回滚会同时撤销文档变更与事件，事务内不调用 mixin-search；
- 独立 `document-index-worker` 按单文档顺序领取最早未完成事件，使用租约、fencing、稳定子操作 ID、续租、确定性退避和死信状态处理重试与进程恢复；
- 同步顺序固定为索引不可变版本、更新访问策略、激活版本；模糊超时按完全相同的操作 ID 重试，普通更新不主动删除旧版本；
- `document-index-admin` 提供失败项查看/重放、事实对账和全量重建。全量重建在 repeatable-read 快照内只生成幂等事件，不在快照事务中执行网络调用；
- 提交、重试、重放与重建边界已固化在 [ADR-008](../adr/008-document-index-transactional-outbox.md)。

P2.3 实测结果：

- 在 `apps/gin-backend` 执行 `go test ./...` 通过，覆盖事务事件、整体回滚、调用顺序、租约续期、可重试故障、快照不一致死信以及模糊超时复用操作 ID；
- 执行 `./verify-index-delivery.ps1`，真实 PostgreSQL 验收覆盖同文档顺序、并发领取、租约 fencing、失败退避、死信阻塞、人工重放和过期租约接管，最终输出 `P2.3_INDEX_DELIVERY=PASS`；
- 执行 `./verify-index-rebuild-e2e.ps1`，从全新 gin PostgreSQL、mixin 控制 PostgreSQL 和 Qdrant 重建活动版本/权限/删除状态并验证搜索结果，最终输出 `P2.3_INDEX_REBUILD_E2E=PASS`；
- 两个验收入口均使用随机 Compose project 与一次性资源，并在结束后清理容器、网络、数据卷和临时进程；
- 从仓库根目录执行 `./deployments/verify.ps1`，Proto 生成一致、前端输出 `PHASE_BOUNDARY=PASS` 并构建 1862 个模块、三个 Go module 的 test/vet 通过、四个根 Compose 服务全部 healthy、数据库输出 `BM25_ONLY_OK`、运行时 API 回归为 `95 passed / 0 failed / 95 total`，最终输出 `P1.5_BUILD_TEST_DEPLOYMENT=PASS` 并清理一次性环境；
- 本次 P2.3 仓库级回归报告为 [`full-api-p15_20260914_094448.json`](../../deployments/test-results/full-api-p15_20260914_094448.json) 与 [`full-api-p15_20260914_094448.md`](../../deployments/test-results/full-api-p15_20260914_094448.md)；
- 本地 Markdown 链接检查覆盖 46 个文件、84 个相对链接，结果为 `MARKDOWN_LINKS=PASS`。

## P2.4 已完成

P2.4 已于 2026-09-14 完成。根 Compose 现在持续运行 Documents 事务 Outbox 到 mixin-search 的影子索引链路，正式 HTTP 搜索仍由 PostgreSQL BM25 返回。

已完成实现：

- 根 Compose 新增控制 PostgreSQL、Qdrant、mixin-search 和 `document-index-worker`，共八个默认运行服务；mixin-search 仅把 gRPC 映射到回环地址，控制 PostgreSQL 与 Qdrant 不暴露主机端口；
- gin-backend 工作区镜像同时构建 HTTP 服务、Worker 和管理命令；mixin-search 镜像同时构建 gRPC 服务与健康检查器，两个运行镜像均使用非 root 用户、只读根文件系统、临时 `/tmp`、移除 Linux capabilities 并启用 `no-new-privileges`；
- `document_index_delivery` 配置统一管理开关、gRPC 地址、批量、并发、轮询、租约、RPC 超时、退避、消息上限和健康超时；开关只控制 Worker 消费，文档事务始终记录 Outbox；
- `document-index-admin status` 输出 mixin-search gRPC 健康以及 pending、processing、retry、dead-letter、过期租约、失败次数、最老未完成事件和最后投递延迟；数据库状态读取失败才使 Worker 健康检查失败；
- gin-backend `/readyz` 只反映正式 PostgreSQL/Redis 依赖，mixin-search 故障不会把 Documents/BM25 主链路标记为不可用；
- 对账继续严格要求当前文档、版本和访问策略完整，不加入历史旧数据兼容；仓储集成测试补齐当前模型并立即清理临时跨文档夹具；
- 根验收复用 P2.3 已有租约专项门禁，不再额外重启 Worker 制造长租约，故障恢复只验证 P2.4 的 mixin-search 停机、积压观测和服务恢复后自动排空；
- 装配与健康边界已固化在 [ADR-009](../adr/009-shadow-index-compose-and-health-boundary.md)。

P2.4 实测结果：

- `docker compose build gin-backend mixin-search` 成功，两个 workspace-aware 多阶段镜像完成构建；
- 从仓库根目录执行 `./deployments/verify.ps1 -SkipImageBuild`，Proto 生成一致、前端输出 `PHASE_BOUNDARY=PASS` 并构建 1862 个模块、三个 Go module 的 test/vet 全部通过、数据库输出 `BM25_ONLY_OK`；
- 空数据卷下八个默认服务全部达到 healthy；初始 Outbox 与对账事件全部排空；
- mixin-search 停机前和停机期间两轮运行时 API 回归均为 `95 passed / 0 failed / 95 total`，停机期间 gin-backend `/readyz`、Documents 写入和 PostgreSQL BM25 保持正常；
- mixin-search 恢复后积压无需人工修复即自动排空，死信为零，最终输出 `P2.4_SHADOW_INDEX=PASS` 与 `P1.5_BUILD_TEST_DEPLOYMENT=PASS`；
- 本次正常链路报告为 [`full-api-p15_20260914_103919.json`](../../deployments/test-results/full-api-p15_20260914_103919.json) 与 [`full-api-p15_20260914_103919.md`](../../deployments/test-results/full-api-p15_20260914_103919.md)；
- 本次停机链路报告为 [`full-api-p24_outage_20260914_103924.json`](../../deployments/test-results/full-api-p24_outage_20260914_103924.json) 与 [`full-api-p24_outage_20260914_103924.md`](../../deployments/test-results/full-api-p24_outage_20260914_103924.md)；
- 验收结束后一次性容器、网络和数据卷全部清理。

## P2.5 已完成

P2.5 已于 2026-09-14 完成。gin-backend 现在会在正式 PostgreSQL BM25 查询完成后非阻塞提交同查询的 mixin-search 影子检索；正式 HTTP 返回、错误和 readiness 不读取影子结果。

已完成实现：

- 新增有界、并发可配置的 ShadowSearchObserver。队列满时直接记录 dropped 日志；远程查询使用独立 deadline，进程收尾时有界排空已接收任务；
- mixin-search gRPC 客户端新增 SearchDocuments 领域映射，QueryService 只通过可选调度端口提交影子任务，HTTP handler 与响应 DTO 均未改变；
- 新增 document_search_shadow_observations，查询正文只保存 SHA-256；表内按 runtime/evaluation 来源记录 BM25/影子文档 ID、可比较结果、差异、耗时、gRPC 错误、超时以及权限/生命周期/活动版本/正式范围复核；
- 复核重新读取 gin-backend 当前事实；合法公开但不属于 owner-only SearchMine 的候选单独计为 formal scope mismatch；
- document-index-admin shadow-status 支持按来源汇总；根验证明确区分写后短暂传播窗口与 Outbox 收敛后的质量门禁；
- 新增 document-search-v1 固定评测集和 document-search-eval，覆盖中文、英文、代码、标题、正文、精确关键词、语义表达并生成 JSON/Markdown 报告；
- local-hash-v1 被明确标记为评估型 embedding，即使小样本数值达标也不能自动批准正式读取切换；完整决策见 [ADR-010](../adr/010-shadow-query-evaluation-gate.md)。

P2.5 实测结果：

- gin-backend、mixin-search、packages/gen 三个 Go module 的 go test ./... 与 go vet ./... 全部通过；前端输出 PHASE_BOUNDARY=PASS 并完成 1862 模块生产构建；
- 空数据卷下八个默认服务全部 healthy，数据库检查输出 BM25_ONLY_OK；
- 固定七类样本中，BM25 Recall@5/MRR/nDCG@5 为 1.0000/1.0000/1.0000；mixin-search 为 1.0000/0.9048/0.9286，p50 为 103167 us，p95/max 为 459029 us，错误为 0；
- 七条收敛后 evaluation 观测的权限、生命周期、活动版本与 formal scope mismatch 均为 0；
- mixin-search 停机期间第二轮运行时 API 回归仍为 95 passed / 0 failed / 95 total，gin-backend readiness 正常，影子失败观测从 0 增至 3；恢复后 Outbox 自动排空且死信为 0；
- 根验收最终输出 P2.5_SHADOW_QUERY_EVALUATION=PASS、P2.4_SHADOW_INDEX=PASS 和 P1.5_BUILD_TEST_DEPLOYMENT=PASS，并清理一次性环境；
- 质量报告为 [document-search-evaluation-p25_20260914_231537.json](../../deployments/test-results/document-search-evaluation-p25_20260914_231537.json) 与 [document-search-evaluation-p25_20260914_231537.md](../../deployments/test-results/document-search-evaluation-p25_20260914_231537.md)；
- 正常链路 API 报告为 [full-api-p15_20260914_231533.json](../../deployments/test-results/full-api-p15_20260914_231533.json) 与 [full-api-p15_20260914_231533.md](../../deployments/test-results/full-api-p15_20260914_231533.md)；
- 停机链路 API 报告为 [full-api-p24_outage_20260914_231542.json](../../deployments/test-results/full-api-p24_outage_20260914_231542.json) 与 [full-api-p24_outage_20260914_231542.md](../../deployments/test-results/full-api-p24_outage_20260914_231542.md)。

书面结论：P2.5 实施和验收已完成，但当前 embedding profile 是 local-hash-v1，评测规模也只覆盖固定的七类基线，因此正式读取继续保持 PostgreSQL BM25，当前决策为 KEEP_BM25。

## P2.5 完成后缓存加固

2026-09-15 完成了不扩展产品功能范围的缓存正确性与资源边界加固：

- User、Manager 与 Document 的 EntityCache 全部通过进程级 DefaultRuntime 构造，共享 Redis 适配器和 singleflight；内存回退拆为 entities/documents 两个 TTL + LRU 分区，并同时限制条目数和字节数；
- MemCache 未命中统一为 `ErrMiss`，EntityCache 使用 `errors.Is` 分类；运行时每分钟输出不含业务 key 的命中、故障、回源、回填/删除错误和容量统计；
- users/managers 增加数据库单调 `cache_revision`，ID 与邮箱/用户名查询先读权威 head，再统一按 `id+revision` 建键；旧读在 Evict 后晚回填时不会被新 revision 命中；
- Evict 同时尝试两级删除并聚合返回错误，只承担旧键回收，不再承担一致性；
- 删除未被调用且错误声称支持内存回退的 JWT 旧 API；当前会话状态继续只依赖 Redis 原子操作并在故障时失败关闭；
- 决策与代价固化在 [ADR-011](../adr/011-bounded-cache-runtime-and-revision-fencing.md)。

本次实测结果：

- gin-backend 全量 `go test ./...` 与 `go vet ./...` 通过；缓存单测覆盖有界 LRU、统一 miss、跨实例 singleflight、Evict 双错误和确定性的延迟旧回填隔离；
- 空卷 PostgreSQL 输出 `ENTITY_CACHE_REVISION_OK`，确认 User/Manager 两次立即更新均从 revision 1 增长到 3；
- 根验收中正常与 mixin-search 停机期间两轮 API 回归均为 `95 passed / 0 failed / 95 total`，恢复后 Outbox 自动排空且死信为 0；
- 根验收最终输出 `P2.5_SHADOW_QUERY_EVALUATION=PASS`、`P2.4_SHADOW_INDEX=PASS` 和 `P1.5_BUILD_TEST_DEPLOYMENT=PASS`，并删除一次性容器、网络和数据卷；
- 本次报告为 [正常 API JSON](../../deployments/test-results/full-api-p15_20260915_033311.json)、[正常 API Markdown](../../deployments/test-results/full-api-p15_20260915_033311.md)、[停机 API JSON](../../deployments/test-results/full-api-p24_outage_20260915_033321.json)、[停机 API Markdown](../../deployments/test-results/full-api-p24_outage_20260915_033321.md)、[检索评测 JSON](../../deployments/test-results/document-search-evaluation-p25_20260915_033317.json) 与 [检索评测 Markdown](../../deployments/test-results/document-search-evaluation-p25_20260915_033317.md)。

## 当前仍然有效的限制

- P2.5 已完成影子查询与质量评估，但 local-hash-v1 仅为确定性评估 embedding；接入真实语义 embedding 并扩大标注集前不进入读取切换；
- runtime 观测会如实记录 Outbox 收敛前的短暂旧版本/删除差异；稳定正确性门禁使用索引收敛后的 evaluation 来源；
- PostgreSQL BM25 仍是正式读取方，当前结果不表示已完成混合检索切换；
- Chat/WebSocket 继续保留源码但不注册，py-agent/QQ、聊天记录域和团队空间界面不进入阶段 2；
- 当前验证是无生产数据前提下的本地开发基线，不代表生产部署、高并发、灰度、备份恢复或灾难恢复已经完成。
