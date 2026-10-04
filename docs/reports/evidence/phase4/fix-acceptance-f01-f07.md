# 验收反馈修复证据（F01–F07）

> 来源：go-web 服务拆分验收反馈（核对日期 2026-09-30），7 项发现
> 执行者：主 Agent 与三名 Subagent（`fix-document` F01、`fix-search` F02、`svc-web` F02-Web；F03–F07 由主 Agent 直接执行）
> 环境：Windows + Windows PowerShell 5.1；Docker 引擎 29.7.2；PostgreSQL `127.0.0.1:15432`、Qdrant gRPC `127.0.0.1:16334`、Redis `127.0.0.1:16379`

## 0. 结论

| 项 | 状态 | 一句话结论 |
| --- | --- | --- |
| F01 保存幂等不能识别间隔重投 | **已修复** | 幂等范围从「最近一次」改为「该文档全部已提交请求」的持久账本；验收方探针已成为正式用例 |
| F02 候选上限与分页总数不一致 | **已修复** | `total` = 可翻页读到的结果数，`truncated` = 本页之后还有结果；窗口只影响渲染顺序，不再限制可读范围 |
| F03 CI 缺少 Qdrant | **已修复** | 增加 Qdrant 服务、就绪等待与零跳过断言；**映射端口修正为 `16334:6334`**（集成测试走 `config.Default()`，固定连 `127.0.0.1:16334`，原 `6334:6334` 与测试不一致）；已在等价干净环境实测，GitHub Actions 未运行 |
| F04 go-web 实际数据库账号 | **已修复** | 新增 `go_web_app` 角色（只授认证与管理表），纳入隔离门禁，Web 验收在专属账号下通过 |
| F05 PowerShell 7 Cookie 解析 | **已修复** | 解析改为版本无关；2026-10-01 独立复核补齐端到端：PowerShell 7.6.5 本地进程与 Compose 容器两种形态各 43/43，Windows PowerShell 5.1 本地进程 43/43 |
| F06 遗留检索入口未完全退出 | **已修复** | 旧 mixin-search 客户端与死端口删除、通用方法移入 `packages/serviceauth`、旧基线移入 Compose profile、架构断言封口 |
| F07 计划与完成状态不一致 | **已修复** | 日期/状态统一、历史标记、BM25→tsvector 登记为算法替换、pgvector 与 Qdrant 口径分开写明 |

**本轮额外发现并修复的真实缺陷（不在原 7 项内，由「Compose 全栈验证」暴露）**：四个 Go 应用的 Dockerfile 在 ADR-017 之后**根本无法构建**——`go.work` 已列出全部 module，而 `apps/gin-backend/Dockerfile` 只复制了其中的一部分，`go mod download` 因找不到 `../document-service/go.mod` 等而失败；同时 `dependencies.go` 已 import `packages/serviceauth`，Dockerfile 却没有复制它。修复为四个镜像都以 `GOWORK=off` 独立构建（与 CI 的 module 隔离检查同一前提），并只复制各自真正需要的共享包。

## 1. F01 保存幂等：持久请求账本

- **修复文件**：`apps/document-service/schema/schema_init.sql`、`internal/domain/{model.go,save_request.go,save_request_test.go}`、`internal/infrastructure/postgres/{documents.go,save_requests.go}`、`internal/application/document_commands.go`、`internal/application/integration_test.go`。
- **契约变化**：`SaveDocumentResponse` 新增 `applied_version_id`（本次请求产生的版本，重放时为首次尝试的版本）；`SaveDocumentRequest.request_id` 注释明确「持久记录 + 同 ID 不同载荷以 `ALREADY_EXISTS` 拒绝」；重放返回当前 head 且 `replayed=true` 表示本次零变更。契约文件 §3.1 同步。
- **行为变化**：新增 `document_service.document_save_requests`（`document_id`/`request_id`/`version_id`/`payload_fingerprint`/`created_at`，PK `(document_id,request_id)`），与保存同事务提交；删除 `documents.last_save_request_id`（单一真相来源）；载荷指纹为长度前缀 sha256，覆盖 title/content/content_format 与 `authenticated_public` 的 presence+取值。
- **回归用例**：`TestIntegrationSaveDocumentReplaysADelayedRequestID`（验收方 A→B→A 探针原样整理）、`TestIntegrationSaveDocumentRejectsAKnownRequestIDWithADifferentPayload`、A→A、并发同 ID、回滚后同 ID 重试成功，以及无需数据库的 `internal/domain/save_request_test.go`。
- **实测**（`apps/document-service`，`GOCACHE=%TEMP%\gb-goweb`）：`go build ./...`=0、`go vet ./...`=0、`gofmt -l .`=0 行、`go test ./... -count=1`=0、`go test ./... -count=1 -run Integration -v` = **PASS 32 / FAIL 0 / SKIP 0**；含 F01 用例的包 `-count=3` 连跑 PASS 27 / FAIL 0 / SKIP 0。A→B→A 实测：versions 保持 3、events 3、aggregate 5、activation 3、active 仍为 B、正文仍 `beta`、`replayed=true`、`applied_version_id` = A 首次版本。
- **真库门禁抓到的回归**（记录在案）：首轮 full test 中 8 个 SaveDocument 用例 `NotFound`——`FindSaveRequest` 把 `pgx.ErrNoRows` 与 `domain.ErrNotFound` 比较，导致「首次保存（账本无条目）」被当成错误。修正后全绿；该缺陷在 build/vet/gofmt/无库单测下不可见。
- **未完成**：无。

## 2. F02 分页边界、总数与截断语义

- **修复文件**：`apps/document-search/internal/application/{hybrid.go,search.go}`、`internal/config/config.go`、`configs/*.yaml`、`README.md`；测试 `internal/application/{search_pagination_integration_test.go,search_pagination_test.go,zz_review_probe_compat_test.go}`。Web 侧：`apps/gin-backend/internal/modules/document/{domain/query.go,interfaces/sourceowned/*}`、`common/base/responses/response.go`、前端 `types/api.ts`、`views/app/{LibraryView,MarkdownEditorView}.vue` 等。
- **契约/行为变化**：`total` = 调用方真正可翻页读到的结果数（关键词完整匹配数 + 向量臂额外召回且通过同一 SQL 过滤，去重）；`truncated` = `offset+len(hits) < total`；`vector.max_keyword_candidates`（键名/默认值/环境变量名不变）语义改为 **RRF 融合窗口宽度**——窗口内 RRF 排序，窗口外关键词匹配按关键词顺序续排，因此整条答案集可完整分页。Web 侧 `meta.truncated` 透传、`total_pages` 由真实可翻页总数推导、越界页收敛回最后一页。
- **回归用例**：验收方探针原样保留为 `TestIntegrationReviewProbePastCandidateCap`；另有 7 个分页集成用例（窗口低于/等于/超过匹配数、`page_size` 变化、向量臂改变窗口内顺序、两臂去重、混合尾部可分页、窗口=1 时所有者/范围/生命周期不回退）与纯函数性质测试；Web 侧 `TestSearchTruncationAndReadablePageBoundaries` 等 7 个用例。
- **实测**：验收方探针输出由修复前 `page 3 with 3 matches and candidate cap 2: total=3 hits=0 truncated=true` 变为 **`total=3 hits=1 truncated=false`**；越界页 `page=4,page_size=1,total=3` → 0 命中、`total=3`、`truncated=false`。`apps/document-search`：build/vet/gofmt=0，`go test ./... -count=1`=0，`-run Integration -v` = **PASS 42 / FAIL 0 / SKIP 0**，连跑 3 次全 0。
- **未完成**：无。

## 3. F03 CI 的 Qdrant 依赖

- **修复文件**：`.github/workflows/verify.yml`。
- **变化**：`go-modules` 作业新增 `qdrant/qdrant:v1.19.1` 服务容器（**映射 `6333:6333` 与 `16334:6334`**），因为该镜像没有 shell、无法用 `--health-cmd`，就绪改由显式轮询 `http://127.0.0.1:6333/readyz` 的步骤保证（60 次 × 2s，超时即失败）；初始化步骤补齐 `auth`/`manager` schema（`service_roles.sql` 现在给 `go_web_app` 授权，缺表会让 psql 中止）并在最后执行 `verify_service_isolation.sql`；新增「零跳过」断言步骤——对三个来源服务跑 `-run Integration -v`，出现 `--- SKIP` 或没有任何 PASS 即失败。文件头注释改为说明 Qdrant 是必需依赖。

  **端口修正（2026-10-01，验收反馈 P1）**：初版写成 `6334:6334`，与集成测试的实际连接地址不一致——`apps/document-search/internal/config/config.go:169` 的 `Default()` 固定 `Vector.Endpoint = "127.0.0.1:16334"`，而 `internal/application/integration_test.go:59` 与 `internal/interfaces/grpcapi/transport_integration_test.go:37` 都用 `config.Default()`（仅覆盖 Postgres DSN），**不读 `DOCUMENT_SEARCH_VECTOR_ENDPOINT`**，因此加环境变量也不能修好。现改为 `16334:6334`，并在工作流里写明理由。
- **本地等价验证（干净环境）**：用两个一次性容器（`postgres:17-bookworm` 映射 15433、`qdrant:v1.19.1` 映射 16335）重建全新环境，按 CI 顺序以字节级重定向应用 8 份 SQL，全部 `exit=0`（含 `SERVICE_ISOLATION_OK`）；随后以该环境跑三个服务的集成测试：

  | module | 退出码 | PASS | FAIL | SKIP |
  | --- | --- | --- | --- | --- |
  | apps/document-service | 0 | 45 | 0 | 0 |
  | apps/document-search | 0 | 42 | 0 | 0 |
  | apps/qq-search | 0 | 19 | 0 | 0 |

  临时容器已删除。
- **按工作流实际端口重新验证（2026-10-01）**：清了 `DOCUMENT_SEARCH_TEST_DSN`/`DOCUMENT_SEARCH_VECTOR_ENDPOINT` 等全部环境覆盖，用**默认配置**（PostgreSQL `127.0.0.1:15432`、Qdrant `127.0.0.1:16334`）复跑，即 CI 的真实连接方式：

  | module | 退出码 | PASS | FAIL | SKIP |
  | --- | --- | --- | --- | --- |
  | apps/document-service | 0 | 45 | 0 | 0 |
  | apps/document-search | 0 | 42 | 0 | 0 |
  | apps/qq-search | 0 | 19 | 0 | 0 |

  先前用 16335 的临时环境只证明「服务能通过测试」，这一次才证明**当前工作流的端口配置本身可用**。
- **未完成**：**GitHub Actions 未实际执行**（本机无外网到 `proxy.golang.org` 等，只能做到本地等价验证）。请在可联网环境触发一次 `verify` 工作流并回填结论。
- **附带记录**：三份服务 schema 基线**不可重复应用**（第二次执行报「已存在」并以 `ON_ERROR_STOP` 退出 3）。这与仓库「空库一次性初始化」的约定一致，CI 与 `00-init.sh` 都只对全新库执行一次；此处如实记录，不作为缺陷。

## 4. F04 go-web 专属数据库账号

- **修复文件**：`deployments/postgresql/sql/service/service_roles.sql`、`deployments/postgresql/sql/service/verify_service_isolation.sql`、`apps/gin-backend/configs/{config.yaml,config.docker.yaml}`、`deployments/verify-stage-a-web.ps1`、`.github/workflows/verify.yml`。
- **变化**：新增 `go_web_app` 角色（仅 LOGIN + CONNECT），只授予 `public.users`、`public.managers`、`public.manager_registration_requests` 的 SELECT/INSERT/UPDATE/DELETE 与 `public` 序列，并对 `document_service`/`document_search`/`qq_search` 三个 schema 的表与序列显式 REVOKE。Chat 表**有意不授权**（该面未注册，启用时必须显式改授权）。gin-backend 的本地与容器配置、Web 验收脚本生成的配置都改用该账号，不再使用超级用户。
- **隔离门禁新增用例**：`go_web_app` 能对 `public.users` 做插入/删除往返并读取 `public.managers`；对三个服务 schema 的写入与对 `document_service.document_events` 的读取都必须是 `insufficient_privilege`。
- **实测**：`SERVICE_ISOLATION=PASS`（脚本退出码 0，`NOTICE: SERVICE_ISOLATION_OK`）；Web 端到端验收在 `go_web_app` 下 43/43 通过（本地进程与容器两种形态各一次）；反向核验（`fix-document` 实测）显示 `go_web_app` 对 `document_save_requests` 的四项权限全 false。

## 5. F05 两种 PowerShell 版本的 Cookie 解析

- **修复文件**：`deployments/verify-stage-a-web.ps1`。
- **变化**：新增 `Read-SetCookieValues`，按宿主返回的**形状**而不是按版本分支处理——`WebHeaderCollection`（5.1，索引器把多个 `Set-Cookie` 拼成一个字符串）走 `GetValues`/索引器并按「逗号后紧跟 `name=`」切分，`HttpResponseHeaders`（PowerShell 7，索引器返回独立的值的序列）走枚举，字符串与字符串数组输入也直接支持；`Get-SessionCookieTable` 同时从响应头与该请求会话的 Cookie 容器收集，任一来源缺失都能补齐，并且不再依赖「按下标取头部」这种会丢掉边界的方式。`-UseBasicParsing` 只在主版本 < 6 时传入。
- **实测**：
  - `-SelfTest` 三态全绿：真实 `System.Net.Http.HttpResponseHeaders`（PowerShell 7 的形状）、拼接后的头部字符串（5.1 的形状）、独立值数组，三者都解析出 `pp_user_at`/`pp_user_csrf`/`pp_user_rt`，`COOKIE_PARSING=PASS`（退出码 0）。
  - Windows PowerShell 5.1 上完整 Web 验收通过：本地进程形态 43/43、容器形态 43/43。
  - 非 2xx 仍按原样读取状态与错误正文（`ErrorDetails` 与响应流两条路径保留）。
- **端到端缺口已补齐（2026-10-01 独立复核）**：PowerShell 7.6.5 的本地进程形态与 Compose 容器形态 Web 验收各 **43/43**，Windows PowerShell 5.1 本地进程 **43/43**。本机未安装 PowerShell 7，该结论由验收方执行并反馈。

## 6. F06 遗留检索入口与辅助代码退出

逐项：旧调用者 → 新负责人 → 替代入口 → 证据。

| 对象 | 旧调用者 | 新负责人 / 替代入口 | 处置与证据 |
| --- | --- | --- | --- |
| `internal/modules/document/infrastructure/mixinsearch/`（`client.go`、`client_test.go`、`capability.go`、`capability_test.go`） | 仅 `internal/app/dependencies.go:17,107` 用 `LoadCapabilityKey`；其余为自测 | `packages/serviceauth.LoadBoundaryKeyFile`（共享包，新增 `keyfile.go` + `keyfile_test.go`） | 目录整体删除；`dependencies.go` 改用共享加载器；`packages/serviceauth` build/vet/gofmt/test 全 0 |
| `internal/modules/document/domain/{index_delivery.go,repository.go,shadow_search.go,index_client.go,model.go}` | 除自身外 0 引用（全模块 grep `domain.<Symbol>` 均为 0）；`index_client.go` 仅被已删客户端使用 | 无（职责已随阶段 C 的旧链路退场） | 5 个文件删除；`domain/` 只剩 `query.go`（`Page`/`DocumentHead`/`DocumentView`/`DocumentSummary` 供 `interfaces/sourceowned` 使用），并删除其中已无使用者的 `QueryRepository`/`DocumentViewLoader`/`QueryCache` 三个端口 |
| 根 Compose 默认启动 `mixin-search` + `control-postgres` | `docker compose up -d` | 两者移入 profile `legacy-retrieval`，默认启动路径不再包含 | `docker compose config --services` 默认 8 个服务、不含二者；`--profile legacy-retrieval config --services` 为 10 个；`deployments/README.md`、`PROJECT_STRUCTURE.md`、计划 §0.8 写明用途与命令 |
| 旧检索基线本身（`internal/rag/**`、Chat 语料、控制面） | — | 保留，作为阶段 B 的对照基线 | 未删除；已明确其验证用途与显式启动方式 |
| 「旧路径重新进入 Web 文档业务」 | — | 新增架构断言 | `internal/architecture/dependencies_test.go` 新增规则：`internal/` 下任何文件不得导入含 `mixinsearch` 的包或 `packages/gen/mixin-search`；保留「gin-backend 不得导入来源专属服务 module」 |

- **实测**：`apps/gin-backend` build/vet/gofmt=0、`go test ./... -count=1`=0（含新增架构断言）；默认服务集合与 profile 服务集合如上；Compose 全栈构建启动后 8/8 服务 healthy。
- **未完成**：无。

## 7. F07 计划、算法说明与完成状态统一

- **修复文件**：`docs/planning/CURRENT_IMPLEMENTATION_PLAN.md`、`docs/architecture/PROJECT_STRUCTURE.md`、`deployments/README.md`、`docs/README.md`。
- **变化**：
  - 顶部日期改为 2026-09-30，状态改为「阶段 A/B/C 均已完成并有实测证据」，不再写「进行中」；
  - §0.7 的「属阶段 B 尚未完成」整段替换为阶段 B 与 F01–F07 的完成记录，并链接各自证据；
  - **算法替换登记**：旧 `public.document_search_projection` 的 ParadeDB BM25（`|||` + `pdb.score`）已删除，现为 `document_search.document_index` 的 tsvector + GIN（`websearch_to_tsquery`/`ts_rank_cd`）；明确写为**算法替换、不是效果等价**，检索质量另行验证；
  - **保留「向量流程接管」与「语义效果验证」的区别**：本地哈希向量的接管以「流程在位、可写、可删、可重建、可查询、固定数据集可复现」验收，语义模型与效果另行验证；
  - **pgvector 口径**：明确它是 mixin-search 自身三容器隔离验收的旧验证设施，不是受支持能力；默认启动路径不使用它，主业务库无 vector 列，因此与 `bm25_only_verify.sql` 的门禁不冲突（作用域不同）；
  - **qq-search 与 document-search 的 Qdrant 状态分开写明**：前者集合未创建（登记 alias、实际走 tsvector），后者的 `go_web_document_v1` alias 与集合已实际创建并被查询使用；
  - 旧的一轮临时冻结（§0.9）与历史内容明确标记为历史。
- **实测**：`docs/check-doc-links.ps1` = `DOC_LINKS=PASS`（checked=246、broken=0）；同一任务在当前文档中不再出现相互冲突的完成状态；每项保留能力只登记一个实际负责人。
- **未完成**：无。

## 8. 本轮重新验收

| # | 验收项 | 命令 | 结果 |
| --- | --- | --- | --- |
| 1 | 受影响 module 独立构建与测试 | 七个 module 的 `go build/vet/gofmt -l/test` | document-service 45 PASS、document-search 42 PASS、qq-search 19 PASS（`-run Integration -v`，全部 0 skip）；gin-backend / mixin-search / packages 全 0 |
| 2 | 真实 PostgreSQL + Qdrant 集成 | 同上，DSN 指向开发库与 Qdrant | 0 skip、0 fail；document-search 使用真实 Qdrant |
| 3 | 角色隔离 | `deployments/postgresql/verify-service-isolation.ps1` | `SERVICE_ISOLATION=PASS`（含 `go_web_app`） |
| 4 | 三服务验收 | `deployments/verify-source-owned-services.ps1` | `ADR017_E2E=PASS` |
| 5 | 真实 Web 验收 | `deployments/verify-stage-a-web.ps1` | `STAGE_A_WEB=PASS`，43 断言 / 0 失败 |
| 6 | 两种 PowerShell 环境 | `-SelfTest` + 两种版本端到端 | 5.1 本地进程 43/43；**PS 7.6.5 本地进程 43/43、Compose 容器 43/43**（2026-10-01 独立复核） |
| 7 | CI 所需环境 | 一次性干净容器 + CI 初始化顺序；并按工作流实际端口复跑 | 8 份 SQL 全部 exit 0；三服务集成 45/42/19，0 skip；**清空环境覆盖、按端口 `16334` 复跑同样 45/42/19、0 skip**；GitHub Actions 未执行 |
| 8 | Compose 全栈 | `docker compose up -d --build --wait` | 8/8 服务 healthy；/readyz 与容器探针全部通过；**容器形态下 Web 验收 43/43** |
| 9 | 前端 / 协议 / 架构 / 文档 | `verify.ps1` 内的对应步骤 | 前端类型检查与生产构建通过（`PHASE_BOUNDARY=PASS`）；协议 6/6；架构断言通过；`DOC_LINKS=PASS` |
| 10 | 完整门禁 | `deployments/verify.ps1` | `VERIFY=PASS` |

## 9. 未完成与依赖（如实登记）

| # | 项 | 原因 | 需要谁做什么 |
| --- | --- | --- | --- |
| 1 | GitHub Actions 上的 CI 实际执行 | 本机无到 `proxy.golang.org` 的外网，只能做本地等价验证 | 在可联网环境触发一次 `verify` 工作流并回填；端口配置已于 2026-10-01 修正并本地复跑通过 |
| 2 | ~~PowerShell 7 端到端 Web 验收~~ | **已关闭**（2026-10-01 独立复核：PS 7.6.5 本地进程与容器各 43/43） | 无需后续动作 |
| 3 | 容器内 Web 验收之外的部署态回归（`runtimeapitest`） | 需要完整部署与管理端引导数据 | 如需，按 `deployments/README.md` 的后台步骤执行 |
| 4 | 检索质量（BM25→tsvector 的效果差异、向量语义效果） | 本轮范围外 | 单独的质量验证 |
| 5 | py-agent 实际接入 | 独立任务 | 依据 [PY_AGENT_INTEGRATION_DELIVERY.md](../../contracts/PY_AGENT_INTEGRATION_DELIVERY.md) 反馈 |

## 10. 环境记录

- 会话开始时开发数据库未运行（Docker Desktop 引擎停止、`docker.service` 需提权）：由主 Agent 重新拉起 Docker Desktop 并启动 `postgres`/`redis`/`qdrant`，随后解除两个 Subagent 的阻塞。
- 容器内到 `proxy.golang.org` 不可达但 `goproxy.cn` 可达，Compose 构建用 `GOPROXY=https://goproxy.cn,direct` 完成。
- 工作区写操作与 Docker 访问在受限模式下被拒绝，均以一次性放宽执行并说明理由。

## 11. 独立复核后的收尾（2026-10-01）

验收方独立复核确认 F01、F02 原问题已修复，F04、F05、F06 通过，并补齐了 PowerShell 7 的端到端验收；随后指出三项收尾工作。本轮据此完成：

**代码修复**

1. **CI Qdrant 端口**（[verify.yml](../../../.github/workflows/verify.yml)）：`6334:6334` → **`16334:6334`**。原因是集成测试走 `config.Default()`（`apps/document-search/internal/config/config.go:169` 固定 `127.0.0.1:16334`），`internal/application/integration_test.go:59` 与 `internal/interfaces/grpcapi/transport_integration_test.go:37` 都不读环境变量，因此映射到 6334 时测试连不上，加 `DOCUMENT_SEARCH_VECTOR_ENDPOINT` 也无效。工作流内已写明该理由，避免再次改回去。

**本地验证**

2. 清空全部环境覆盖（`DOCUMENT_SEARCH_TEST_DSN`、`DOCUMENT_SEARCH_VECTOR_ENDPOINT` 等），用**默认配置**（PostgreSQL `127.0.0.1:15432`、Qdrant `127.0.0.1:16334`）复跑三个服务的集成测试，即工作流的真实连接方式：**document-service 45 / document-search 42 / qq-search 19，全部 0 失败 0 跳过**。先前用 16335 的临时环境只证明服务能通过测试，这一次证明的是工作流自身的端口配置可用。
3. 工作流文件的结构检查：端口映射为 `15432:5432`（postgres）、`6333:6333` 与 `16334:6334`（qdrant）；无制表符；Qdrant 就绪轮询步骤仍在 `steps` 内。

**GitHub Actions 实际执行**

4. **仍未执行**——本机无到 `proxy.golang.org` 的外网。本地等价验证与端口复跑已完成，需在可联网环境触发一次 `verify` 工作流回填。

**文档同步**

5. [PY_AGENT_INTEGRATION_DELIVERY.md](../../contracts/PY_AGENT_INTEGRATION_DELIVERY.md) 升为 **v2**：`document.proto` 哈希更新为 `a9a926c1b0acb43ff5bc3248456238eadb8c7bfb501691bf28b2bb325092b30f`，其余七个哈希逐个复核一致，并新增第九行 `packages/serviceauth/keyfile.go`（`6a1ab1dcdef86c8358caf0b2880fb30b83c709040dfd1d3eb4449625543107a5`）；新增 §1.1 说明保存用例的重放语义（持久请求账本、同 ID 不同载荷 `ALREADY_EXISTS`、`document` 为当前 head 而 `applied_version_id` 为首次版本）。
6. [CURRENT_IMPLEMENTATION_PLAN.md](../../planning/CURRENT_IMPLEMENTATION_PLAN.md) §0.10 阶段 B 的旧表述已替换：Compose 全栈改为「已实测 8/8 healthy，容器形态 Web 验收 43/43」，pgvector 口径指向 §0.8 的裁决，仅保留「数据集只读复用」一条；并补入两种 PowerShell 环境的 43/43 证据。
