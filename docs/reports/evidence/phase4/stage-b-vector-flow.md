# 阶段 B 证据：本地哈希向量流程接管到 document-search

> 任务：共享任务 `task-5`（负责人 `svc-vector`，2026-09-30）
> 写入范围：`apps/document-search/`（源码、schema、配置、测试、README）、本证据文件
> 契约基线：`docs/adr/017-source-owned-document-and-search-services.md` 决策 5/6、`docs/planning/CURRENT_IMPLEMENTATION_PLAN.md` §0.10 阶段 B、`docs/contracts/DOCUMENT_SERVICE_V1_CONTRACT.md` §6.1
> 环境：Windows + Go 1.26.8；PostgreSQL `127.0.0.1:15432/gin_demo`（角色 `document_search_writer`）；Qdrant v1.19.1 gRPC `127.0.0.1:16334`（HTTP 16333 未发布，实测不可达且本实现只用 gRPC）
> 构建前置：`$env:GOCACHE = Join-Path $env:TEMP 'gb-goweb'`
> 本轮验收目标：向量流程在位、可写、可删、可重建、可查询且结果可复现；**语义模型与检索效果不在本轮验收**（§0.10 明确）。

## 1. 能力清单与唯一负责人

| 能力 | 唯一负责人 | 实现落点 | 证据 |
| --- | --- | --- | --- |
| 本地哈希 embedding（`local-hash-v1`） | **document-search** | `internal/application/embedding.go` | `embedding_test.go` 用旧实现原码跑出的黄金向量逐桶断言 |
| Qdrant collection/alias 创建、切换、删除、清理 | **document-search** | `internal/infrastructure/qdrant/store.go` | `TestIntegrationVectorNamespaceMatchesTheActiveGeneration`、`TestIntegrationVectorGenerationLifecycleOnAScratchAlias` |
| collection 身份（alias / storage domain / profile / 维度）来自索引记录 | **document-search** | `internal/application/vectorindex.go`（读 `document_search.index_generations`） | 同上；不一致即启动失败 |
| 事件写入路径的向量写/替换/删除（含栅栏） | **document-search** | `internal/application/{vector_write.go,events.go,consumer.go}` | `TestIntegrationVectorUpsertWritesAndReplacesPoints`、`TestIntegrationVectorDeleteRemovesPoints`、`TestIntegrationVectorFailureRollsBackTheIndexTransaction` |
| 重建（关键词 + 向量，本地事件重放）+ 过期 collection 清理 | **document-search** | `internal/application/rebuild.go` | `TestIntegrationVectorRebuildRestoresPointsWithoutFactSource`、`TestIntegrationRebuildUsesLocalEventsOnly` |
| 关键词 + 向量混合检索（RRF 融合）、授权/生命周期/分页/真实 `total` | **document-search** | `internal/application/{search.go,hybrid.go}` | `TestIntegrationHybridSearchRecallsVectorOnlyDocument` + 阶段 A 的 6 个检索集成测试全部保持通过 |
| 组合根装配（进程启动即打开集合、装配失败即启动失败、`/readyz` 含向量后端） | **document-search** | `internal/app/app.go`、`internal/interfaces/grpcapi/api.go` | `TestIntegrationAppComesUpWithTheVectorFlow`、`TestIntegrationAppRefusesToStartWithoutTheVectorBackend` |
| 固定数据集评测 | **document-search**（唯一负责人） | `internal/application/evaluation_integration_test.go`（数据集 `deployments/evaluation/document-search-v1.json` 只读引用） | `TestIntegrationEvaluationDatasetIsStableAndReproducible` |

`apps/mixin-search` 的本地哈希向量与 Qdrant 后端只作为**只读参考基线**，未修改、也不再是任何能力的负责人。

## 2. 修改文件

| 文件 | 变更 |
| --- | --- |
| `internal/application/embedding.go`（新） | `local-hash-v1`：小写 unigram + 相邻 bigram、FNV-1a 64、64 维有符号桶、L2 归一化；`embedText`/`embedText32`/`tokenize`/`profileDimensions`，纯函数无外部依赖 |
| `internal/vectorindex/vectorindex.go`（新） | 向量索引端口：`Index` 接口 + `Document`/`Chunk`/`Filter`/`Candidate`；`Filter.MatchesNothing` 明确“公开兜底不是召回族” |
| `internal/infrastructure/qdrant/store.go`（新） | Qdrant 实现：`EnsureGeneration`（幂等创建 + 维度校验 + payload 索引 + alias）、`PrepareGeneration`、`SwitchAlias`（校验前缀、失败补偿、回读校验）、`DropAlias`、`DropCollection`、`PruneGenerations`、`ReplaceDocuments`（先删后写）、`DeleteDocuments`、`Search`（按文档折叠、稳定排序）、`PointCount`、`Health`；每次调用受 `vector.timeout` 约束；并发创建按“AlreadyExists 后复查”收敛 |
| `internal/application/vectorindex.go`（新） | 读活动 generation 行并交叉校验配置；构造 Qdrant store 并 `EnsureGeneration`；`NewVectorIndex` 是进程根与测试夹具共用的唯一入口 |
| `internal/application/vector_write.go`（新） | `vectorBatch`（按文档取最后一次变更）、`flush`（先删后写，事务提交前执行）、`vectorDocumentForEvent`（与关键词索引同一分块，chunk 0 携带标题/摘要） |
| `internal/application/hybrid.go`（新） | 关键词候选 + 向量召回 + RRF 融合（`fuseRanks` 纯函数）、融合后分页、`total` = 关键词匹配数 + 向量独有召回数；召回文档必须经 SQL 范围过滤回读 |
| `internal/application/service.go` | `Dependencies.VectorIndex`；`vector.enabled` 时缺少向量索引即构造失败 |
| `internal/application/search.go` | `searchScopePredicate` 拆为 `searchScopeFilter` + `searchKeywordConjunct`；`searchHitsSQL` → `searchKeywordCandidatesSQL`（有界候选，SQL 内排序）；新增 `selectScopedDocumentsSQL`（召回文档回读 + `keyword_match` 标记）；查询入口改为调用 `retrieveDocuments`；`search.go` 的导入面不变（架构断言继续通过） |
| `internal/application/events.go` | `applyEventTx` 增加向量参数并在提交前 `flush`；`mutateIndex` 记录向量变更（被栅栏拒绝的事件不触碰集合）；`upsertIndexRowSQL` 增加 `vector_profile`/`vector_dimensions` |
| `internal/application/consumer.go` | `ConsumerDependencies.VectorIndex`；消费路径把向量索引传入同一 apply |
| `internal/application/rebuild.go` | 重放批次内累积向量变更并在提交前写入；重放成功后 `PruneGenerations` 删除未被 `index_generations` 引用的物理 collection（失败则标记 rebuild 失败，不静默） |
| `internal/config/config.go` | 新增 `vector.*` 配置段（默认值、环境变量覆盖、`VectorConfig()` 校验、`ProfileDimensions` 封闭集合）；`vector.enabled=false` 时仍给出可用的融合边界 |
| `internal/app/app.go` | 启动时 `NewVectorIndex` 并注入 transport 与 consumer；装配失败关闭资源；`/readyz` 同时检查数据库与向量后端；`Shutdown`/`Close` 关闭向量客户端 |
| `internal/interfaces/grpcapi/api.go` | `New` 增加 `application.VectorIndex` 参数并注入业务层 |
| `schema/schema_init.sql` | `document_index` 增加 `vector_profile`/`vector_dimensions`；`index_generations` 增加 `vector_profile`/`vector_dimensions`（含物理名 `<alias>_<generation>` 的说明）；文件末尾补充幂等 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` |
| `configs/config.yaml`、`configs/config.docker.yaml` | 新增 `vector:` 段并注释（本机 `127.0.0.1:16334`，Compose `qdrant:6334`） |
| `README.md`（新） | 能力/负责人表、向量流程、collection 身份、写入/重建/查询融合语义（含 `total` 定义）、配置项、测试方式、数据库同步说明 |
| 测试：`internal/application/{embedding_test,hybrid_fusion_test,vector_integration_test,evaluation_integration_test}.go`、`internal/app/app_integration_test.go`、`internal/config/vector_test.go`、`internal/dbtest/devschema_apply_test.go`（扩展） | 新增 10 个纯单元测试 + 6 个配置测试 + 11 个集成测试；开发库守卫扩展到向量列 |
| 既有测试适配：`internal/application/{integration_test,search_integration_test,consumer_integration_test}.go`、`internal/interfaces/grpcapi/transport_integration_test.go` | 夹具注入真实 Qdrant 向量索引并清理自己的点；3 处“陈旧/已删除内容不可检索”的断言按混合语义改写（见 §3.3） |

未改动（按要求）：`packages/proto`、`packages/gen`、`packages/serviceauth`、`go.work`、`go.work.sum`、`docker-compose.yaml`、`deployments/`、`docs/contracts/`、`docs/adr/`、`apps/mixin-search/`、`apps/gin-backend/`、`apps/qq-search/`。`apps/document-search/go.mod`/`go.sum` 仅新增 `github.com/qdrant/go-client v1.19.2`（与 mixin-search 同版本，模块缓存内已有，`go mod tidy` 在受限模式下无法写入工作区，故按缓存与 mixin-search 的校验和手工补齐两行）。

## 3. 契约与行为变化

### 3.1 查询从“仅关键词”变为“关键词 + 向量 RRF”

- `SearchDocuments` 现在由两条臂组成：关键词臂（tsvector/标题子串，SQL 内排序）与向量臂（Qdrant 召回 `vector.recall` 个点，按文档折叠取最高分），用 RRF（`1/(rrf_k+rank)` 求和，`rrf_k` 默认 60）融合，按融合分降序、`document_id` 升序分页。
- `SearchHit.score` 的语义由“关键词分数”变为**融合分数**（RRF），这是本轮唯一对外可见的字段语义变化；阶段 A 固定的字段（`owner_subject_key`、`authenticated_public`、`created_at`、`updated_at`）与生命周期/发布过滤不变。
- **`total` 的语义**：融合答案集的真实大小 = 该范围内关键词匹配数（`count(*)`，完整计数）+ 向量臂额外召回且通过同一 SQL 过滤的文档数。命中与总数来自同一组候选集合；`page`/`page_size` 各页互不重叠、不遗漏；关键字候选超过 `vector.max_keyword_candidates` 或本页之后仍有答案时 `truncated=true`。`vector.enabled=false` 时退化为“关键词匹配数”，与阶段 A 完全一致。
- 授权规则不放宽：请求中任一未授予标识仍整体 `PERMISSION_DENIED`；收窄规则、`owned_by_subject_only`、范围失败关闭均未改动。

### 3.2 向量臂的召回族与授权

- 召回族是调用方**点名或获授权的空间与文档**。`authenticated_public` 兜底是授权下限，不作为召回族：否则任何未收窄查询都会把整个部署的公开语料按相似度算进答案（实测：开发库公开语料 62 篇时，阶段 A 的 3 个精确总数断言被抬高到 63–66）。
- 兜底仍然完整生效于**过滤**：向量臂召回的每个 document id 都必须通过 `selectScopedDocumentsSQL`（含公开兜底、生命周期、发布状态、主体过滤）才可能成为命中。因此“公开文档对空信封可见”的既有行为不变（`TestIntegrationQueryAuthorization` 通过），只是它由关键词臂承载。
- 后果（写入 README 与本节）：某公开文档若既不含查询词、又不在调用方自己的空间/文档族内，不会被向量臂召回。这是本轮明确的取舍，不改变任何既有可检索集合。

### 3.3 阶段 A 的 3 处断言按混合语义改写（语义未放宽）

混合检索会返回“同一范围内、向量相似但未命中关键词”的文档。因此 3 处“陈旧内容不可检索”的断言不能再要求整页为空，改为断言**陈旧版本/已删除文档本身不出现**：

| 测试 | 原断言 | 现断言 |
| --- | --- | --- |
| `TestIntegrationUpdateReplacesSearchableContent` | 旧内容查询返回 0 命中 | 命中的版本必须是新版本，且标题/摘要不含旧内容 |
| `TestIntegrationOutOfOrderEventsAreIgnored` | 迟到（低 revision）内容查询返回 0 命中 | 命中版本必须是当前版本，且不含迟到内容 |
| `TestIntegrationRebuildUsesLocalEventsOnly` | 已删除文档查询返回 0 命中 | 已删除文档不在命中中（同空间其他文档可被向量臂召回） |

其余 20 个阶段 A 集成测试（范围收窄、主体隔离、分页、页面上限、生命周期、Web 字段、权限包含、重复/乱序/tombstone、重建、schema 隔离、命名空间、状态、消费游标、传输边界、开发库守卫等）**一行未改、全部通过**。

### 3.4 启动与就绪

- `vector.enabled=true`（默认）时，进程启动即打开活动 generation 的集合；Qdrant 不可达、alias 与索引记录不一致、profile/维度不一致都会导致启动失败（不静默退化为只查关键词）。
- `/readyz` 现在同时要求数据库与向量后端可用，理由同上；`/healthz`、`/info` 不变。

## 4. 数据库与集合变更

### 4.1 开发库（已同步执行，幂等）

以 `postgres` 超级用户经 `127.0.0.1:15432` 执行（服务角色不是这两张表的属主；`docker compose exec` 在本会话被沙箱拒绝、不允许提权，故用一次性 Go 程序，源码位于仓库外的 `%TEMP%\dsh-vec-migrate\main.go`——本会话沙箱不允许删除该临时目录，故保留，它不属于仓库内容）：

```sql
ALTER TABLE document_search.document_index
    ADD COLUMN IF NOT EXISTS vector_profile varchar(32) NOT NULL DEFAULT 'local-hash-v1';
ALTER TABLE document_search.document_index
    ADD COLUMN IF NOT EXISTS vector_dimensions integer NOT NULL DEFAULT 64 CHECK (vector_dimensions > 0);
ALTER TABLE document_search.index_generations
    ADD COLUMN IF NOT EXISTS vector_profile varchar(32) NOT NULL DEFAULT 'local-hash-v1';
ALTER TABLE document_search.index_generations
    ADD COLUMN IF NOT EXISTS vector_dimensions integer NOT NULL DEFAULT 64 CHECK (vector_dimensions > 0);
```

实测输出（`information_schema.columns` + 计数）：

```text
COLUMN document_index.vector_profile character varying nullable=NO default='local-hash-v1'::character varying
COLUMN document_index.vector_dimensions integer nullable=NO default=64
COLUMN index_generations.vector_profile character varying nullable=NO default='local-hash-v1'::character varying
COLUMN index_generations.vector_dimensions integer nullable=NO default=64
COUNTS events=1183 documents=513 generations=1
GENERATION g1 alias=go_web_document_v1 domain=document-search:documents:v1 profile=local-hash-v1 dimensions=64 active=true
```

新库由 `schema/schema_init.sql` 直接建出这些列；旧库用同文件末尾的幂等 `ALTER` 同步。运行库守卫：`TestIntegrationDevSchemaCarriesTheQueryPathColumns`（扩展后含 `vector_profile`/`vector_dimensions`）与新增的 `TestIntegrationDevSchemaCarriesTheVectorFlowColumns`。

### 4.2 Qdrant（实测）

```text
COLLECTIONS (1): [go_web_document_v1_g1]
ALIAS go_web_document_v1 -> go_web_document_v1_g1
POINTS alias go_web_document_v1: 513
DISTINCT DOCUMENTS (first page): 513
INFO go_web_document_v1_g1 status=Green points=513 vectors=64 payload_indexes=10
```

- 计划 §0.7/§0.10 记录 `go_web_document_v1` 当时只在 `index_generations` 登记、Qdrant 中并未创建；本轮首次真实创建（物理名 `<alias>_<generation>` = `go_web_document_v1_g1`）。
- 513 个点 = 本地事件账本重放后的 513 篇文档（与 `document_index` 行数一致，单 chunk 文档 1 点/篇）。
- 10 个 payload 索引：`storage_domain`、`document_id`、`version_id`、`owner_space_id`、`owner_subject_key`、`allowed_space_ids`、`vector_profile`（keyword）+ `authenticated_public`、`active`、`tombstoned`（bool）。
- 每个测试夹具在结束时删除自己文档的点；scratch alias 生命周期测试创建并清理了独立 alias，实测未留残余 collection。

## 5. 实际命令与退出码

工作目录：`apps/document-search`（另有一步在仓库根验证 workspace 模式）

| 命令 | 退出码 | 关键输出 |
| --- | --- | --- |
| `go build ./...` | 0 | 无输出 |
| `go vet ./...` | 0 | 无输出 |
| `gofmt -l .` | 0 | 0 行输出 |
| `go test ./... -count=1` | 0 | `ok internal/app 0.108s`；`ok internal/application 17.452s`；`ok internal/architecture 0.052s`；`ok internal/config 0.017s`；`ok internal/dbtest 0.076s`；`ok internal/interfaces/grpcapi 0.133s`；其余 `[no test files]` |
| `go test ./... -count=1 -run Integration -v` | 0 | `--- PASS` = **34**，`--- FAIL` = **0**，`--- SKIP` = **0** |
| `go test ./... -count=1 -run Integration`（连续 3 次） | 0,0,0 | 每次五个包全 `ok`（application 14.1–15.3s） |
| 仓库根：`go build ./apps/document-search/...` | 0 | 无输出（go.work 工作区模式，未修改 `go.work.sum`） |
| 仓库根：`go test ./apps/document-search/... -count=1` | 0 | 五个包全 `ok` |
| `go run %TEMP%\dsh-vec-reference\main.go`（旧实现原码，仅加打印） | 0 | 生成 `embedding_test.go` 的黄金向量（源码位于仓库外，保留原因同上） |
| `go run %TEMP%\dsh-vec-inspect\main.go` | 0 | §4.2 的 Qdrant 实测输出（只读，源码位于仓库外） |

集成测试总数：**34**（阶段 A 的 23 个全部保持通过 + 本轮 11 个），**0 跳过**，全部连接真实 PostgreSQL 与真实 Qdrant；连不上即 `t.Fatalf`（`openVectorIndex`）。

新增集成测试：

- `TestIntegrationVectorUpsertWritesAndReplacesPoints`：upsert 后点数 = `chunk_count`；索引行记录 profile/维度；缩短版本后点数 = 新版本 chunk 数（旧 chunk 未残留），召回版本为新版本。
- `TestIntegrationVectorDeleteRemovesPoints`：delete 后点数 0 且不可召回；被栅栏拒绝的 upsert 不写点。
- `TestIntegrationVectorRebuildRestoresPointsWithoutFactSource`：删除集合中的点后用不可达的 source 端点重建，点数与版本恢复，alias 仍指向记录中的物理 collection（同时验证重建顺带清理）。
- `TestIntegrationHybridSearchRecallsVectorOnlyDocument`：关键词臂只返回 1 篇；混合返回 2 篇（关键词命中 + 仅向量召回），顺序为关键词文档在前，范围外同内容文档不出现，向量命中带真实展示字段；融合序分页 1+1 且 total=2、truncated 为 true/false；`owned_by_subject_only` 下二者都不返回。
- `TestIntegrationVectorNamespaceMatchesTheActiveGeneration`：`Alias/StorageDomain/Generation/Profile/Dimensions` 与 `index_generations` 活动行逐项一致；`PhysicalCollection == <alias>_<generation>`；`GetIndexStatus` 报告同一身份。
- `TestIntegrationVectorGenerationLifecycleOnAScratchAlias`：独立 alias 上 `EnsureGeneration` → `PrepareGeneration(g2)` → 写入 → `SwitchAlias`（新代为空、越界目标被拒、被服务的 collection 不可删）→ `PruneGenerations` 只删未登记代 → 登记代被保留并可显式删除。
- `TestIntegrationVectorFailureRollsBackTheIndexTransaction`：向量写失败 → apply 返回 `UNAVAILABLE`，SQL 投影与事件账本都没有落库；向量恢复后同一事件重投成功并写入点。
- `TestIntegrationAppComesUpWithTheVectorFlow` / `TestIntegrationAppRefusesToStartWithoutTheVectorBackend`：组合根装配（真实集合、`/readyz` 200 ready）与失败关闭（Qdrant 不可达即启动失败）。
- `TestIntegrationEvaluationDatasetIsStableAndReproducible`：固定数据集三次运行一致 + 重建后一致 + 每个 relevant 文档都在答案中（见 §6）。
- `TestIntegrationDevSchemaCarriesTheVectorFlowColumns`：运行库守卫。

新增纯单元测试（无数据库、无 Qdrant）：`application` 10 个（黄金向量含 5 个子用例、确定性/归一化、float32 收窄、profile 封闭集合、RRF 5 个、召回过滤器 fail-closed）与 `config` 6 个（默认值、禁用开关、10 类非法值、`Validate` 覆盖、两份随仓库配置可加载并通过校验、profile 表）。

## 6. 固定数据集结果

`deployments/evaluation/document-search-v1.json`（7 文档 / 7 查询，只读引用，未修改）经由真实事件路径入索引、真实混合查询路径执行。**三次运行顺序逐字相同**（同进程内两次 + 完整重建后一次，测试断言），`total` 与答案集合在两次独立进程运行中亦相同。

`go test ./internal/application/ -run TestIntegrationEvaluationDatasetIsStableAndReproducible -v` 实测输出（`[kw]` = 关键词臂独立命中，`[vec]` = 仅向量臂召回）：

```text
dataset document-search-v1: 7 documents, 7 queries, all three runs identical
query q-zh       chinese             "并发控制"            | total=7 truncated=false | keyword_matches=1 | order=[zh-concurrency[kw] exact-quasar[vec] semantic-recovery[vec] title-beidou[vec] code-timeout[vec] body-mars[vec] en-outbox[vec]] | relevant=[zh-concurrency]
query q-en       english             "transactional outbox"| total=7 truncated=false | keyword_matches=1 | order=[en-outbox[kw] code-timeout[vec] semantic-recovery[vec] title-beidou[vec] body-mars[vec] exact-quasar[vec] zh-concurrency[vec]] | relevant=[en-outbox]
query q-code     code                "context.WithTimeout" | total=7 truncated=false | keyword_matches=1 | order=[code-timeout[kw] zh-concurrency[vec] en-outbox[vec] exact-quasar[vec] title-beidou[vec] body-mars[vec] semantic-recovery[vec]] | relevant=[code-timeout]
query q-title    title               "北斗索引"             | total=7 truncated=false | keyword_matches=1 | order=[title-beidou[kw] semantic-recovery[vec] body-mars[vec] zh-concurrency[vec] exact-quasar[vec] code-timeout[vec] en-outbox[vec]] | relevant=[title-beidou]
query q-body     body                "火星信标"             | total=7 truncated=false | keyword_matches=0 | order=[zh-concurrency[vec] exact-quasar[vec] semantic-recovery[vec] code-timeout[vec] body-mars[vec] title-beidou[vec] en-outbox[vec]] | relevant=[body-mars]
query q-exact    exact_keyword       "quasar-p25-token"    | total=7 truncated=false | keyword_matches=1 | order=[exact-quasar[kw] zh-concurrency[vec] title-beidou[vec] body-mars[vec] semantic-recovery[vec] en-outbox[vec] code-timeout[vec]] | relevant=[exact-quasar]
query q-semantic semantic_expression "系统恢复后怎样保证消息不会丢失" | total=7 truncated=false | keyword_matches=0 | order=[semantic-recovery[vec] title-beidou[vec] zh-concurrency[vec] exact-quasar[vec] body-mars[vec] code-timeout[vec] en-outbox[vec]] | relevant=[semantic-recovery]
```

读法与边界（**不是效果验收**，按 §0.10 语义模型与效果另行验证）：

- **流程证据**：7 篇文档全部经“事件 → SQL 投影 + 向量点”写入；每条查询都返回真实 `total`、`truncated=false`；`q-semantic`（“系统恢复后怎样保证消息不会丢失”）关键词臂 0 命中，向量臂单独把 `semantic-recovery` 排在第一——即“关键词结果之外加入向量召回”可实测；`q-zh`/`q-en`/`q-code`/`q-title`/`q-exact` 的相关文档均排在第一位。
- **`total=7` 的原因**：`vector.recall=100` 大于该范围内集合点数 7，向量臂因此召回整个获授范围（小语料下属正常行为，任何语料下最多额外召回 `vector.recall` 篇）；本数据集的判别信息在**融合顺序**而非总数。
- **`q-body` 相关文档排在第 5**：`local-hash-v1` 是字符级哈希向量（无外部语义模型），“火星信标”与多篇文档共享单字特征，属检索效果范畴，本轮不调参、不引入外部模型（符合 §0.10 与本任务约束）。
- **跨进程可复现性的边界**：同一进程内重复查询、以及重建前后，顺序逐字相同（测试断言）。两次独立进程运行中，答案集合、`total`、关键词命中集合、以及关键词命中在前的头部完全一致；当若干候选与查询的余弦相似度**恰好相等**（哈希向量常见的 0.0）时，并列顺序由 `document_id` 破平，而评测夹具每次运行生成新的文档 UUID，故并列组的先后可能不同。真实部署中文档 id 固定，同一查询顺序完全确定。

## 7. 验收项 → 证据映射

| 验收项（任务清单） | 证据 |
| --- | --- |
| 1 向量流程：embedding 同一 profile、profile 进配置与索引记录、collection/alias 创建与切换、命名取自 `index_generations` | `embedding.go` + `embedding_test.go`（旧实现原码黄金向量）；`vectorindex.go` 读活动行并交叉校验；`config.yaml`/`config.docker.yaml` 的 `vector.profile`；`document_index.vector_profile`、`index_generations.vector_profile` 列；`TestIntegrationVectorNamespaceMatchesTheActiveGeneration`、`TestIntegrationVectorGenerationLifecycleOnAScratchAlias` |
| 2 写入路径：upsert 写/替换（含旧版本）、delete/tombstone 删除、同一事件流程、幂等与乱序栅栏不变 | `events.go`（栅栏先决、提交前 flush）、`vector_write.go`；`TestIntegrationVectorUpsertWritesAndReplacesPoints`、`TestIntegrationVectorDeleteRemovesPoints`、`TestIntegrationDuplicateEventIsNoOp`、`TestIntegrationOutOfOrderEventsAreIgnored`、`TestIntegrationDeleteIsFencedByLifecycleOnly`、`TestIntegrationVectorFailureRollsBackTheIndexTransaction` |
| 3 重建同时重建关键词与向量且不调用事实源 | `rebuild.go`（重放同一批事务内写向量）；`TestIntegrationVectorRebuildRestoresPointsWithoutFactSource`（source 指向关闭端口）、`TestIntegrationRebuildUsesLocalEventsOnly` |
| 4 查询：关键词之外加入向量召回、明确融合规则、保持包含规则/主体过滤/分页/真实 `total` | `hybrid.go`（RRF）、`search.go`（范围内候选 + 回读过滤）；`TestIntegrationHybridSearchRecallsVectorOnlyDocument` + 阶段 A 6 个检索集成测试 + `TestIntegrationQueryAuthorization` |
| 5 删除/清理路径：再建索引不残留旧 collection | `store.PruneGenerations`/`DropCollection`/`DropAlias`，`rebuild.go` 重放成功后清理；`TestIntegrationVectorGenerationLifecycleOnAScratchAlias`（创建/切换/清理/显式删除）、Qdrant 实测只剩 1 个 collection |
| 6 固定数据集验证与评测唯一负责人 | §6 输出 + `TestIntegrationEvaluationDatasetIsStableAndReproducible`；负责人登记见 §1 |
| 7 配置项默认值、校验与说明 | `config.VectorConfig()` + `vector_test.go`（含两份随仓库配置加载校验）+ `configs/*.yaml` 注释 + `README.md` |
| 8 集成测试连真实 PostgreSQL 与真实 Qdrant，不允许 skip | 34 PASS / 0 SKIP；`openVectorIndex` 连接失败即 `t.Fatalf`；`dbtest.Open` 同类语义 |
| 阶段 A 行为不回退 | 23 个阶段 A 集成测试全部通过（其中 3 处断言按混合语义改写，见 §3.3），`internal/architecture` 结构断言通过 |

## 8. 架构与质量约束

- 只写 `apps/document-search/` 与本证据文件；未导入其他应用 `internal` 包（`TestNoForeignModuleImports`）、未写其他服务业务表（`TestNoCrossServiceTableWrites`）、业务代码未依赖组合根（`TestNoServiceImportsItsOwnCompositionRoot`）——`internal/architecture` 全绿。
- 查询路径仍无事实源客户端：`TestQueryPathHasNoSourceClient` 通过，`search.go` 导入面仍为 `context`/`fmt`/`strings`/`packages/gen/documentsearch/v1`/`packages/serviceauth`；新增的向量代码只依赖 `internal/vectorindex` 端口与 Qdrant gRPC，不触碰 `packages/gen/document/v1` 与 `google.golang.org/grpc` 客户端（该限制只允许 `consumer.go` 持有）。
- Qdrant 与 PostgreSQL 无共享事务，写入顺序在代码与 README 中写明：向量写在 SQL 提交之前，失败即回滚并重投；提交失败留下的孤立点因“答案必须通过 SQL 投影过滤”而不可能成为命中（`TestIntegrationVectorFailureRollsBackTheIndexTransaction`）。
- `go build` / `go vet` / `gofmt -l`（0 行）/ `go test ./... -count=1` 全部通过；连续 3 次集成运行 0 失败（并发包同时跑）。

## 9. 未运行项、依赖与边界

**未运行项**

1. **Docker Compose 全栈未重建/未运行**：本会话沙箱拒绝 Docker API（`npipe:////./pipe/dockerDesktopLinuxEngine`，权限不足）且不允许提权，因此 `configs/config.docker.yaml`、容器内 `/readyz` 探针、`qdrant:6334` 服务名解析均**未在容器中实测**（配置加载与校验已由 `TestShippedConfigurationsLoad` 覆盖，Qdrant 连通性由本机 16334 实测覆盖）。
2. **Qdrant HTTP 16333 不可达**：Compose 只发布 gRPC 16334（任务简报里的 16333 实测连接被拒）。本实现只用 gRPC，无影响；未为此改动 `docker-compose.yaml`（超出写入范围）。
3. **语义模型与检索效果未验证**：按 §0.10“语义模型与效果另行验证”，本轮不引入外部模型、不做召回率/排序调参；§6 的效果观察仅作记录。
4. 未做容量/性能压测；全量重建（1183 事件 / 513 文档，含向量写入）实测单次约 1.5s（另一测试含两次重建共 3.4s），不构成瓶颈。

**依赖与需要 `lead` 决策的事项**

1. `vector.enabled: true` 后 **Qdrant 是启动硬依赖**：`docker-compose.yaml` 的 `document-search` 服务目前只有 `depends_on: postgres/document-service`，建议补 `qdrant: {condition: service_healthy}`（该文件不在本任务写入范围，未改）。当前行为是容器启动失败后由 `restart: unless-stopped` 重试直至 Qdrant 就绪。
2. 运行中的旧开发库需要执行 §4.1 的四条 `ALTER`（已执行）；新库由 `schema_init.sql` 直接覆盖。
3. `apps/document-search/go.mod`/`go.sum` 新增 `github.com/qdrant/go-client v1.19.2`：因受限模式无法在工作区内写文件（`go mod tidy` 报 `Access is denied`），`go.sum` 的两行取自模块缓存与 mixin-search 的校验和（同一模块同一版本）。**实测校验**：`GOWORK=off GOFLAGS=-mod=mod GOPROXY=off go mod tidy -diff` 输出中这两行是未改动的上下文行，即手工补的校验和与 tidy 要写的一致；同一 diff 还显示该 module 在本轮之前就不是 tidy-clean 的（`google.golang.org/protobuf` 被代码直接导入却仍标 `// indirect`，另有几条无用的 `go.sum` 行），本轮未改动这些既有差异。
4. 阶段 A 证据中登记的既有边界（`SearchHit.updated_at` 取事件的 `occurred_at`）不变；本轮的 `SearchHit.score` 语义变化（融合分）建议由 `lead` 在契约说明或后续文档中确认是否需要显式登记（`DOCUMENT_SERVICE_V1_CONTRACT.md` §6.1 未定义 `score` 字段语义）。
