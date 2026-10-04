# 阶段 A 验收证据：真实 Web 端到端、模块门禁与契约生成物

> 执行者：主 Agent（Lead）
> 日期：2026-09-30
> 范围：阶段 A 的集成验收（真实进程 + 真实认证 + 真实 PostgreSQL/Qdrant），以及七个 Go module、协议生成物、数据库权限与前端构建的门禁结果
> 相关交付：[document-service](stage-a-document-service.md)、[document-search](stage-a-document-search.md)、[go-web 与前端](stage-a-go-web.md)、[qq-search](stage-a-qq-search.md)、[py-agent 接口材料](../../contracts/PY_AGENT_INTEGRATION_DELIVERY.md)

## 1. 新增的验收门禁

| 门禁 | 文件 | 作用 |
| --- | --- | --- |
| 阶段 A Web 端到端验收 | `deployments/verify-stage-a-web.ps1` | 启动 document-service、document-search、gin-backend 三个真实进程，经真实注册/登录会话走 Web HTTP，断言 43 条结果 |
| 来源服务跨服务验收（已有） | `deployments/verify-source-owned-services.ps1` | 启动三个来源服务并跑 `document-service-e2e` |

`verify-stage-a-web.ps1` 不修改仓库文件；运行产物（二进制、日志、gin-backend 的临时配置）全部落在系统临时目录。

**环境**：PostgreSQL `127.0.0.1:15432/gin_demo`、Redis `127.0.0.1:16379`、Qdrant `127.0.0.1:16334`（`docker compose up -d postgres redis qdrant`）；宿主机为 Windows PowerShell 5.1；`$env:GOCACHE = %TEMP%\gb-goweb`。

## 2. 阶段 A Web 端到端验收结果

命令：

```powershell
& .\deployments\verify-stage-a-web.ps1
```

退出码 `0`，`STAGE_A_WEB=PASS`，**43 条断言全部通过、0 失败**（实际输出节选）：

```text
==> Cursor pagination: 25 documents at page size 9
  page sizes observed: 9, 9, 7
  PASS  pages read as 9/9/7
  PASS  meta.total reports the real total (25)
  PASS  pagination returned no duplicate row
  PASS  pagination covered every document exactly once
  PASS  no created document was skipped by pagination
==> Subject isolation: public and private documents
  PASS  A's own list excludes B's private document
  PASS  A's own list excludes B's public document
  PASS  A's public list contains B's public document
  PASS  A's public list excludes B's private document
  PASS  A may read a public document owned by B
  PASS  A is refused B's private document with 403 (got 403)
==> Save returns the new body immediately
  PASS  saving succeeded (status 200)
  PASS  save response carries the new body
  PASS  detail read after save returns the new body
==> Non-owner and stale-revision refusals
  PASS  a non-owner save is refused with 403 (got 403)
  PASS  the refused save changed nothing
  PASS  a stale expected revision is refused with 409 (got 409)
  PASS  the stale save changed nothing
==> Access policy change is visible in detail and list
  PASS  switching to public succeeded (status 200)
  PASS  detail read reports public
  PASS  list summary reports the same public state as the detail read
  PASS  switching back to private is applied
  PASS  detail read agrees after switching back
==> Search: scoped totals, paging and owner isolation
  PASS  search total reached 25 (observed 25); B's public document is excluded by the owner filter
  PASS  search meta.total is the real total (25)
  PASS  search page 1 returns 9 hits
  PASS  personal search excludes B's public document
  PASS  search page 1 and page 2 are disjoint
  PASS  search page 3 returns the remaining 7 hits (got 7)
  PASS  a single wide search page returns every owned match (25)
  PASS  the searched document is present in the index
  PASS  the search hit reports the real access state
  PASS  the search hit carries real timestamps
==> Delete removes the document from the active list and from search
  PASS  delete succeeded (status 200)
  PASS  the owner can still read the trashed document (got 200)
  PASS  the deleted document left the active listing
  PASS  the active listing total dropped to 24 (got 24)
  PASS  search total dropped to 24 after delete (observed 24)

assertions: 43, failures: 0
STAGE_A_WEB=PASS
```

### 2.1 验收项 → 证据

| 验收项 | 证据 |
| --- | --- |
| 经真实认证执行创建、详情、保存、公开状态修改、列表、搜索和删除 | 两个主体各自 `POST /api/v1/public/auth/register` + `/login` 取得真实会话（含 CSRF），全部操作走 `Authorization` 以外的真实 Cookie + `X-CSRF-Token` |
| 25 篇按每页 9 篇得到 9、9、7 | `page sizes observed: 9, 9, 7` |
| 列表翻页无重复、无遗漏 | 三页并集恰好 25 篇、每篇一次；`no created document was skipped by pagination` |
| 搜索分页与总数正确 | `total=25` 为真实匹配数；页 1/2 不相交；页 3 为 7 |
| 列表与详情的公开状态一致 | 切到 public 后详情与 `mine` 摘要同为 `public`；切回 private 后详情一致 |
| 保存后页面读到新正文 | 保存响应与随后的详情读取都返回新正文 |
| 非所有者修改被拒绝 | 另一主体 PUT → 403，且文档未变 |
| 过期修订号冲突 | `expectedRevision=1` → 409，且文档未变 |
| 主体 A、B 与公开／私有文档的隔离 | A 的 `mine` 不含 B 的任何文档；A 的 `public` 含 B 的公开文档、不含 B 的私有文档；A 读 B 私有 → 403 |
| 已删除内容不进入结果 | trash 后离开活动列表（`total 24`）且搜索总数降到 24；所有者仍可读该已删除文档（生命周期语义，不是物理删除） |
| 多页数据 + 轮询等待索引 + 明确测试超时 | `Wait-ForIndexedTotal` 以 90s 上限轮询搜索总数，未命中即失败 |

### 2.2 被修正的一处断言（记录在案）

首轮运行唯一失败项是「删除后详情读取返回 404」。核对实现后确认**断言本身错误**：`TrashDocument` 是生命周期转换（`trashed`），不是物理删除，契约没有规定所有者失去读取权。已把断言改为「所有者仍可读 + 离开活动列表 + 离开索引」，并在脚本中写明理由。这不是把失败改成通过，而是把断言改回契约描述的行为；索引侧的移除仍由搜索总数下降独立断言。

## 3. 跨服务验收（ADR-017 门禁）

命令：`& .\deployments\verify-source-owned-services.ps1` → 退出码 `0`，`ADR017_E2E=PASS`：

```text
DOCUMENT_CHAIN_OK
  - created document 81d738d4-… as web:user:489807800
  - detail read matches the written content
  - issued a document-search capability with 1 member spaces
  - document-search returned 1 hit(s) for the marker
  - an out-of-grant request was rejected as a whole
  - a capability minted for another audience was rejected
  - indexing without the writer scope was rejected
  - index status: alias=go_web_document_v1 generation=g1 indexed=461
QQ_CHAIN_OK
  - indexed one raw QQ message and one raw QQ file
  - qq message and file indexes are separate and both searchable
  - recall hid the message from search while keeping the record
  - an out-of-scope QQ conversation was rejected as a whole
```

## 4. 七个 Go module 的门禁

命令：对 `apps/{document-service,document-search,qq-search,mixin-search,gin-backend}`、`packages/gen`、`packages/serviceauth` 各自执行 `go build ./...`、`go vet ./...`、`gofmt -l .`、`go test ./... -count=1`。

| module | build | vet | gofmt | test |
| --- | --- | --- | --- | --- |
| apps/document-service | 0 | 0 | 0 文件 | 0 |
| apps/document-search | 0 | 0 | 0 文件 | 0 |
| apps/qq-search | 0 | 0 | 0 文件 | 0 |
| apps/mixin-search | 0 | 0 | **29 文件** | 0 |
| apps/gin-backend | 0 | 0 | 0 文件 | 0 |
| packages/gen | 0 | 0 | 0 文件 | 0 |
| packages/serviceauth | 0 | 0 | 0 文件 | 0 |

`apps/mixin-search` 的 29 个未格式化文件是**本轮之前就存在**的状态（本轮未修改该 module 的任何文件，`git status` 对该目录无输出）；阶段 B/C 处理该 module 时一并说明，不作为阶段 A 的回归。

## 5. 其它门禁

| 门禁 | 命令 | 结果 |
| --- | --- | --- |
| 协议生成物 | `packages/proto/verify-generated.ps1` | 六份契约全部 `PASS`（生成代码与 proto 逐文件哈希一致） |
| 数据库写入权限隔离 | `deployments/postgresql/verify-service-isolation.ps1` | `SERVICE_ISOLATION=PASS` |
| 文档链接 | `docs/check-doc-links.ps1` | `DOC_LINKS=PASS`，`checked=223 documents=119 broken=0 unstable=0` |
| 前端类型检查 | `npm run type-check`（`vue-tsc -b`） | 退出码 0 |
| 前端生产构建 | `npm run build`（`vue-tsc -b && vite build`） | 退出码 0，`dist/` 32 个文件，`✓ built in 1.77s` |

前端构建与 `vue-tsc -b` 在受限沙箱下会因 esbuild 子进程管道（`spawn EPERM`）与 `dist`/`tsbuildinfo` 写入被拒而失败；按工具边界以一次性放宽执行完成，结果如上。Subagent C 的报告已如实登记该限制，本节是补齐后的结果。

## 6. 阶段 A 的契约修订（主 Agent）

| 契约 | 修订 | 由谁实现 |
| --- | --- | --- |
| `document.v1` | 新增 `SaveDocument`（单事务保存、`optional authenticated_public` presence 语义、幂等 `request_id`）；`DocumentSummary.authenticated_public`；`ListDocumentsResponse.total_count`；`DocumentEventEnvelope.created_at`；`UpdateDraftRequest.authenticated_public` 改 `optional` | document-service（任务 A） |
| `documentsearch.v1` | `top_k` → `page_size`；新增 `owned_by_subject_only`；`SearchHit` 增加 `owner_subject_key`/`authenticated_public`/`created_at`/`updated_at`；`IndexDocumentEventRequest.created_at` | document-search（任务 B） |
| `qqsearch.v1` | `GetQQRecordStateRequest` 新增 `QQChannelScope scope` | qq-search（任务 D） |
| `packages/serviceauth` | capability 的 `issuer` 必须是该 audience 的事实源（`document-search` ← `document-service`，`qq-search` ← `py-agent`）；签发端与校验端同时执行；新增两个固定凭证样例（golden vector） | 主 Agent |

## 7. 环境与过程记录- 会话开始时工作区**所有子目录均不可写**（Windows 文件权限缺少当前用户项，且沙箱授权不可继承），`pwsh` 命令一律以 `grantWrite` 失败告终。按内置技能脚本修复根目录后仍需逐命令放宽才能在工作区内写文件；用户已确认把会话切到完全权限，但该切换在本会话未生效，因此工作区内的写操作通过 `sandbox_permissions: danger-full-access` 一次性放宽完成，均已在调用中说明理由。
- `docker compose exec` 与 `docker` 在受限模式下不可用（命名管道），schema 变更由 subagent 经 `127.0.0.1:15432` 直连执行，均已核验。
- 变更的数据库列（开发库与 `schema_init.sql` 同步）：`document_service.documents.last_save_request_id`、`document_search.document_index.created_at`、`document_search.document_index.document_updated_at`。

## 8. 阶段 B 之后的复核与最终门禁

阶段 B 把正式文档检索从“仅关键词”改为“关键词 + 向量 RRF”，因此阶段 A 的验收必须在阶段 B 之后重跑。

**复核发现并修正的一处断言**：`the search hit reports the real access state` 在首次复核时失败。原因不是功能回退，而是**断言读得太早**：访问策略变更经事件流到达索引，紧跟变更后的检索命中仍可能描述上一个访问状态。这正是任务要求“轮询等待索引更新并设置明确超时”的场景，因此把该断言改为带截止时间的轮询（`Wait-ForSearchHitVisibility`），未收敛即失败。修正后复核通过，命中最终收敛为 `private`。

**复核结果**：`STAGE_A_WEB=PASS`，43 条断言、0 失败（阶段 B 之后）。

**最终整仓门禁**：`deployments/verify.ps1` → `VERIFY=PASS`，依次通过协议生成物（6/6）、七个 module 的 build/vet/gofmt/test、数据库写权限矩阵（`SERVICE_ISOLATION=PASS`）、跨服务验收（`ADR017_E2E=PASS`）、真实 Web 链路（`STAGE_A_WEB=PASS`）、前端类型检查与生产构建（`PHASE_BOUNDARY=PASS`，`vite build` 成功）、文档链接（`DOC_LINKS=PASS`）。

阶段 B 的 `SearchHit.score` 语义变化（由关键词分数变为 RRF 融合分数）已登记进 [文档服务契约 §6.1.1](../../contracts/DOCUMENT_SERVICE_V1_CONTRACT.md)。
