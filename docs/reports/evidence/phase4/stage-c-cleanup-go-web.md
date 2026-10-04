# 阶段 C 清理证据：gin-backend 与 mixin-search（task-6）

> 执行者：teammate `svc-cleanup`（task-6）｜日期：2026-09-30
> 范围：`apps/gin-backend/`、`apps/mixin-search/` 的旧实现清理 + 该范围内的删除后善后
> 结论：gin-backend `go build ./...` / `go vet ./...` / `gofmt -l .`（0 行）/ `go test ./... -count=1` 全部通过；mixin-search `go build ./...` / `go vet ./...` / `go test ./... -count=1` 通过，`gofmt -l .` 在 Windows 工作树列出 29 个文件（**预存在的 CRLF 检出产物**，见第 6 节，非本次改动引入）

## 0. 删除执行方式与权限边界

物理删除不是由本 subagent 执行的，原因是会话权限：

1. `pwsh` 的 `Remove-Item` 对 `apps/gin-backend/**` 与 `apps/mixin-search/**` 一律被 Windows ACL 拒绝，实测三例：

   ```text
   DENIED : apps/gin-backend/internal/modules/document/infrastructure/cache/cache.go
            -> ArgumentException: 对路径的访问被拒绝。
   DENIED : apps/gin-backend/cmd/document-index-worker/main.go  -> 同上
   DENIED : apps/mixin-search/cmd/demo/main.go                  -> 同上
   ```

   `Set-Content` 同样被拒绝；`$ExecutionContext.SessionState.LanguageMode` 为 `FullLanguage`。
2. read/edit/write 文件工具在工作区内可写（实测创建/覆盖成功），但没有删除语义；"编辑到空内容"不等于删除。
3. 对 `danger-full-access` 的一次性申请返回：`the user rejected escalating this command to "danger-full-access"; it stays denied, so stop and explain instead of working around it`。

因此按主 Agent 裁决（方案 A）由主 Agent 执行物理删除，本 subagent 负责清单、删除后的源码/配置善后与验收：

- 第一批（主 Agent）：`internal/modules/document/{application,infrastructure/postgresql,infrastructure/cache,evaluation}`、`internal/modules/space`、`cmd/tools/spacectl`、`cmd/document-index-worker`、`cmd/document-index-admin`、`cmd/document-search-eval`，以及本次实测留下的探针文件 `apps/gin-backend/.probe-fs-tool.txt`。
- 第二批（主 Agent）：`internal/config/must/{indexDeliveryConf,shadowSearchConf,mixinSearchSecurityConf}*.go`（6 个）、`compose.index-delivery.yaml`、`verify-index-delivery.ps1`、`verify-index-rebuild-e2e.ps1`，以及 `apps/mixin-search/cmd/{demo,rag-token,rag-grpc-client}`。

下文行号均为**删除前**工作树的坐标（git 未提交状态）。

## 1. 删除清单：旧调用者 → 新负责人 → 替代入口 → 验证证据

### 1.1 旧文档应用层

| 项 | 内容 |
| --- | --- |
| 删除对象 | `internal/modules/document/application/**` 全部 12 个文件（command_service、index_delivery、index_maintenance、query_service、shadow_search 及各自单元/集成/e2e 测试） |
| 旧调用者 | `cmd/document-index-admin/main.go:18`、`cmd/document-index-worker/main.go:16`、`cmd/document-search-eval/main.go:21`；组合根自阶段 A 起不再构造（`internal/app/dependencies.go` 的 import 列表里没有 application） |
| 新负责人 | 正式文档写入与详情读取：`apps/document-service`；正式文档检索：`apps/document-search` |
| 替代入口 | `internal/modules/document/interfaces/sourceowned` 经 `infrastructure/documentservice.Client`（SaveDocument/UpdateDraft/Publish/Get/List）与 `infrastructure/documentsearch.Client`（SearchDocuments）访问服务 |
| 验证证据 | `go list -f '{{.ImportPath}} {{.Imports}}' ./...`：application 仅被三个 cmd 引用；删除后 gin-backend `go build ./...`=0、`go vet ./...`=0、`go test ./... -count=1`=0；`git status` 显示 12 个 `D` |

### 1.2 旧 PostgreSQL 仓储（`public.*` 表实现）

| 项 | 内容 |
| --- | --- |
| 删除对象 | `internal/modules/document/infrastructure/postgresql/**` 共 8 个文件（repository、query、records、shadow_search、index_delivery_store + 3 个集成测试） |
| 旧调用者 | `cmd/document-index-admin/main.go:20`、`cmd/document-index-worker/main.go:18`、`cmd/document-search-eval/main.go:25`；旧 application 的集成测试 |
| 新负责人 | `apps/document-service` 自己的 schema（`document_service.*`，由 `apps/document-service/schema/schema_init.sql` 建立） |
| 替代入口 | Web 侧不再存在表级路径：只经 1.1 的两个 gRPC 客户端；ADR-017 决策 6 规定 go-web 不持有文档业务表写权限 |
| 验证证据 | 上述构建/测试退出码；`git status` 显示 8 个 `D`；`internal/app/dependencies.go` 不再注册 `connection.ServiceDocument` |

### 1.3 旧评测能力

| 项 | 内容 |
| --- | --- |
| 删除对象 | `internal/modules/document/evaluation/**`（evaluation.go、evaluation_test.go） |
| 旧调用者 | `cmd/document-search-eval/main.go:23` —— `go list` 全模块反向依赖中唯一调用者 |
| 新负责人 | `apps/document-search`（CURRENT_IMPLEMENTATION_PLAN §0.10 阶段 B「评测」唯一负责人） |
| 替代入口 | `apps/document-search` 的 `SearchDocuments` + 固定数据集 `deployments/evaluation/document-search-v1.json`（阶段 B，task-5 进行中） |
| 验证证据 | 反向依赖唯一；删除后模块 build/vet/test 通过 |
| **依赖登记** | 阶段 B 的评测入口尚未产出可运行的等价 CLI/固定产物；旧入口依赖已删的 application 仓储与影子观察器，无法原样保留。评测能力在阶段 B 完成前处于「职责已登记、实现待接管」状态，见第 3 节第 5 条 |

### 1.4 孤儿文档缓存

| 项 | 内容 |
| --- | --- |
| 删除对象 | `internal/modules/document/infrastructure/cache/**`（cache.go、cache_test.go） |
| 旧调用者 | **无**。`go list` 全模块 import 图显示没有任何包 import 它；组合根与旧 application 都不使用 |
| 新负责人 | 无（文档实体缓存随直连表路径一并退场） |
| 替代入口 | 无。`internal/common/base/cache` 的 `documents` 分区同步删除（见 1.10） |
| 验证证据 | 反向依赖为空；删除后 `go build/vet/test` 通过 |

### 1.5 旧空间模块

| 项 | 内容 |
| --- | --- |
| 删除对象 | `internal/modules/space/**`（application/{scope,service}+测试、domain/model.go、infrastructure/postgresql/{records,repository}.go，共 7 个文件） |
| 旧调用者 | `cmd/tools/spacectl/main.go:16,17,18` |
| 新负责人 | `apps/document-service`（空间、成员、群空间绑定、资源权限、审计） |
| 替代入口 | `apps/document-service/cmd/document-service-spacectl` |
| 验证证据 | `go list`：space 只被 spacectl 引用；`deployments/README.md:38-42` 记录替代入口；删除后 build/vet/test 通过 |

### 1.6 旧空间 CLI

| 项 | 内容 |
| --- | --- |
| 删除对象 | `cmd/tools/spacectl/**`（main.go、main_test.go） |
| 旧调用者 | 无 Go 导入者（main 包）；文档引用 `docs/operations/QQ_SPACE_BINDINGS.md:12,13,21-23,31-33`、`docs/adr/016:191` |
| 新负责人 | `apps/document-service` |
| 替代入口 | `apps/document-service/cmd/document-service-spacectl`（`deployments/README.md:42`；主 Agent 已同步 `docs/operations/QQ_SPACE_BINDINGS.md:31`） |
| 验证证据 | 删除后模块 build/vet/test 通过；`go test ./...` 输出中不再有 `gin-backend/cmd/tools/spacectl` 与 `internal/modules/space/...` |

### 1.7 旧索引投递 Worker

| 项 | 内容 |
| --- | --- |
| 删除对象 | `cmd/document-index-worker/**` |
| 旧调用者 | `docker-compose.yaml:216-240`（服务定义）、`:228`（健康检查借道 admin）、`deployments/verify.ps1:150,185,379,456`、`apps/gin-backend/Dockerfile:23,35` |
| 新负责人 | `apps/document-search`（事件消费、索引状态与重建） |
| 替代入口 | document-search 的 `RebuildIndex` / `GetIndexStatus`；跨服务门禁 `deployments/verify-source-owned-services.ps1`（ADR017_E2E） |
| 验证证据 | 主 Agent 已删除 Compose 服务与 verify.ps1 旧函数（`deployments/verify.ps1:12-13`、`deployments/README.md:108` 记录退场）；Dockerfile 构建/拷贝步骤已同步移除；删除后模块 build/vet/test 通过 |

### 1.8 旧索引管理 Admin

| 项 | 内容 |
| --- | --- |
| 删除对象 | `cmd/document-index-admin/**` |
| 旧调用者 | `docker-compose.yaml:228,242-245`、`deployments/verify.ps1:151,186,192,380`、`Dockerfile:24,36` |
| 新负责人 / 替代入口 | 同 1.7 |
| 验证证据 | 同 1.7 |

### 1.9 旧检索评测 CLI

| 项 | 内容 |
| --- | --- |
| 删除对象 | `cmd/document-search-eval/**` |
| 旧调用者 | `deployments/verify.ps1:390-392`（脚本已由主 Agent 重写，`deployments/README.md:108`）、`apps/gin-backend/README.md:12,66`（本次已更新） |
| 新负责人 / 替代入口 | 同 1.3 |
| 验证证据 | 删除后模块 build/vet/test 通过 |

### 1.10 不再使用的配置、连接与依赖

| 项 | 删除对象 | 旧调用者（路径:行号） | 新负责人 / 替代入口 | 验证证据 |
| --- | --- | --- | --- | --- |
| a | `internal/config/must/indexDeliveryConf.go` + `_test.go` | `internal/config/config.go:25,96`（已清空）；已删的三个 cmd | `source_owned_services` 段承担全部跨服务接线；索引运维归 document-search | 删除前 grep 只命中 config.go 与被删 cmd；删除后 `go build`/`go test`=0 |
| b | `internal/config/must/shadowSearchConf.go` + `_test.go` | `internal/config/config.go:26,99`；已删的 `cmd/document-search-eval` | 影子查询链路无替代（ADR-017 取消影子评估门禁） | 同上 |
| c | `internal/config/must/mixinSearchSecurityConf.go` + `_test.go` | `internal/config/config.go:27,102`；已删的三个 cmd | 边界密钥改由 `source_owned_services.capability_key_path` 提供（`internal/app/dependencies.go:107` 的 `mixinsearch.LoadCapabilityKey`） | 同上 |
| d | `connection.ServiceDocument`（`connection.go:12-13`） | `internal/app/dependencies.go:48`（ready 健康检查）、`:108`（注册）、`:182`（注销） | **无替代**：go-web 不再连接文档业务库 | 删除后 `grep -rn ServiceDocument apps/gin-backend` 为空；`STRUCTURE_ASSESSMENT.md:328,355,427` 曾把该双连接键列为 P1（`ready()` 对同一 DSN 做两次健康检查并暗示不存在的隔离），本次修复 |
| e | `common/base/cache.PartitionDocuments`（`runtime.go:14,194`） | 仅被已删的 `document/infrastructure/cache` 使用；全模块 grep 只有定义处 | 无替代 | `go test ./internal/common/base/cache -count=1` 通过 |
| f | `configs/config.yaml:26-59`、`configs/config.docker.yaml:29-61` 三段（`document_index_delivery`/`mixin_search_security`/`document_search_shadow`） | 已删的三个 cmd 与已删的配置类型 | `source_owned_services` 段 | `go test ./internal/config -count=1` 通过；配置加载不再校验已删字段 |
| g | `internal/config/must/sourceOwnedServicesConf.go:76-79` caller 白名单 `go-web, spacectl` → 收窄为 `go-web` | `spacectl` 身份只可能来自已删的 `cmd/tools/spacectl`；`deployments/*.ps1:429` 写入的是 `caller: go-web` | document-service 侧仍登记 spacectl（其入口是 document-service-spacectl），但 gin-backend 不再代持该身份 | `go build`/`go vet`/`go test` 通过 |
| h | `apps/gin-backend/Dockerfile:23-24,35-36`（worker/admin 构建与拷贝） | 镜像内二进制的消费方是 `docker-compose.yaml:216-245`（已删） | 镜像只保留 `/app/gin-backend` | 删除后 `go build ./...`=0；镜像仅构建 `./cmd/server` |

### 1.11 旧索引验收脚本与 Compose 片段

| 项 | 内容 |
| --- | --- |
| 删除对象 | `apps/gin-backend/compose.index-delivery.yaml`、`verify-index-delivery.ps1`、`verify-index-rebuild-e2e.ps1` |
| 旧调用者 | `verify-index-delivery.ps1:52,56,60` 与 `verify-index-rebuild-e2e.ps1:133` 调用已删包的测试（`TestIndexDeliveryStoreIntegration`、`TestIndexMaintenanceIntegration`、`TestCommandServiceIntegration`、`TestIndexDeliveryRebuildE2E`）；`compose.index-delivery.yaml` 只被这两个脚本引用（`verify-index-delivery.ps1:10`、`verify-index-rebuild-e2e.ps1:12`）；`apps/gin-backend/README.md:74-75`（本次已更新） |
| 新负责人 / 替代入口 | `deployments/verify-source-owned-services.ps1`（ADR017_E2E）、`apps/document-search` 的集成测试与 `RebuildIndex`/`GetIndexStatus` |
| 验证证据 | 目标测试所在包已删除，脚本已无有效目标；`docs/adr/008:54-55` 与 `docs/reports/PHASE2_IMPLEMENTATION_LOG.md:89-90` 只以反引号引用（非 Markdown 链接），`docs/check-doc-links.ps1` 不受影响 |

### 1.12 mixin-search：已被替代的手工调试入口

| 项 | 内容 |
| --- | --- |
| 删除对象 | `apps/mixin-search/cmd/demo`、`cmd/rag-token`、`cmd/rag-grpc-client`（由主 Agent 执行） |
| 旧调用者 | 无脚本/CI/测试引用，证据见下方引用检查；仅 `apps/mixin-search/README.md:52,58-60,80,86,136-151,248-250` 与 `docs/architecture/PROJECT_STRUCTURE.md:120-121`（主 Agent 已更新） |
| 引用检查命令与结果（删除前执行） | 1) `Select-String -Path docker-compose.yaml -Pattern 'rag-grpc-client\|rag-token\|cmd/demo'` → 0 命中（只命中 `gin_demo` 等无关串）。2) `Select-String -Path deployments/*.ps1,deployments/**/*.ps1,.github/workflows/*.yml -Pattern 'rag-grpc-client\|rag-token\|cmd/demo\|mixin-search/cmd'` → 仅 `deployments/verify.ps1:37`（`apps/gin-backend/cmd/tools/pemgenerator`，无关）。3) `Select-String -Path apps/mixin-search/*.ps1 -Pattern 'go run\|go test\|cmd/'` → 只有 `go test ./internal/rag -run TestPostgresControlStoreIntegration`、`go test ./internal/rag -run 'TestQdrant.*Integration'`、`go test ./internal/rag -run 'TestQdrantAliasSwitchIsPerCorpus\|TestAliasSwitchStaysInsideItsOwnCorpus'`。4) `apps/mixin-search/internal/architecture/dependencies_test.go` 只做 import 方向断言，无这些目录的路径断言 |
| 新负责人 | 正式文档检索：`apps/document-search`；QQ 来源检索：`apps/qq-search` |
| 替代入口 | 手工工具无直接替代：capability 由 `go-web` 侧签发，端到端验证走 `deployments/verify.ps1` 与包内集成测试 |
| 验证证据 | 删除后 mixin-search `go build ./...`=0、`go vet ./...`=0、`go test ./... -count=1`=0 |

## 2. 保留清单（未删除及理由）

| 对象 | 理由 |
| --- | --- |
| `internal/modules/document/interfaces/sourceowned/**`、`infrastructure/documentservice/**`、`infrastructure/documentsearch/**`、`infrastructure/mixinsearch/**` | 当前唯一 Web 文档链路；`internal/app/dependencies.go:107` 仍用 `mixinsearch.LoadCapabilityKey` |
| `internal/modules/document/domain/**` | 主 Agent 明确要求保留。`model.go`/`query.go` 被 sourceowned 使用（`domain.DocumentSummary`/`DocumentView`/`DocumentHead`/`Page` 等 50 处）；`index_client.go` 被 `infrastructure/mixinsearch/client.go:30,118,239` 使用。`index_delivery.go`/`repository.go`/`shadow_search.go` 已无使用者，见第 3 节 |
| `apps/mixin-search/internal/rag/**`（含 `store_pgvector.go`）、`internal/chat/**`、`internal/chatindex/**`、`internal/security/**`、`internal/transport/**`、`document_pipeline/**`、`cmd/rag-server`、`cmd/rag-healthcheck`、`verify-*.ps1`、`compose.yaml` | 阶段 B 仍在接管的检索基线，本次按要求未改动 |
| `cmd/tools/runtimeapitest` | Web 运行时 API 验收客户端，仍指向 sourceowned 路由；无需改动，`go build`/`go vet` 通过 |
| `.github/workflows/verify.yml` | 逐 module 执行 `go build/vet/test ./...`，已核对不引用任何已删入口，无需修改 |
| `apps/mixin-search/README.md`、`apps/gin-backend/README.md` | 在写入范围内，已改为当前结构（见第 4 节说明） |

## 3. 未删除项与原因

1. **mixin-search pgvector 后端（主 Agent 裁决不删）**：`internal/rag/store_pgvector.go`、`cmd/rag-server -store pgvector`、`compose.yaml` 的 pgvector 服务、`go.mod` 的 pgvector 依赖均保留——`verify-qdrant-control.ps1` 的三容器隔离验收与 `-store pgvector` 启动路径仍在使用。
   **已知冲突（待决策）**：`deployments/postgresql/sql/plugin/bm25_only_verify.sql` 断言"任何业务 schema 都不含 vector 列/向量索引"。pgvector 后端只在 `apps/mixin-search/compose.yaml` 的独立 pgvector 容器里建 `rag_chunks(vector)`，不进入 `gin_demo` 默认栈，因此当前门禁不会因此失败；但"PostgreSQL 内是否存在向量能力"的口径需要一次显式裁决（删除该后端 / 保留并明确门禁只覆盖业务 schema / 明确它只服务一次性验收环境）。
2. **`internal/modules/document/domain/{index_delivery.go,repository.go,shadow_search.go}`**：主 Agent 要求保留 `domain/**`；这三个文件在阶段 C 后已无任何使用者（全模块 grep 无 `domain.IndexDelivery*`、`DocumentRepository`、`ShadowSearch*` 引用），属可单独清理的死端口。
3. **`internal/modules/document/infrastructure/mixinsearch/client.go`**：任务要求保留该目录；其文档索引调用当前只被自身测试引用，生产代码只用 `capability.go` 的 `LoadCapabilityKey`。
4. **mixin-search `gofmt -l .` 的 29 个文件**：无法在受限会话内写工作树；修复方式与影响见第 6 节。
5. **阶段 B 评测接管尚未闭环**：`apps/document-search` 当前没有评测命令或固定数据集产物（task-5 进行中）。本阶段删除的是"旧评测入口 + 其依赖的旧仓储/影子观察器"，评测职责已登记给 document-search；在阶段 B 完成前，仓库没有可复现的评测 CLI。这是阶段 C 的已知对外依赖，不是本次删除的遗漏。

## 4. 删除后的善后（本 subagent 实际改动）

| 文件 | 改动 |
| --- | --- |
| `internal/app/dependencies.go` | 删除 `connection.ServiceDocument` 的注册、`ready()` 健康检查与注销；`ready()` 现在只检查本进程 PostgreSQL(auth) 与 Redis，并加注释说明 ADR-017 边界 |
| `internal/common/base/connection/connection.go` | 删除 `ServiceDocument` 常量 |
| `internal/config/config.go` | 删除 `IndexDeliveryConfig`/`ShadowSearchConfig`/`MixinSearchSecurity` 三个字段与对应 `ConfigCheck` 调用 |
| `internal/config/must/sourceOwnedServicesConf.go` | caller 白名单由 `go-web, spacectl` 收窄为 `go-web`（gin-backend 不再代持 spacectl 身份） |
| `internal/common/base/cache/runtime.go` | 删除已无使用者的 `PartitionDocuments` 常量与默认分区项 |
| `internal/architecture/dependencies_test.go` | 重写为描述**阶段 C 之后**的结构：保留 domain 纯净性、interfaces 不持有本地持久化、infrastructure 单向依赖、业务模块不依赖组合根、common/* 与 jwt 规则，**保留「gin-backend 不得导入来源专属服务 module」**；新增 documentservice/documentsearch 客户端"不得持有模型/GORM/数据库连接"两条规则；移除会因目录删除而 `t.Fatalf` 的 `modules/document/application` 规则（共 10 条断言，全部通过） |
| `internal/config/config.yaml`、`configs/config.docker.yaml` | 删除 `document_index_delivery`/`mixin_search_security`/`document_search_shadow` 三段 |
| `Dockerfile` | 只构建并拷贝 `/app/gin-backend`（`./cmd/server`），删除 worker/admin 的构建与拷贝 |
| `apps/gin-backend/README.md` | 目录树、document 结构说明、常用命令、边界密钥说明、缓存分区说明、P2.3–P2.5 历史段全部改为当前结构；旧的 worker/admin/eval 命令与 `verify-index-*.ps1` 用法移除 |
| `apps/mixin-search/README.md` | 「运行」段改为 `cmd/rag-server`/`cmd/rag-healthcheck` 入口并说明 `cmd/demo`/`rag-token`/`rag-grpc-client` 已随阶段 C 退场；删除手工 capability 签发与最小客户端用法，改为指向 `deployments/verify.ps1` 与包内验收脚本；「代码入口」清单同步；P2.3–P2.5 段标注已退场 |
| `go.mod` / `go.sum` | 未改动。`go mod tidy -diff` 退出码 0、无输出 → 无孤儿依赖，也无需补 sum |

## 5. 实际命令与退出码

删除前基线（2026-09-30，第一批删除执行之前）：

```text
apps/gin-backend : go build ./...=0  go vet ./...=0  gofmt -l . → 0 行  go test ./... -count=1=0
apps/mixin-search: go build ./...=0  go vet ./...=0  gofmt -l . → 29 行 go test ./... -count=1=0
```

删除与善后之后（最终验收，两模块均在各自目录执行）：

```text
apps/gin-backend
  go build ./...            → exit 0
  go vet ./...              → exit 0
  gofmt -l .                → 0 行（exit 0）
  go test ./... -count=1    → exit 0；ok: internal/app, internal/architecture,
                              internal/common/base/cache, internal/common/base/responses,
                              internal/common/service/sessionevent, internal/config, internal/model/store,
                              internal/modules/aiagent, internal/modules/chat/{api,structure/bridge,
                              types/client,types/message}, internal/modules/document/infrastructure/mixinsearch,
                              internal/modules/document/interfaces/sourceowned,
                              internal/platform/httpserver, internal/platform/httpserver/middleware
                              （无 FAIL、无 panic）
  go mod tidy -diff         → exit 0，无输出

apps/mixin-search
  go build ./...            → exit 0
  go vet ./...              → exit 0
  gofmt -l .                → 29 行（见第 6 节；与删除前基线相同，非本次引入）
  go test ./... -count=1    → exit 0；ok: cmd/rag-server, document_pipeline,
                              internal/architecture, internal/chat, internal/chatindex,
                              internal/controlplane, internal/rag, internal/security,
                              internal/transport/grpc（无 FAIL、无 panic）
```

其他核对：

```text
git status --porcelain -- apps/gin-backend apps/mixin-search
  → D=42（删除项）、M=14（本次善后文件 + 阶段 A 既有修改）、??=4（阶段 A 既有新增：
    sourceOwnedServicesConf.go、infrastructure/documentsearch/、infrastructure/documentservice/、
    interfaces/sourceowned/）；total=60
Test-Path apps/gin-backend/.probe-fs-tool.txt → False（探针文件已由主 Agent 删除）
```

## 6. 已知环境问题：mixin-search `gofmt -l .` = 29

- 证据：`git ls-files --eol apps/mixin-search` 对这 29 个文件全部显示 `i/lf w/crlf`（索引内 LF、Windows 工作树 CRLF），且 `git status` 不包含它们（相对索引无内容修改）；gin-backend 没有此类文件，`gofmt -l .` 为 0 行。
- 原因：工作树 `core.autocrlf=true` 的检出结果。gofmt 输出 LF，因此把 CRLF 文件判为未格式化。**在 Linux/CI（autocrlf=false）检出时这些文件是 LF，`gofmt -l .` 为 0。**
- 这是预存在状态：阶段 C 之前的基线同样是 29 行（第 5 节），本次未新增未格式化文件。
- 如需让本地门禁显示 0 输出（该操作对 git 不可见：CRLF→LF 归一化后与索引 blob 相同）：在 `apps/mixin-search` 执行 `gofmt -w .`。需要写工作树权限，因此由主 Agent 执行；执行后 `git status` 应仍只有 `M README.md` 与三个 `D cmd/...`。

## 7. 需主 Agent 同步修改（本 subagent 写入范围之外）

| 位置 | 现状 | 建议 |
| --- | --- | --- |
| `deployments/verify-stage-a-web.ps1:391,405,413`（含其内 `:410-411` 的 `index_caller_id`/`search_caller_id`） | 仍向生成的 gin-backend 配置写入 `document_index_delivery`、`mixin_search_security`、`document_search_shadow` 三段 | YAML 反序列化忽略未知键，脚本不会失败，但字段已不存在 → 删除这三段 |
| `deployments/postgresql/sql/plugin/bm25_only_verify.sql:84-85` | 白名单仍列出 `document_index_delivery_events`、`document_search_shadow_observations` | 这两张表属于 `public.*` 旧文档 schema，应与旧表清理一起决定去留 |
| `deployments/postgresql/sql/service/document/schema_init.sql:122,174` | 仍创建旧 `public` 投递/影子表（阶段 C 计划删除的"旧表、投影、事件与初始化内容"） | 确认无运行依赖后删除；`deployments/**` 由主 Agent 维护 |
| `deployments/postgresql/chat-timescaledb.md:19` | 仍写 `ServiceDocument` 注册模式，并称"当前组合根不注册该连接" | 与 `connection.go` 的删除同步（连接键已不存在） |
| `docs/planning/CURRENT_IMPLEMENTATION_PLAN.md:158`（§0.7 未完成清单） | 仍写旧 application/postgresql/space/worker/admin/eval/spacectl "仍在仓库中" | 改为已完成，并注明 mixin-search 侧退场项 |
| `docs/planning/CURRENT_IMPLEMENTATION_PLAN.md:206,208`（阶段 B 评测） | 仍写"旧入口在 `apps/gin-backend/cmd/document-search-eval`" | 旧入口已删除；改为"职责已登记 document-search，实现待接管（task-5）" |
| `docs/planning/CURRENT_IMPLEMENTATION_PLAN.md:218`（阶段 C 删除项） | 与已完成状态不同步 | 勾选/更新为已完成 |
| `docs/adr/008:22,54-55`、`docs/adr/009:14,20,26`、`docs/adr/010:20`、`docs/adr/016:191` | 历史 ADR 引用已删 Worker/Admin/验收脚本与 gin-backend spacectl | 作为历史记录可保留，建议补一行"已随 ADR-017 阶段 C 退场"的状态注记（`docs/**` 由主 Agent 维护） |
| `docs/adr/011`（缓存分区） | 描述含 `documents` 分区的有界缓存运行时 | `PartitionDocuments` 已删除，建议核对措辞 |
| `STRUCTURE_ASSESSMENT.md:56,59,328,355,427,496-497,556-566`、`REFACTOR_CLEANUP_PLAN.md:77`、`MIXIN_SEARCH_SPLIT_ASSESSMENT.md:258` | 根目录盘点快照按清理前结构描述（含 `ServiceDocument` 双连接键 P1 项，本次已修复；含 `cmd/document-search-eval` 归类） | 归档或标注已过期 |
| `.github/workflows/verify.yml` | 无引用已删入口 | 无需修改（已核对） |
| `packages/**`、`go.work`、`docker-compose.yaml` | 本次未改动；`docker-compose.yaml` 已由主 Agent 移除 worker/admin 服务 | 无需进一步同步；`go mod tidy -diff` 在两个 module 均无输出 |

## 8. 本次改动文件一览

- `apps/gin-backend/`：`internal/app/dependencies.go`、`internal/common/base/connection/connection.go`、`internal/common/base/cache/runtime.go`、`internal/config/config.go`、`internal/config/must/sourceOwnedServicesConf.go`、`internal/architecture/dependencies_test.go`、`configs/config.yaml`、`configs/config.docker.yaml`、`Dockerfile`、`README.md`
- `apps/mixin-search/`：`README.md`
- 删除项（主 Agent 执行）：见第 1 节 1.1–1.12
