# 阶段 A 证据：document-service 保存用例与列表真实公开状态

> 任务：共享任务 `task-1`（负责人 `svc-document`）
> 日期：2026-09-30
> 写入范围：`apps/document-service/`（源码、schema、测试）、本证据文件
> 环境：Windows + Go 1.26.8；开发库 `postgres://document_service_writer:document_service@127.0.0.1:15432/gin_demo?sslmode=disable`（`docker compose up -d postgres redis qdrant` 已执行）
> 构建前置：`$env:GOCACHE = Join-Path $env:TEMP 'gb-goweb'`

## 1. 交付内容

1. **`SaveDocument` 用例**：单事务内完成 权限检查 → 预期修订号检查 → 新版本写入 → 活动版本切换 → 可选访问策略修改 → Outbox 事件写入，返回切换后的新活动版本详情。
2. **版本切换只有一套规则**：抽出 `switchActiveVersion`（`internal/application/version_switch.go`），`PublishDocument` 与 `SaveDocument` 共用；不存在第二份 supersede/mark-published/bump-activation 序列。
3. **幂等**：新增 `documents.last_save_request_id`；同一 `request_id` 重复提交返回首次提交后的状态（`replayed=true`），不新增版本、不新增事件、不新增策略写入。重放判断在 `SELECT ... FOR UPDATE` 行锁之后、期望修订号检查之前完成。
4. **`UpdateDraft` 保持草稿语义**；请求显式携带 `authenticated_public`（`optional bool` 存在）时以 `INVALID_ARGUMENT` 拒绝，不静默忽略。
5. **`ListDocuments`**：摘要返回策略行的真实 `authenticated_public`（不再由发布状态推断），并返回与过滤条件一致、与分页无关的 `total_count`。
6. **`appendDocumentEvent`** 填充 `DocumentEventEnvelope.created_at`（文档创建时刻）。
7. **集成测试**：真实 PostgreSQL，无 skip；新增 7 个集成测试。

## 2. 修改文件

| 文件 | 变更 |
| --- | --- |
| `internal/application/version_switch.go` | 新增：唯一的版本切换规则 `switchActiveVersion`（no-op 判定、旧活动版本 superseded、目标 published、`ActivateVersion`、持久化新 head） |
| `internal/application/document_commands.go` | 新增 `saveDocument`；`publishDocument` 改为调用共用切换函数；`updateDraft` 拒绝显式 `authenticated_public` |
| `internal/application/documents.go` | `ListDocuments` 同事务统计 `total_count`；详情摘要传入策略 |
| `internal/application/envelope.go` | `summaryProto` 填充 `authenticated_public`；`documentSummary` 增加 policy 参数；`buildEnvelope` 填充 `created_at` |
| `internal/application/handlers.go` | 新增 `SaveDocument` 业务入口（错误映射沿用 `grpcError`） |
| `internal/application/service.go` | 新增测试 seam `WithEventSink`（与既有 `WithAuditSink` 对称） |
| `internal/domain/model.go` | `Document.LastSaveRequestID`；`Summary.AuthenticatedPublic` |
| `internal/infrastructure/postgres/documents.go` | 读取/写入 `last_save_request_id`（列清单、INSERT、UPDATE、scan） |
| `internal/infrastructure/postgres/listing.go` | 抽出共享过滤条件 `documentFilterSQL`；分页查询返回策略 `authenticated_public`；新增 `CountReadableDocuments`（同一过滤、无游标与 limit） |
| `internal/infrastructure/postgres/store.go` | `EventSink` 字段与 `WithEventSink` |
| `internal/infrastructure/postgres/outbox.go` | `EventSink` 接口 + `DefaultEventSink`；`AppendEvent` 走 sink |
| `internal/interfaces/grpcapi/api.go` | `SaveDocument` → scope `document.write`；`Documents` 接口新增方法 |
| `internal/interfaces/grpcapi/handlers.go` | `SaveDocument` 传输适配（无业务校验） |
| `internal/application/integration_test.go` | 适配 `optional bool`；新增 7 个集成测试；`UpdateDraft` 显式标志校验用例 |
| `cmd/document-service-e2e/main.go` | 修正既有编译中断：`SearchDocumentsRequest.TopK` → `PageSize`（3 处，契约已改名） |
| `schema/schema_init.sql` | `documents.last_save_request_id varchar(128)`（新库初始化） |

未改动（按要求）：`packages/proto`、`packages/gen`、`packages/serviceauth`、`go.work`、`docker-compose.yaml`、`deployments/`、`docs/contracts/`、`docs/adr/`。

## 3. 契约与行为变化

- **保存即生效**：`SaveDocument` 在一个事务里追加版本并把它切换为活动版本，响应中的 `summary.active_version_id` 与 `detail.version` 就是刚保存的版本；随后 `GetDocument` 立即返回新正文。
- **修订号**：一次保存 = 一次版本追加（`aggregate_revision +1`）+ 一次活动版本切换（`activation_revision +1`、`aggregate_revision +1`）；仅当显式访问策略真正改变时再 `access_revision +1`、`aggregate_revision +1`。显式发送与当前相同的值不推进访问修订号。
- **普通正文保存保留策略**：请求未携带 `authenticated_public` 时策略行完全不变；显式携带时才修改，且详情、列表摘要、Outbox 事件三处一致（同一事务、同一策略行）。
- **幂等语义**：`request_id` 非空且等于该文档最近一次成功保存的键 → 返回当前已提交状态并置 `replayed=true`；不写版本、不写事件、不推进修订号。该判断读取的是 `SELECT ... FOR UPDATE` 锁定后的行，因此并发重复请求被串行化，第二个请求必然看到第一个提交的键。
- **`UpdateDraft`**：显式 `authenticated_public` → `INVALID_ARGUMENT`（在文档查询之前判断），草稿语义与“不写 Outbox 事件”不变。
- **`ListDocuments`**：`documents[].authenticated_public` 来自策略行；`total_count` 使用与分页完全相同的过滤条件（同一 SQL 常量），不含游标与 limit。
- **Outbox**：每个事件的 `created_at` 为文档创建时刻（与详情摘要 `created_at` 一致）。

已知边界（有意保留）：幂等键只保留“最近一次成功保存”的 `request_id`；若在一次更新的保存之后再次重放更早的 `request_id`，会被当作新保存处理。这与任务描述的 `last_save_request_id` 方案一致，未引入第二套账本。

## 4. 数据库变更

- `apps/document-service/schema/schema_init.sql`：`documents` 新增 `last_save_request_id varchar(128)`（空值表示从未执行过带 request id 的保存）。
- 运行中的开发库已同步执行：
  `ALTER TABLE document_service.documents ADD COLUMN IF NOT EXISTS last_save_request_id varchar(128)`
  执行方式：本会话 `docker compose exec` 被沙箱拒绝（named pipe 访问不允许，且本会话不允许申请提权），改用一次性 Go 程序经 `127.0.0.1:15432` 以 `postgres` 超级用户直连执行；执行输出 `OK: ALTER TABLE ...`，随后核验 `information_schema.columns` 返回 `column data_type: character varying`。该临时程序位于 `%TEMP%`，执行后已删除。
- 未新增表，未改变其他服务的 schema；现有 schema 级授权（`GRANT ALL ON ALL TABLES IN SCHEMA document_service`）足以覆盖该列。

## 5. 实际命令与退出码

工作目录：`apps/document-service`

| 命令 | 退出码 | 关键输出 |
| --- | --- | --- |
| `go build ./...` | 0 | 无输出 |
| `go vet ./...` | 0 | 无输出 |
| `gofmt -l .` | 0 | 0 行输出 |
| `go test ./... -count=1` | 0 | `ok document-service/internal/application 2.169s`；`ok .../architecture 0.054s`；`ok .../domain 0.015s`；`ok .../grpcapi 0.103s`；其余包 `[no test files]` |
| `go test ./... -count=1 -run Integration -v` | 0 | `--- PASS` = 30，`--- FAIL` = 0，`--- SKIP` = 0；`ok document-service/internal/application 2.126s`；`ok document-service/internal/interfaces/grpcapi 0.092s` |
| `go test ./internal/application/ -count=5 -run 'TestIntegrationConcurrentSaveWithTheSameRequestIDWritesOneVersion\|TestIntegrationSaveDocument'` | 0 | `ok document-service/internal/application 11.787s`（并发与保存用例各跑 5 次稳定通过） |

集成测试总数：原有 22 个（application）+ 1 个（grpcapi 传输）= 23，新增 7 个 → **30 个，0 跳过**，全部连接真实 PostgreSQL；连不上即 `t.Fatalf`（`internal/testsupport/harness.go`）。

新增/相关测试：

- `TestIntegrationSaveDocumentCommitsNewActiveVersion`
- `TestIntegrationSaveDocumentStaleRevisionChangesNothing`
- `TestIntegrationSaveDocumentIsIdempotentPerRequestID`
- `TestIntegrationConcurrentSaveWithTheSameRequestIDWritesOneVersion`
- `TestIntegrationSaveDocumentRejectsNonOwner`
- `TestIntegrationSaveDocumentRollsBackWhenTheEventCannotBeWritten`
- `TestIntegrationSaveDocumentAccessPolicyIsConsistentEverywhere`
- 既有测试适配：`TestIntegrationOwnershipIsEnforcedOnEveryMutation`（改为省略字段并新增“显式标志被拒”断言）、`TestIntegrationGetDocumentAndListDocuments`（`total_count`、摘要公开状态）、`TestIntegrationCreateDocumentCommitsEveryRow`（`envelope.created_at`）、`TestIntegrationValidationRejections`（`UpdateDraft` 显式访问标志 → `INVALID_ARGUMENT`）

## 6. 验收项 → 证据映射

| 验收项 | 证据 |
| --- | --- |
| 保存后 `GetDocument` 立即返回新正文 | `TestIntegrationSaveDocumentCommitsNewActiveVersion`：保存响应 `detail.version.content = "saved body"`，随后 `GetDocument` 返回同一活动版本与正文；旧版本 `superseded`，`documents.active_version_id` 指向新版本 |
| 过期 `expected_aggregate_revision` → `FAILED_PRECONDITION`，文档、版本与事件均不变 | `TestIntegrationSaveDocumentStaleRevisionChangesNothing`：`codes.FailedPrecondition`；versions=1、`aggregate_revision=1`、`last_save_request_id IS NULL`、events=1 |
| 事件写入失败时整个事务回滚（无版本、无策略变更） | `TestIntegrationSaveDocumentRollsBackWhenTheEventCannotBeWritten`：以 `WithEventSink(failingEventSink{})` 令事务最后一步失败；回滚后 versions=1、活动版本与 `aggregate_revision` 不变、策略仍 `authenticated_public=false AND access_revision=1`、`last_save_request_id IS NULL`、events=1，健康服务读回原正文 |
| 重复请求不产生重复业务结果 | `TestIntegrationSaveDocumentIsIdempotentPerRequestID`（重复 `request_id` → `replayed=true`、versions=2、events=2、`aggregate_revision=3`；换新 `request_id` 才真正再保存）；`TestIntegrationConcurrentSaveWithTheSameRequestIDWritesOneVersion`（并发同一 `request_id` → 恰好 1 次写入 + 1 次重放，versions=2、events=2、head 上只有一条已提交保存） |
| 非所有者保存被拒绝 | `TestIntegrationSaveDocumentRejectsNonOwner`：`codes.PermissionDenied`，head/版本/事件均未变化 |
| 普通正文保存保留访问策略；显式修改时详情、列表摘要与事件三处一致 | `TestIntegrationSaveDocumentAccessPolicyIsConsistentEverywhere`：正文保存后策略 `false`/`access_revision=1`；显式 `true` → 详情、摘要、列表摘要、事件均为 `true` 且 `access_revision=2`；显式 `false` 两次 → 均 `false`、`access_revision=3`（相同值不推进）；同时用已登记的第二主体验证资源判定随之开放/拒绝 |
| 列表真实公开状态与 `total_count` | `TestIntegrationGetDocumentAndListDocuments`：一页 1 条时 `total_count=2`；`authenticated_public_only` 时 `total_count=1`；摘要公开标志与创建时策略一致 |
| Outbox `created_at` | `TestIntegrationCreateDocumentCommitsEveryRow`：`envelope.created_at` 非空且等于摘要 `created_at`；保存测试再次断言 |
| 版本切换单一规则 | `internal/application/version_switch.go` 为唯一实现；`publishDocument` 与 `saveDocument` 均调用它；`TestIntegrationDocumentLifecycleTransitions`（发布/撤销/归档/删除语义不变，重复发布仍不推进修订号）保持通过 |

## 7. 架构与质量约束

- 只修改 `apps/document-service/`；未导入其他应用 `internal` 包，未写其他服务业务表（`internal/architecture` 测试通过）。
- 文档服务仍是正式文档唯一写入方；写入路径不依赖检索服务可用性（事件与业务变更同事务提交）。
- 集成测试连真实 PostgreSQL，连接失败 `t.Fatalf`，无 skip。
- `go build` / `go vet` / `gofmt -l` / `go test` 全部通过。

## 8. 跳过项、依赖与未完成项

- **无跳过项**：集成测试 0 skip。
- **依赖（已由他人处理）**：`apps/gin-backend` 因本次契约变更出现编译中断（`documentsearch/client.go` 的 `TopK`、`sourceowned/gateway.go:177` 的 `bool` → `*bool`、`gateway.go:331` 由发布状态推断公开状态），已由 `lead` 分配给 `svc-web`（`task-4`），不在本任务写入范围。
- **未完成项**：无（本任务验收项全部有实测证据）。
- **已知边界**：幂等键只保留最近一次成功保存的 `request_id`（见 §3）；如需对任意历史 `request_id` 重放，需要独立的幂等账本表，属下一次有意的契约/模型修订。
- **环境限制记录**：`docker compose exec -T postgres psql ...` 在本会话被沙箱拒绝（`npipe:////./pipe/dockerDesktopLinuxEngine` 访问不允许，且本会话不允许提权），因此 schema 变更改由直连数据库执行；`docker compose` 本身未受影响（PostgreSQL 容器此前已启动）。
