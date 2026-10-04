# F02-Web：检索总数/截断语义的 HTTP 与前端接线

> 负责人：teammate `svc-web`（共享任务 `task-9`）
> 写入范围：`apps/gin-backend/`、`apps/simple-frontend/`、本文件
> 日期：2026-09-30
> 上游：task-8（`fix-search`）把 `document-search` 的 `total` 改为「真正可翻页读到的结果数」，`truncated` 改为「本页之后还有结果」；主 Agent 在 `packages/gen/document/v1` 重新生成了 `SaveDocumentResponse.applied_version_id`
> 上一轮证据：[stage-a-go-web.md](./stage-a-go-web.md)

## 1. 结论

任务 9 的 6 项交付全部落地：

1. **不再展示读不到的结果页**：`meta.total_pages` 由检索返回的真实 `total` 计算（`NewCursorPageMeta`），前端 `hasNext = activePage < total_pages`，并在旧链接/结果集缩小时把越界页码收敛回最后一页。
2. **截断状态透传**：HTTP 响应新增 `meta.truncated`（`omitempty`），前端在检索结果被截断时显示明确提示，不静默隐藏。
3. **保存路径跟进契约**：`replayed` 与 `appliedVersionId` 经 gateway 透传到 HTTP 响应；编辑页在 `replayed=true` 时提示「本次保存此前已生效」而不是「刚刚保存成功」。
4. **阶段 A 行为未回退**：列表游标分页（`cursor`/`meta.nextCursor`）、个人搜索 `owned_by_subject_only=true`、真实公开状态与时间、非所有者 403、`visibility` 显式传递全部保留，并有回归测试覆盖。

后端 `go build ./...`、`go vet ./...`、`gofmt -l .`、`go test ./... -count=1` 全绿；前端 `vue-tsc` 两个 project 类型检查 0 错误、阶段边界脚本 PASS。

**未完成项**：前端生产构建（`npm run build`）在本会话沙箱内无法执行（详见 §6）；真实链路复核本轮未跑（PostgreSQL/Qdrant/Redis 在本会话不可达，详见 §5.3），改用真实 HTTP handler + 真实 gRPC 客户端 + 进程内假服务的链路测试覆盖同一批断言。

## 2. 交付对照

| # | 交付 | 落点 | 验证用例 |
| --- | --- | --- | --- |
| 1 | 不展示读不到的结果页 | `responses.NewCursorPageMeta`（真实 total → total_pages）、`LibraryView.vue`（`hasNext`、越界页收敛） | `TestSearchTruncationAndReadablePageBoundaries`、`TestSearchExposesTruncationAndNeverAdvertisesAnUnreadablePage`、`TestSearchOutOfRangePageKeepsTotalAndDropsTruncation` |
| 2 | 截断状态透传 + 前端提示 | `domain.Page.Truncated`、`gateway.Search`、`responses.Meta.Truncated`、`adapter.writeList`、`types/api.ts`、`LibraryView.vue` 的 `.search-notice` | `TestSearchForwardsTotalAndTruncated`、`TestSearchExposesTruncationAndNeverAdvertisesAnUnreadablePage` |
| 3 | `replayed` + `appliedVersionId` 透传 | `gateway.MutationResult`、`documentservice` 客户端（返回完整 `SaveDocumentResponse`）、`adapter.update`、`types/domain.ts` 的 `DocumentSaveResult`、`api/document.ts`、`MarkdownEditorView.vue` | `TestUpdateCarriesReplayAndAppliedVersion`、`TestUpdateRendersReplayAsAlreadyApplied`、`TestUpdateSavesThroughSaveDocumentAndReturnsTheNewBody`、`TestUpdateReplaysSameRequestID` |
| 4 | 阶段 A 行为不回退 | 未改动相关路径 | 上轮全部用例仍通过（`TestCursorPagingOverTwentyFiveDocuments`、`TestSearchPagingUsesRealTotalAndOwnershipFilter`、`TestListRendersPolicyVisibilityNotPublicationStatus`、`TestUpdateByNonOwnerIsForbidden`、`TestUpdateRejectsMissingVisibility` 等） |
| 5 | 类型、接口与测试更新 | 见 §3 | `go test ./...` |
| 6 | 前端类型检查与生产构建 | 类型检查通过；生产构建受沙箱限制（§6） | `vue-tsc --noEmit` ×2 项目 |

## 3. 修改文件

后端 `apps/gin-backend/`：

- `internal/modules/document/domain/query.go`：`Page` 新增 `Truncated bool`（检索专用；列表保持 false）。
- `internal/modules/document/interfaces/sourceowned/gateway.go`：
  - `Search` 透传 `SearchDocumentsResponse.total` 与 `truncated`（不再自行推导页数）；
  - `MutationResult` 新增 `AppliedVersionID`，`Update` 从 `SaveDocumentResponse.applied_version_id` 取值。
- `internal/modules/document/interfaces/sourceowned/adapter.go`：
  - `writeList` 把 `page.Truncated` 写入 `meta.truncated`；
  - PUT 响应新增 `appliedVersionId`（与既有 `replayed` 并列）。
- `internal/common/base/responses/response.go`：`Meta` 新增 `Truncated bool \`json:"truncated,omitempty"\``。
- 测试：
  - `gateway_test.go`：新增 `TestSearchForwardsTotalAndTruncated`、`TestListPagesAreNotTruncated`、`TestUpdateCarriesReplayAndAppliedVersion`。
  - `adapter_test.go`：`listEnvelope.Meta` 增加 `truncated`；新增 `TestSearchExposesTruncationAndNeverAdvertisesAnUnreadablePage`、`TestSearchOutOfRangePageKeepsTotalAndDropsTruncation`、`TestUpdateRendersReplayAsAlreadyApplied`；`TestUpdateSendsExplicitPolicyRevisionAndRequestID` 增加 `appliedVersionId`/`replayed` 断言。
  - `service_chain_test.go`（真实 HTTP handler + 真实 gRPC 客户端 + 进程内假服务）：新增 `TestSearchTruncationAndReadablePageBoundaries`；假检索服务按 F02 语义实现（`truncated = offset+len(hits) < total`，越界页空命中且 total 不变）；假 `SaveDocument` 返回 `applied_version_id` 并在重放时回放首次版本；`TestUpdateSavesThroughSaveDocumentAndReturnsTheNewBody`、`TestUpdateReplaysSameRequestID` 增加断言。
  - `real_services_test.go`（可选真实链路）：搜索用例增加 `truncated`/越界页/每个可读页非空断言，保存用例增加 `appliedVersionId` 与重放断言。

前端 `apps/simple-frontend/`：

- `src/types/api.ts`：`ApiMeta.truncated?`。
- `src/types/domain.ts`：新增 `DocumentSaveResult`（`replayed`、`appliedVersionId`）。
- `src/api/document.ts`：`update` 返回 `DocumentSaveResult`。
- `src/views/app/LibraryView.vue`：检索结果被截断时显示 `.search-notice`；`meta.total_pages` 由真实 total 推导；越界页码（旧链接/结果集缩小）`router.replace` 收敛到最后一页后重读。
- `src/views/app/MarkdownEditorView.vue`：`replayed=true` 时按「本次请求已生效过」提示（含 `appliedVersionId`），不再宣称刚刚保存成功。

## 4. 契约与行为变化

1. `GET /api/v1/protected/documents/search` 的 `meta` 新增 `truncated`（仅在为 true 时出现）：表示检索结果未完整展示（本页之后还有结果）。`meta.total` / `meta.total_pages` 始终来自检索返回的真实可翻页结果数。
2. `PUT /api/v1/protected/documents/:id` 的响应新增 `appliedVersionId`（与既有 `replayed`、`revision` 并列）：`replayed=true` 表示该 `requestId` 此前已生效、本次未写入新版本，`appliedVersionId` 是首次尝试写入的版本。
3. 列表接口（`mine`/`public`）行为不变，`truncated` 恒为 false（游标列表没有“被截断”的概念，下一页由 `meta.nextCursor` 表达）。
4. 前端在检索页新增截断提示；越界页码不再作为结果页展示。

## 5. 实际命令、退出码与关键输出

### 5.1 后端（工作目录 `apps/gin-backend`，`$env:GOCACHE = Join-Path $env:TEMP 'gb-goweb'`）

```text
gofmt -l .                -> GOFMT_CLEAN（0 个文件）
go build ./...            -> exit 0
go vet ./...              -> exit 0
go test ./... -count=1    -> exit 0（含 ok gin-backend/internal/modules/document/interfaces/sourceowned、
                              ok gin-backend/internal/common/base/responses 等）
```

F02 新增用例（`-run` 过滤，全部 PASS）：

```text
=== RUN   TestUpdateRendersReplayAsAlreadyApplied
--- PASS: TestUpdateRendersReplayAsAlreadyApplied (0.00s)
=== RUN   TestSearchExposesTruncationAndNeverAdvertisesAnUnreadablePage
--- PASS: TestSearchExposesTruncationAndNeverAdvertisesAnUnreadablePage (0.00s)
=== RUN   TestSearchOutOfRangePageKeepsTotalAndDropsTruncation
--- PASS: TestSearchOutOfRangePageKeepsTotalAndDropsTruncation (0.00s)
=== RUN   TestSearchForwardsTotalAndTruncated
--- PASS: TestSearchForwardsTotalAndTruncated (0.00s)
=== RUN   TestListPagesAreNotTruncated
--- PASS: TestListPagesAreNotTruncated (0.00s)
=== RUN   TestUpdateCarriesReplayAndAppliedVersion
--- PASS: TestUpdateCarriesReplayAndAppliedVersion (0.00s)
=== RUN   TestSearchTruncationAndReadablePageBoundaries
--- PASS: TestSearchTruncationAndReadablePageBoundaries (0.01s)
PASS
ok  	gin-backend/internal/modules/document/interfaces/sourceowned	0.030s
```

`TestSearchTruncationAndReadablePageBoundaries` 走真实 HTTP handler + 真实 gateway + 真实 gRPC 客户端（假服务按 F02 语义），断言：

- 第 1/2 页 `meta.truncated=true`，最后一页 `meta.truncated=false`；
- `meta.total=25`、`meta.total_pages=3`，第 1..3 页都返回结果（9/9/7）；
- 第 4 页（越界）命中为空，`total` 不变、`truncated=false`。

### 5.2 前端（工作目录 `apps/simple-frontend`）

```text
node scripts/verify-phase-boundary.mjs          -> exit 0  PHASE_BOUNDARY=PASS
npx vue-tsc --noEmit -p tsconfig.app.json      -> exit 0（0 类型错误）
npx vue-tsc --noEmit -p tsconfig.node.json     -> exit 0
npm run build                                  -> exit 1（沙箱限制，见 §6）
```

### 5.3 真实链路复核：本轮未跑（环境不可达）

本轮尝试启动真实服务时 PostgreSQL 已不可达，因此真实链路复核未执行：

```text
apps/document-service: go run ./cmd/document-service
  -> {"msg":"document service exited with an error","error":"document-service postgres: ping:
      failed to connect ... 127.0.0.1:15432 ... the target machine actively refused it"}

Test-NetConnection 127.0.0.1:15432 -> False
Test-NetConnection 127.0.0.1:16334 -> False（Qdrant）
Test-NetConnection 127.0.0.1:16379 -> False（Redis）
```

`GOWEB_REAL_SERVICES=1 go test ./internal/modules/document/interfaces/sourceowned/ -run TestRealChain -v`
需要上述基础设施与两个服务进程，本轮无法执行。相关断言已写进 `real_services_test.go`（`truncated`、每个可读页非空、越界页、`appliedVersionId`、重放），供 `fix-search` 完成后复核；`go vet ./...` 已确认该文件可编译。

补充事实：本轮读取 `apps/document-search/internal/application/hybrid.go` 时，`Truncated: bounds.Offset+int64(len(hits)) < total`（第 153 行）与 `search_integration_test.go` 中「第 1/2 页 truncated=true、第 3 页 false」的断言已在位，即 `fix-search` 的目标语义已落进代码；真实链路未跑纯属本会话基础设施不可达。

## 6. 跳过项与阻塞

| 项 | 状态 | 说明 |
| --- | --- | --- |
| `npm run build`（`vue-tsc -b && vite build`） | **未完成（沙箱限制，非代码问题）** | 先执行 `npm run test:phase-boundary`（PASS），随后 `vue-tsc -b` 报 `TS5033 Could not write file .../node_modules/.tmp/tsconfig.app.tsbuildinfo: EPERM`；`npx vite build --configLoader runner --outDir %TEMP%/sf-dist-f02` 报 `Error: spawn EPERM`（`vite/dist/node/chunks/config.js` 的 `optimizeSafeRealPathSync` 子进程管道被沙箱拒绝）。本会话按 `task-9` 说明申请过一次 `danger-full-access`，被自动拒绝，未再重试。请在无沙箱终端执行 `cd apps/simple-frontend && npm run build` 补齐。 |
| `npm run type-check`（`vue-tsc -b`） | **未完成（同上）** | `tsbuildinfo` 写入受限；已用 `vue-tsc --noEmit -p tsconfig.app.json` 与 `-p tsconfig.node.json` 覆盖同一批源码（0 错误）。 |
| 真实链路复核 | **未跑（基础设施不可达）** | 见 §5.3；由 `lead` 在 `fix-search` 完成后重跑。 |
| 部署态 HTTP 全量验收（`cmd/tools/runtimeapitest`） | 未运行 | 需要完整 Compose 部署。 |

## 7. 验收项 → 用例映射

| 验收项（task-9） | 用例 |
| --- | --- |
| 1a `meta.total_pages` 由真实 total 推导 | `TestSearchTruncationAndReadablePageBoundaries`、`TestSearchExposesTruncationAndNeverAdvertisesAnUnreadablePage` |
| 1b 最后一页之后无可用页码 | `TestSearchTruncationAndReadablePageBoundaries`（第 4 页空、total 不变）、前端 `hasNext = activePage < meta.total_pages` |
| 1c 越界页不作为结果页展示 | `LibraryView.vue` 越界收敛（`router.replace` 到最后一页），`TestSearchOutOfRangePageKeepsTotalAndDropsTruncation` |
| 2 `truncated` 透传 | `TestSearchForwardsTotalAndTruncated`、`TestSearchExposesTruncationAndNeverAdvertisesAnUnreadablePage` |
| 2b 前端明确提示 | `LibraryView.vue` `.search-notice`（检索且非加载中且有结果且 `meta.truncated`） |
| 3 `replayed`/`appliedVersionId` 透传 | `TestUpdateCarriesReplayAndAppliedVersion`、`TestUpdateRendersReplayAsAlreadyApplied`、`TestUpdateSavesThroughSaveDocumentAndReturnsTheNewBody` |
| 3b 编辑页区分「已生效过」与「刚刚保存」 | `MarkdownEditorView.vue` 的 `updated.replayed` 分支（info toast + `appliedVersionId`） |
| 4 阶段 A 行为不回退 | 上轮全部用例仍通过（游标分页、`owned_by_subject_only`、真实公开状态/时间、非所有者 403、显式 visibility） |
| 6 前端类型检查 | `vue-tsc --noEmit` ×2 项目 exit 0；生产构建受沙箱限制（§6） |

## 8. 已知限制

1. 前端没有测试运行器（无 vitest），分页/提示逻辑通过类型检查 + 后端契约测试覆盖；浏览器级行为需人工或后续 E2E 补齐。
2. `meta.total` 仍是 `int`（由 `total_count`/检索 `total` 的 int64/int32 转换），开发规模下无影响。
3. 越界页码收敛依赖一次请求往返（先拿到响应里的真实 `total_pages` 再 replace）；这是“服务端说哪些页可读”的必然代价，不会把空页当结果页展示。
