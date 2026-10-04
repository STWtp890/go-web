# 阶段 A / 任务 C：go-web 与前端接通保存、游标分页、搜索分页与公开状态

> 负责人：teammate `svc-web`（共享任务 `task-4`）
> 写入范围：`apps/gin-backend/`、`apps/simple-frontend/`、本文件
> 日期：2026-09-30
> 契约基线：`packages/proto/document/v1`、`packages/proto/documentsearch/v1`（主 Agent 已生成 `packages/gen/*`；本轮未修改 proto/gen/serviceauth/go.work）

## 1. 结论

任务 C 的 9 项交付全部落地，`go build ./...`、`go vet ./...`、`gofmt -l .`、`go test ./... -count=1` 全绿；前端 `vue-tsc` 两个 project 类型检查通过、阶段边界脚本通过。

验收项在**真实链路**上实测通过：本机启动 `document-service`（127.0.0.1:18081）与 `document-search`（127.0.0.1:18082），由真实 HTTP handler → 真实 gateway → 真实 gRPC 客户端 → 真实服务跑完 4 个用例（`TestRealChain*`）。验收期间启动的两个后台服务已在取证后停止，重启命令见 §5.2。

- 25 篇按每页 9 篇：**9 / 9 / 7**，`total_count=25`，无重复无遗漏；
- 前进/后退一致（回到第一页、复用第二页游标都得到同一批文档）；
- 搜索分页 **9 / 9 / 7**，`total=25` 为真实匹配数；
- 个人搜索 `owned_by_subject_only=true`：另一主体的公开文档既不计入 total 也不出现在命中；
- 保存走 `SaveDocument`：一次调用同时生效正文与显式访问策略，保存后立即可读到新正文，列表与详情公开状态一致；
- 非所有者保存返回 `PermissionDenied` → HTTP 403（`ErrForbidden` / `StatusForbidden`）。

唯一未完成项：前端 `npm run build`（`vue-tsc -b` + `vite build`）在本会话沙箱内无法完成，原因是 esbuild 子进程管道被沙箱拒绝（`spawn EPERM`）与 `tsbuildinfo` 写入被拒（`EPERM`）。详见 §6，属于环境限制而非代码问题。

## 2. 交付对照

| # | 交付 | 落点 | 验证 |
| --- | --- | --- | --- |
| 1 | 保存走完整用例 `SaveDocument` | `interfaces/sourceowned/gateway.go`（`Update`→`SaveDocument`）、`infrastructure/documentservice/client.go`（新增 `SaveDocument`） | `TestRealChainSaveAppliesPolicyAndRejectsNonOwner`、`TestUpdateCallsSaveDocumentWithExplicitPolicyRevisionAndRequestID` |
| 2 | 列表游标原样透传 + 前端游标栈 | `gateway.go`（`PageToken: cursor`、`Page.NextCursor`）、`domain/query.go`、`responses.NewCursorPageMeta`、`src/composables/useCursorPager.ts`、`LibraryView.vue` | `TestRealChainCursorPagingTwentyFiveDocuments`、`TestCursorPagingOverTwentyFiveDocuments` |
| 3 | 重置游标：账号/列表类型/每页数量 | `LibraryView.vue`（mode watcher、`subscribeSessionChanges`、`changePageSize`+`setPageSize`） | 见 §5.3 代码走查；`useCursorPager.setPageSize/reset` |
| 4 | 搜索分页与真实总数 | `adapter.go`（`page`/`pageSize`→`page`/`page_size`）、`documentsearch/client.go`、`gateway.go`（`Total: response.Total`） | `TestRealChainSearchPagingAndOwnershipFilter`、`TestSearchReportsRealTotalAndPage` |
| 5 | 个人搜索所有者过滤 | `gateway.go` 固定 `ownedBySubjectOnly=true`；`documentsearch/client.go` 透传 `OwnedBySubjectOnly` | 真实链路：他人公开文档不计入 total、不出现 |
| 6 | 真实公开状态与时间 | `summariesToDomain`/`detailToView`/搜索命中改用 `authenticated_public`、`created_at`、`updated_at`；`unix()` 零值保护 | `TestListRendersPolicyVisibilityNotPublicationStatus`、`TestSearchHitsCarryIndexedTimesAndPolicy` |
| 7 | 接通访问策略修改 | `MarkdownEditorView.vue` 编辑模式、`router` 新增 `document-edit`、`DocumentDetailView.vue` 编辑入口、PUT 显式 `visibility` | 真实链路保存用例 + `TestUpdateRejectsMissingVisibility` |
| 8 | 受影响类型/接口/测试 | 见 §3 文件清单 | 全量测试 |
| 9 | 修复契约变更导致的编译中断 | `documentsearch/client.go`（`TopK`→`PageSize`）、`gateway.go`（`*bool` presence、`total_count`、`next_page_token`）、`documentservice/client.go` | `go build ./...` / `go vet ./...` exit 0 |

## 3. 修改文件（本轮，均在写入范围内）

后端 `apps/gin-backend/`：

- `internal/modules/document/infrastructure/documentsearch/client.go`：`Search` 签名改为 `(…, page, pageSize int, ownedBySubjectOnly bool)`，请求发 `page`/`page_size`/`owned_by_subject_only`（原 `TopK`）。
- `internal/modules/document/infrastructure/documentservice/client.go`：新增 `SaveDocument`；`ListDocuments` 增加返回 `totalCount`（`ListDocumentsResponse.total_count`），游标仍为 `next_page_token`。
- `internal/modules/document/domain/query.go`：`Page` 新增 `NextCursor string`，`Number` 明确为展示用。
- `internal/modules/document/interfaces/sourceowned/gateway.go`：
  - `documentserviceAPI` 用 `SaveDocument` 替换 `UpdateDraft`；`ListDocuments` 返回真实总数；
  - `UpdateCommand` 增加 `ExpectedAggregateRevision`、`RequestID`；`Update` 以 `*bool` 显式下发访问策略；
  - `ListOwned`/`ListPublic` 签名改为 `(ownerID, pageSize, cursor)`，游标原样透传，`Total` 取 `total_count`；
  - 删除 `pageToken()`/`totalOf()`（不再由页码换算偏移、不再伪造 total）；
  - `Search` 固定 `ownedBySubjectOnly=true`，命中改用 `authenticated_public`/`created_at`/`updated_at`/`owner_subject_key`；
  - 列表摘要改用 `DocumentSummary.authenticated_public`（原为 `publication_status==PUBLISHED` 推导）；
  - 新增 `webUserIDFromSubject()`（只解析 `web:user:*`，其他命名空间返回空作者）。
- `internal/modules/document/interfaces/sourceowned/adapter.go`：
  - `listQuery`（`pageSize`+`cursor`，`page` 仅作展示序号）、`mutationRequest` 增加 `expectedRevision`/`requestId`；
  - PUT 显式要求 `visibility`（缺失即 400，不猜测“保持策略”）；响应增加 `revision`/`replayed`；
  - GET 详情响应增加 `ownerId`、`revision`（编辑器据此回传 `expectedRevision`）；
  - `writeList` 输出 `meta.nextCursor`（`omitempty`，最后一页不出现该字段）；
  - `requestID()`：缺省时生成 UUID，保证每次保存都带幂等键；
  - 时间统一经 `unix()` 输出，零值输出 0（前端显示“—”）。
- `internal/common/base/responses/response.go`：`Meta` 增加 `nextCursor`（`omitempty`），新增 `NewCursorPageMeta`。
- 测试：`interfaces/sourceowned/adapter_test.go`（改写 + 新增 6 个用例）、`gateway_test.go`（新增，假客户端）、`service_chain_test.go`（新增，真实 HTTP handler + 真实 gRPC 客户端 + 进程内假服务）、`real_services_test.go`（新增，可选真实链路验收）。

前端 `apps/simple-frontend/`：

- `src/api/document.ts`：列表改为 `pageSize`+`cursor`；检索保留 `page`/`pageSize`；`update` 传 `expectedRevision`/`requestId`。
- `src/types/api.ts`：`ApiMeta.nextCursor?`。
- `src/types/domain.ts`：`DocumentDetail.revision?`、`ownerId` 恢复；`UpdateDocumentInput` 增加 `expectedRevision`；`CreateDocumentInput.requestId?`。
- `src/composables/useCursorPager.ts`（新增）：游标栈（`reset`/`setPageSize`/`advance`/`retreat`/`goTo`/`record`）。
- `src/views/app/LibraryView.vue`：mine/public 用游标栈翻页，search 用页码 + 真实 total；每页数量选择（默认 9）；列表类型/账号/页大小变化时重置游标历史。
- `src/components/PaginationControl.vue`：可显式传 `hasNext`/`hasPrev`（游标模式）。
- `src/views/app/MarkdownEditorView.vue`：新增编辑模式（读详情→预填→`SaveDocument` PUT），同一保存的重试复用幂等键。
- `src/views/app/DocumentDetailView.vue`：编辑入口、作者展示。
- `src/router/index.ts`：新增 `edit/:documentId`（`document-edit`）。

## 4. 契约与行为变化

1. `PUT /api/v1/protected/documents/:id` 现在落到 document-service `SaveDocument`：正文、访问策略、版本切换、Outbox 事件在同一事务；响应新增 `revision`（新聚合修订号）与 `replayed`。请求体新增可选 `expectedRevision`、`requestId`。
2. `GET /documents/mine|public` 改为游标分页：请求 `?pageSize=9&cursor=<opaque>`，响应 `meta.nextCursor`（最后一页无该字段）、`meta.total` 为真实 `total_count`、`meta.total_pages` 由真实总数计算。`page` 参数仍被接受并做边界校验（`min=1`），但**不再参与数据选取**，只作展示序号——已部署的 HTTP 验收脚本 `?page=1&pageSize=100`、`?page=0&pageSize=101`（期望 400）行为不变。
3. `GET /documents/search` 仍为页码分页，`page`/`pageSize` 映射为 `page`/`page_size`，`owned_by_subject_only=true`；`meta.total`/`total_pages` 用响应真实 `total`。
4. 列表摘要与搜索命中的 `visibility` 一律来自访问策略 `authenticated_public`，与详情 `visibility` 同源；搜索命中的 `createdAt`/`updatedAt` 来自索引投影，不再用查询时刻填充。
5. 详情响应新增 `ownerId`（由 `web:user:<id>` 解析）与 `revision`；非 Web 命名空间的主体不渲染作者，不猜 Web 账号。

## 5. 实际命令、退出码与关键输出

### 5.1 后端（工作目录 `apps/gin-backend`，`$env:GOCACHE = Join-Path $env:TEMP 'gb-goweb'`）

```text
go build ./...            -> exit 0（无输出）
go vet ./...              -> exit 0（无输出）
gofmt -l .                -> exit 0，输出 GOFMT_CLEAN（0 个文件）
go test ./... -count=1    -> exit 0
   ok gin-backend/internal/common/base/responses
   ok gin-backend/internal/modules/document/application
   ok gin-backend/internal/modules/document/evaluation
   ok gin-backend/internal/modules/document/infrastructure/cache
   ok gin-backend/internal/modules/document/infrastructure/mixinsearch
   ok gin-backend/internal/modules/document/infrastructure/postgresql
   ok gin-backend/internal/modules/document/interfaces/sourceowned
```

注：`real_services_test.go` 默认 `t.Skip`（需 `GOWEB_REAL_SERVICES=1`），因此默认测试运行保持无外部依赖。

### 5.2 真实链路验收（document-service 18081 + document-search 18082 实跑）

启动方式（两个后台进程，配置文件来自各自仓库目录）：

```powershell
# apps/document-service
$env:DOCUMENT_SERVICE_CONFIG = 'configs/config.yaml'; go run ./cmd/document-service
#   -> {"msg":"document service ready"} / gRPC boundary listening 127.0.0.1:18081
# apps/document-search
$env:DOCUMENT_SEARCH_CONFIG = 'configs/config.yaml'; go run ./cmd/document-search
#   -> {"msg":"document search ready"} / gRPC boundary listening 127.0.0.1:18082
#       following document events after_sequence=328
```

```powershell
cd apps/gin-backend
$env:GOWEB_REAL_SERVICES = '1'
go test ./internal/modules/document/interfaces/sourceowned/ -count=1 -run TestRealChain -v
```

```text
=== RUN   TestRealChainHTTPCursorPaging
    real_services_test.go:232: HTTP mine pages: 9/25, 9/25, 7/25, total=25
--- PASS: TestRealChainHTTPCursorPaging (0.52s)
=== RUN   TestRealChainCursorPagingTwentyFiveDocuments
    real_services_test.go:311: page 1: size=9 total=25 nextCursor="MjAyNi0wOS0zMFQwMzoyNjoyNi45NjA5OTdafDA0ZWVhZDMzLTNhYTYtNDI4MS05ZDJjLTVjNGU2ODM1MTkyYg"
    real_services_test.go:320: page 2: size=9 total=25 nextCursor="MjAyNi0wOS0zMFQwMzoyNjoyNi44MjU4ODhafGUxNDllZDg1LWNmNmQtNGU1NS1iYzNlLWZhOTFhMTc0YjRiYg"
    real_services_test.go:332: page 3: size=7 total=25 nextCursor=""
--- PASS: TestRealChainCursorPagingTwentyFiveDocuments (0.73s)
=== RUN   TestRealChainSaveAppliesPolicyAndRejectsNonOwner
--- PASS: TestRealChainSaveAppliesPolicyAndRejectsNonOwner (0.12s)
=== RUN   TestRealChainSearchPagingAndOwnershipFilter
    real_services_test.go:469: search page 1: size=9 total=25
    real_services_test.go:479: search pages: 9/9/7 hits with real total 25
--- PASS: TestRealChainSearchPagingAndOwnershipFilter (1.06s)
PASS
ok  	gin-backend/internal/modules/document/interfaces/sourceowned	2.462s
EXIT=0
```

四个用例各自断言的内容（全部通过）：

- `TestRealChainHTTPCursorPaging`：**浏览器所走的完整路径**（真实 HTTP handler → 真实 gateway → 真实 gRPC 客户端 → 真实服务）分页为 9/9/7，每页 `meta.total=25`、`meta.total_pages=3`，末页无 `nextCursor`，三页并集 25 篇且无重复，首页摘要中 private/public 混合（证明 `visibility` 来自访问策略而非发布状态）。
- 游标分页：`total=25`；三页 9/9/7；三页并集 25 篇且每篇恰好出现一次；最后一页无游标；回到第一页、复用第二页游标得到同一批文档；`ListDocumentsRequest.page_token` 等于上一页下发的游标（原样透传）。
- 保存：创建时 private → `SaveDocument`（显式 `authenticated_public=true` + `expected_aggregate_revision` + `request_id`）→ 返回新正文；紧接着详情读到新正文且 public；公开列表包含该文档且摘要为 public；另一已登记主体可读该公开文档；该主体保存 → `ErrForbidden` / `StatusForbidden`（HTTP 403）。
- 搜索：真实 `total=25`；分页 9/9/7；另一主体的公开同名文档不计入 total 也不出现在命中；命中带非零 `created_at`/`updated_at`；命中 `visibility` 与索引策略一致。

副作用与清理：每次运行使用随机 `web:user:9xxxxxx` 主体与随机关键词；用例结束 `t.Cleanup` 对创建的文档调用 `TrashDocument`（软删除），不破坏既有开发数据。

### 5.3 前端（工作目录 `apps/simple-frontend`）

```text
node scripts/verify-phase-boundary.mjs                    -> exit 0  PHASE_BOUNDARY=PASS
npx vue-tsc --noEmit -p tsconfig.app.json                -> exit 0（0 类型错误；--listFiles 确认覆盖 LibraryView.vue/useCursorPager.ts/document.ts）
npx vue-tsc --noEmit -p tsconfig.node.json               -> exit 0
npm run type-check   (vue-tsc -b)                        -> exit 1  TS5033 无法写 node_modules/.tmp/*.tsbuildinfo（EPERM，沙箱）
npm run build        (vue-tsc -b && vite build)          -> 未能完成（同上 + esbuild spawn EPERM）
```

类型检查结论以 `--noEmit` 两个 project 的 0 错误为准；`-b` 失败原因是沙箱拒绝写入工作区子目录，与代码无关（见 §6）。

## 6. 跳过项、阻塞与环境限制

| 项 | 状态 | 说明 |
| --- | --- | --- |
| `npm run build` / `vite build` | **未完成（环境限制）** | 沙箱下 esbuild 子进程以管道 stdio 启动被拒：`Error: spawn EPERM`（`node_modules/esbuild/lib/main.js`）；`vite build` 还受 `dist/` 写入限制。等价类型检查已通过，构建需在无沙箱终端执行 `npm run build`。 |
| `npm run type-check`（`vue-tsc -b`） | **未完成（环境限制）** | `tsbuildinfo` 需写入 `node_modules/.tmp/`，`EPERM`。已用 `vue-tsc --noEmit -p tsconfig.app.json` 与 `-p tsconfig.node.json` 覆盖同一批源码。 |
| 部署态 HTTP 全量验收（`cmd/tools/runtimeapitest`） | 未运行 | 需要完整 Compose 部署（Nginx/Redis/auth DB/引导管理员）。本轮以真实 gRPC 链路 + 真实 HTTP handler 测试替代（`TestRealChainHTTPCursorPaging` 走的就是真实 HTTP handler）；接口形状保持兼容（§4.2）。 |
| 其他团队交付 | 依赖已满足 | 本轮真实链路跑通依赖 `apps/document-service` 的 `SaveDocument`/`total_count`/`authenticated_public` 与 `apps/document-search` 的 `page`/`page_size`/`owned_by_subject_only`/命中展示字段；两者当时均可构建并运行（svc-document / svc-search 仍在推进，若其再次改动契约需重跑 §5.2）。 |

## 7. 已知限制与后续

1. **编辑入口对所有读者可见**：`DocumentDetailView` 的“编辑”按钮不区分所有者（前端没有当前 Web 用户 id 的接口）。非所有者点击后可进入编辑页，保存时由 document-service 拒绝并渲染 403 文案。如需隐藏，需要产品侧提供当前用户 id（例如新增 `/api/v1/protected/auth/me`）。
2. **`meta.total` 为 `int`**：`responses.Meta.Total` 仍是 `int`，由 `total_count`（int64）转换；开发规模下无影响，若要求严格可后续统一为 int64。
3. **搜索页码分页 vs 列表游标分页**：两类分页语义不同（搜索响应有真实 total，可做页码算术；列表只有前向游标）。前端 `useCursorPager.goTo` 对“跨多页前进”只前进一步，因为中间游标必须逐页取回；当前 UI 只有上一页/下一页，无影响。
4. **本地草稿**：编辑既有文稿时不写本地草稿（草稿属于“新建”），避免恢复草稿覆盖正在编辑的正文。
