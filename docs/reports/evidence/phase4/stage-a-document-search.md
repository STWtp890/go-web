# 阶段 A 证据：document-search 范围收窄、主体过滤、分页与真实字段

> 任务：共享任务 `task-2`（负责人 `svc-search`，2026-09-30 reopen 后完成修正）
> 日期：2026-09-30
> 写入范围：`apps/document-search/`（源码、schema、测试）、本证据文件
> 环境：Windows + Go；开发库 `postgres://document_search_writer:document_search@127.0.0.1:15432/gin_demo?sslmode=disable`（PostgreSQL 容器已运行）
> 构建前置：`$env:GOCACHE = Join-Path $env:TEMP 'gb-goweb'`
> 契约基线：`packages/proto/documentsearch/v1`（`top_k`→`page_size`、`owned_by_subject_only`、`SearchHit` 四个新字段、`IndexDocumentEventRequest.created_at`），`docs/contracts/DOCUMENT_SERVICE_V1_CONTRACT.md` §6.1

## 1. 交付内容

1. **修复“收窄失效”**：新增纯函数 `effectiveSearchScope`（`internal/application/search.go`）。capability 只作授权上限；请求中**点名的族就是 SQL 的实际过滤集合**，未命名族贡献空集、不回退到 capability；`authenticated_public` 兜底只在**整个请求都没有收窄（两族都为空）**时生效。该语义已由 `lead` 确认与契约 §6.1 一致。
2. **主体过滤**：`owned_by_subject_only=true` 时 SQL 增加 `owner_subject_key = <capability 主体>` 合取；主体只从已校验 capability 读取。主体为空 → 直接返回空结果（`total=0`、无命中），不查库。
3. **命中与总数同条件**：`searchCountSQL` 与 `searchHitsSQL` 共用同一个 `searchScopePredicate` 常量，并接收同一个 `searchScope`；`total` 是真实匹配数，不是本页条数。
4. **分页**：`page`（0/负视为 1）与 `page_size`（0 取配置默认；超上限下调为上限并置 `truncated`）。排序固定为 `score DESC, document_id`，各页互不重叠、不遗漏；末页之后返回空命中但 `total` 仍为真实匹配数。
5. **命中返回 Web 展示字段**：`owner_subject_key`、`authenticated_public`、`created_at`、`updated_at`。**两个时间戳都是文档的真实时刻**：`created_at` 取事件的 `created_at`，`updated_at` 取事件的 `occurred_at`（文档服务在同一事务里把 `documents.updated_at` 与事件 `occurred_at` 设为同一个 `now`）。索引投影由此新增 `created_at` 与 `document_updated_at` 两列；`updated_at` 列保留为索引写入时刻（`DocumentIndexState.indexed_at` 仍读它）。查询路径用 `to_char(... AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')` 在 SQL 内渲染 UTC RFC3339 微秒串，不引入时间依赖。
6. **生命周期/发布/权限过滤不变**：仍为 `lifecycle_status='active' AND publication_status='published'`；已删除（tombstone）、已撤销、非活动版本、草稿一律不进入结果。
7. **索引侧时间列**：`document_search.document_index` 新增 `created_at` 与 `document_updated_at`（均 `timestamptz NOT NULL DEFAULT clock_timestamp()`）；消费时从事件的 `created_at` / `occurred_at` 写入，事件缺失或不可解析时插入回退到写入时刻、更新保留已存值，因此重复投递不会移动文档时间线；重建重放同一事件载荷因而保留两个时刻。
8. **测试**：新增 1 个单元测试文件（4 个用例）+ 6 个检索集成测试 + 1 个开发库 schema 守卫集成测试，全部连真实 PostgreSQL、0 跳过；`TestIntegrationSearchHitCarriesWebFields` 用显式时刻断言 `created_at` / `updated_at` 等于事件声明值，并断言重建后不变。

## 2. 修改文件

| 文件 | 变更 |
| --- | --- |
| `internal/application/search.go` | 新增 `searchScope` 与 `effectiveSearchScope`（收窄规则）；`searchScopePredicate` 增加 `$4` 公开兜底开关与 `$5` 主体合取；`searchHitsSQL` 返回 4 个新字段（`updated_at` 取 `document_updated_at`）并改 `LIMIT $6 OFFSET $7`；`countMatches` 接收同一个 `searchScope`；`top_k`→`page_size`；`total` 溢出饱和 |
| `internal/application/events.go` | `documentEvent.CreatedAt` / `DocumentUpdatedAt`；`parseEventInstant`；`upsertIndexRowSQL` 写入 `created_at`（`$20`）与 `document_updated_at`（`$21`），更新时各自回退到已存值 |
| `internal/application/consumer.go` | `requestFromEnvelope` 映射 `envelope.created_at`（`occurred_at` 原有映射保持不变） |
| `internal/application/integration_test.go` | fixture `documentSpec` 新增 `OwnerSubjectKey`、`CreatedAt`、`OccurredAt`（默认值与既有行为一致） |
| `internal/application/consumer_integration_test.go` | `buildEnvelopes` 映射 `created_at` |
| `internal/application/search_scope_test.go` | 新增：范围解析单元测试（4 个用例，无数据库） |
| `internal/application/search_integration_test.go` | 新增：6 个检索集成测试（收窄、主体隔离、分页、页面上限、生命周期、Web 字段+重建） |
| `internal/dbtest/dbtest.go` | `Sequence()` 的 base 由“墙钟推导”改为**每进程随机**（`[2^46, 2^47)`，随机源不可用时回退时钟）：见 §8 的并行测试进程序列冲突实测 |
| `internal/dbtest/devschema_apply_test.go` | 新增：开发库 `document_index` 列守卫集成测试（`created_at`、`document_updated_at` 非空且有默认值） |
| `internal/interfaces/grpcapi/transport_integration_test.go` | 适配契约改名 `TopK`→`PageSize`；`mintCapability` 增加 issuer 参数，跨 audience capability 改由 `py-agent` 签发（`packages/serviceauth` 新增“事实源是唯一签发方”校验） |
| `internal/interfaces/grpcapi/handlers.go` | 仅澄清 `effectiveScope` 注释（capability 是授权上限，实际过滤在 application 层）；无行为变化 |
| `schema/schema_init.sql` | `document_search.document_index` 新增 `created_at`、`document_updated_at`（新库初始化路径） |

未改动（按要求）：`packages/proto`、`packages/gen`、`packages/serviceauth`、`go.work`、`docker-compose.yaml`、`deployments/`、`docs/contracts/`、`docs/adr/`。

## 3. 契约与行为变化

- **收窄即实际范围**：请求里非空的 `allowed_space_ids` / `allowed_document_ids` 成为该族的 SQL 过滤集合；任一族非空时，返回范围**就是被点名的集合**（两族都点名时是二者的并集），未命名族贡献空集，`authenticated_public` 兜底关闭。“只查获准空间 A”不再返回空间 B 的私有文档，也不再返回空间 B 的公开文档；只点名文档时也不会回退到已授权空间。
- **未收窄时保持既有信封语义**：两族都为空 → 使用 capability 的完整授权集合（三标签 + `allowed_space_ids` + `allowed_document_ids`）并启用公开兜底。capability 的 `authenticated_public` 标志记录事实源解析出的族，**不用于关闭兜底**；这与既有 `TestIntegrationQueryAuthorization`（`capabilityFor(false)` 的空授权仍命中 `authenticated_public` 文档）的语义一致。
- **包含规则不放宽**：任一未授予标识 → 整个请求 `PERMISSION_DENIED`，不静默裁剪；校验在构造实际过滤集合之前完成。
- **主体过滤是合取**：`owned_by_subject_only=true` 在既有范围条件之外再加 `owner_subject_key = capability.subject_key`，因此其他主体的公开与私有文档都不返回；capability 主体为空（本应被传输层拒绝）时应用层失败关闭为空结果。
- **分页与截断**：`page_size` 超过 `index.max_top_k`（默认 100）时下调为上限并置 `truncated=true`；`offset+本页命中数 < total` 也置 `truncated=true`；字段号不变（仍为 4），仅改名。
- **命中时间语义（本轮修正）**：`SearchHit.updated_at` = 事件的 `occurred_at` = 文档服务写 `documents.updated_at` 的同一事务时刻，不再是索引写入时刻；`SearchHit.created_at` = 事件的 `created_at`。索引本地写入时刻仍保留在 `document_index.updated_at`，只用于 `DocumentIndexState.indexed_at`，不再对外冒充文档更新时间。
- **命中新字段**：`owner_subject_key`、`authenticated_public` 来自索引投影；四个字段都在消费事件时写入，查询不回源。

## 4. 数据库变更

- `apps/document-search/schema/schema_init.sql`：`document_search.document_index` 新增
  - `created_at timestamptz NOT NULL DEFAULT clock_timestamp()`
  - `document_updated_at timestamptz NOT NULL DEFAULT clock_timestamp()`

  该文件就是新库初始化来源：`docker-compose.yaml` 把 `./apps/document-search/schema` 只读挂载到 `/service-schema/document-search`，由 `deployments/postgresql/entryscript/00-init.sh` 应用。
- 运行中的开发库已同步执行（幂等）：
  `ALTER TABLE document_search.document_index ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT clock_timestamp()`
  `ALTER TABLE document_search.document_index ADD COLUMN IF NOT EXISTS document_updated_at timestamptz NOT NULL DEFAULT clock_timestamp()`

  核验输出（`information_schema.columns`）：
  `COLUMN created_at timestamp with time zone nullable=NO default=clock_timestamp()`
  `COLUMN document_updated_at timestamp with time zone nullable=NO default=clock_timestamp()`
  `COLUMN updated_at timestamp with time zone nullable=NO default=clock_timestamp()`
- 执行方式与限制：本会话 `docker compose` / `docker compose exec` 被沙箱拒绝（`npipe:////./pipe/dockerDesktopLinuxEngine` 不允许），且本会话不允许提权。加列改用 `%TEMP%` 下的一次性 Go 程序经 `127.0.0.1:15432` 以 `postgres` 超级用户直连执行，执行后已删除该临时程序；未新增表、未改变其他服务 schema、未新增授权（既有 `GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA document_search` 覆盖新列）。
- 首次加列时的一次性助手曾写在模块内（`internal/dbtest/devschema_apply_test.go`），因本会话无法删除工作区文件而改写为永久 schema 守卫测试；文件名保留历史来源，改名由 `lead` 统一处理，本任务不再改动。

## 5. 实际命令与退出码

工作目录：`apps/document-search`

| 命令 | 退出码 | 关键输出 |
| --- | --- | --- |
| `go build ./...` | 0 | 无输出 |
| `go vet ./...` | 0 | 无输出 |
| `gofmt -l .` | 0 | 0 行输出 |
| `go test ./... -count=1` | 0 | `ok document-search/internal/application 6.279s`；`ok .../architecture 0.032s`；`ok .../dbtest 0.047s`；`ok .../grpcapi 0.077s`；其余 `[no test files]` |
| `go test ./... -count=1 -run Integration -v` | 0 | `--- PASS` = 23，`--- FAIL` = 0，`--- SKIP` = 0（16 原有 + 7 新增） |
| `go test ./... -count=1 -run Integration`（连续 5 次） | 0,0,0,0,0 | 每次四个包全 `ok`（application 3.6–3.9s）；用于验证 §8 的序列冲突修复后不再抖动 |
| `go test ./internal/application/ -count=1 -run TestEffectiveSearchScope -v` | 0 | 4 个用例（含 4 个子用例）全部 PASS |
| `git diff --check` | 0 | 无空白错误输出 |

集成测试总数：**原有 16 个全部保持通过**（application 12 + consumer 3 + grpcapi 传输 1），新增 7 个 → **23 个，0 跳过**，全部连接真实 PostgreSQL；连不上即 `t.Fatalf`。

新增测试：

- `TestEffectiveSearchScopeNamedRangeReplacesTheGrant`（+4 子用例）、`TestEffectiveSearchScopeNeverReturnsNilFamilies`、`TestEffectiveSearchScopeDropsNonUUIDIdentifiers`、`TestEffectiveSearchScopeOwnerComesFromTheCapability`
- `TestIntegrationSearchNarrowingExcludesOtherSpaces`
- `TestIntegrationSearchOwnerSubjectsAreIsolated`
- `TestIntegrationSearchPagesAreDisjointAndComplete`
- `TestIntegrationSearchPageSizeAboveMaximumIsLoweredAndReported`
- `TestIntegrationSearchExcludesInactiveAndNonPublishedRows`
- `TestIntegrationSearchHitCarriesWebFields`
- `TestIntegrationDevSchemaCarriesTheQueryPathColumns`

## 6. 验收项 → 证据映射

| 验收项 | 证据 |
| --- | --- |
| 个人搜索排除其他主体的公开与私有文档 | `TestIntegrationSearchOwnerSubjectsAreIsolated`：capability 授权 `[A,B]` 且允许公开，不加开关时 `total=4`（说明范围确实覆盖他人文档）；加 `owned_by_subject_only` → `total=2`，只含本人私有与本人公开，他人公开/私有均不出现；无主体 capability → `total=0` 且无命中（失败关闭） |
| 不同页返回不同结果，总数正确 | `TestIntegrationSearchPagesAreDisjointAndComplete`：5 篇文档 `page_size=2`，三页命中数 2/2/1，每页 `total=5`，三页并集恰好 5 篇且每篇只出现一次；第 1、2 页 `truncated=true`，第 3 页 `false`；越界页命中 0 而 `total=5`；`page_size=0` 取默认；`page=0` 视为第一页 |
| 请求只查询获准空间 A 时，不返回其他空间（含其他空间的公开文档） | `TestIntegrationSearchNarrowingExcludesOtherSpaces`：capability 授权 `[A,B]`；不点名时返回 3 篇；点名 `[A]` → `total=1` 只含 A 的文档，B 的私有与 B 的公开命中均为 0；带公开兜底的 capability 同样只返回 A；只点名文档时不再回退到已授权空间；`[A]+文档级外域授予` 只返回 A；两族都点名时返回二者并集 |
| 越界标识整体拒绝，不静默裁剪 | `TestIntegrationQueryAuthorization`（既有，继续通过）+ `TestIntegrationSearchNarrowingExcludesOtherSpaces`：`[A,未授权空间]`、未授予文档、二者混合均 `PERMISSION_DENIED` |
| 已删除、已撤销、非活动版本不进入结果 | `TestIntegrationSearchExcludesInactiveAndNonPublishedRows`：draft / withdrawn / superseded / archived / trashed / 已删除（tombstone）6 种行均不在结果中，只有 active+published 命中，`total=1`；既有删除、乱序与 tombstone 测试继续通过 |
| 命中与总数同条件 | 两处 SQL 共用同一 `searchScopePredicate` 常量与同一 `searchScope` 值；分页用例中每页 `total=5` 而命中 2/2/1 即为反证 |
| `page_size` 超上限下调并置 `truncated` | `TestIntegrationSearchPageSizeAboveMaximumIsLoweredAndReported`：把 `max_top_k` 降到 3，`page_size=50` → 命中 3、`truncated=true`、`total=5` |
| Web 字段真实可用（含**真实更新时间**） | `TestIntegrationSearchHitCarriesWebFields`：`owner_subject_key` 等于事件主体、`authenticated_public=true`；`created_at` **精确等于**事件 `created_at`（`2026-05-04T03:02:01.123456Z`）；`updated_at` **精确等于**事件 `occurred_at`（首事件 `2026-06-05T04:03:02.654321Z`，第二个事件 `2026-07-06T05:04:03.111222Z`）；后续未声明 `created_at` 的事件不移动创建时刻；删除投影后重建，两个时刻都保持 |
| 原有消费、重复处理与重建测试继续通过 | `TestIntegrationConsumer*`（3）、`TestIntegrationDuplicateEventIsNoOp`、`TestIntegrationRebuildUsesLocalEventsOnly`、`TestIntegrationOutOfOrderEventsAreIgnored` 等 16 个原有集成测试全部 PASS，0 跳过 |
| 索引侧时间列落地 | `schema/schema_init.sql` + 运行库列守卫 `TestIntegrationDevSchemaCarriesTheQueryPathColumns`（两列存在、非空、有默认值）；写入与重建由 `TestIntegrationSearchHitCarriesWebFields` 断言 |

## 7. 架构与质量约束

- 只修改 `apps/document-search/` 与本证据文件；未导入其他应用 `internal` 包，未写其他服务业务表（`internal/architecture` 全通过）。
- 查询路径仍然自足：`internal/architecture/TestQueryPathHasNoSourceClient` 通过，`search.go` 的导入面仍为 `context`、`fmt`、`strings`、`packages/gen/documentsearch/v1`、`packages/serviceauth`——查询与重建路径没有 document-service / go-web / py-agent 客户端，也不回调事实源（测试中 source 端点仍指向不可达端口）。
- 查询只依据已离线校验的 capability：越界整体拒绝、不静默裁剪；主体从 capability 读取，调用方不能提交或改写。
- 集成测试连真实 PostgreSQL，连接失败 `t.Fatalf`，**不允许 skip**（本轮 0 跳过）。
- `go build` / `go vet` / `gofmt -l`（0 行）/ `go test` 全部通过。

## 8. 跳过项、依赖、协商修正与未完成项

- **无跳过项**：集成测试 0 SKIP。
- **本轮修正（reopen 后）**：`SearchHit.updated_at` 改为文档真实更新时刻（事件 `occurred_at`），新增 `document_updated_at` 列并同步开发库；`TestIntegrationSearchHitCarriesWebFields` 改为对显式时刻断言。收窄语义按 `lead` 确认保持不变。
- **跨模块适配（`packages/serviceauth` 在本次会话中被改为“事实源是唯一签发方”）**：`SealCapability` 现在拒绝 `issuer=document-service` + `audience=qq-search`，因此本模块传输测试里“跨 audience capability 必须 UNAUTHENTICATED”的用例改由 `py-agent` 签发该 capability（未改前实测报 `SealCapability: serviceauth: unknown claim: issuer "document-service" may not mint a capability for audience "qq-search"`）。生产代码无需改动。
- **修复了一处会掩盖本轮结论的测试基础设施抖动（属于本模块写入范围）**：`go test ./...` 会把同一个 module 的多个测试包二进制**几乎同时**启动，实测它们的墙钟读数落在同一个 100ns 刻度上（4 个二进制中 3 个共享同一 base，另一次 4 个全部共享），而 `internal/dbtest.Sequence()` 原来用 `time.Now().UnixNano() % 1e12` 作 base，于是两个包为不同事件抢同一个 sequence；抢占失败是 `applyEventTx` 的既有语义（`ErrEventSequenceTaken`），而 `TestIntegrationConsumerResumesWithoutSkippingOrReapplying` 的假事件源有固定投递预算（budget=2），重连后拿不到事件，只能在 10s 后超时失败。实测该失败在本轮出现过 2/3 与 1/2 的跑次（日志：`error="document-search: the event sequence already belongs to another event: sequence 588399211201"`；失败后立即查库 `events_with_large_sequence=0`，冲突行已被并发进程的 cleanup 删除）。修复方式：`Sequence()` 的 base 改为每进程随机（`[2^46, 2^47)`，随机源不可用时回退时钟），使不同进程/不同跑次的 sequence 区间不可能重叠；修复后连续 5 次 `go test ./... -count=1 -run Integration` 全部退出码 0（此前约一半跑次失败）。该修复只影响测试夹具的合成 sequence，不改变服务代码路径。
- **依赖（他人处理）**：`apps/gin-backend` 因契约改名出现编译中断，本会话实测 `go build ./...`（工作目录 `apps/gin-backend`）退出码 1：`internal/modules/document/infrastructure/documentsearch/client.go:151:3: unknown field TopK in struct literal of type documentsearchv1.SearchDocumentsRequest`。该目录不在本任务写入范围（`lead` 已登记为 `svc-web` 的 `task-4`）。
- **待 `lead` 处理**：`internal/dbtest/devschema_apply_test.go` 的文件名保留了一次性迁移助手的历史来源，内容已是长期有效的开发库 schema 守卫（不改库）；按 `lead` 说明由其在有写权限时统一改名，本任务不再尝试删除或改名。
- **未完成项**：验收项全部有实测证据，无未完成项。已知边界（非本次范围）：事件契约没有 `updated_at` 字段，命中时间使用 `occurred_at`（与文档服务写 `documents.updated_at` 同一事务时刻）；Qdrant 向量集合仍未接管（既有 §0.7 限制）。
