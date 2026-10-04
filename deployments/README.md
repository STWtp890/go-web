# 本地依赖与验证

根目录 Compose 默认构建并启动 PostgreSQL、Redis、gin-backend、simple-frontend、Qdrant，以及 ADR-017 拆出的 document-service、document-search 与 qq-search。Web/Documents 链路经 document-service 完成文档写入与详情读取，并经 document-search 完成正式文档检索。Chat/WebSocket schema 和源码保留，但服务、连接、路由和前端入口均未注册。

旧的 `document-index-worker` 与 `document-index-admin` 已在阶段 C 删除，不再有 Compose 入口。旧的 mixin-search 检索基线与它的控制 PostgreSQL 仍在仓库里，但**已退出默认启动路径**，位于 Compose profile `legacy-retrieval`：

```powershell
# 只在需要对照验证旧检索基线时显式启动
docker compose --profile legacy-retrieval up -d mixin-search control-postgres
```

| 依赖 | 当前作用 | 宿主机端口 |
| --- | --- | --- |
| PostgreSQL | 用户、管理员，以及三个服务各自的 schema（`document_service` / `document_search` / `qq_search`） | `127.0.0.1:15432` |
| Redis | 会话 SID、缓存、Token 状态与会话撤销广播 | `127.0.0.1:16379` |
| 控制 PostgreSQL | 迁移期 mixin-search 文档控制状态 | `127.0.0.1:15433` |
| Qdrant | 迁移期 mixin-search 文档语料集合；三个新服务各自的 collection/alias 命名空间已在 schema 中登记 | `127.0.0.1:16334`（gRPC） |

### 来源专属服务的数据库账号与隔离

三个服务各自使用独立的 PostgreSQL 角色与 schema，由 `postgresql/sql/service/service_roles.sql` 建立：

| 服务 | schema | 写入账号 | 索引命名空间 |
| --- | --- | --- | --- |
| document-service | `document_service` | `document_service_writer` | 无（文档服务不写索引） |
| document-search | `document_search` | `document_search_writer` | Qdrant alias `go_web_document_v1` |
| qq-search | `qq_search` | `qq_search_writer` | Qdrant alias `qq_source_messages_v1` / `qq_source_files_v1` |

唯一的跨 schema 权限是**按列只读**授予 `document_search_writer` 读取 `document_service.document_events`。写入权限矩阵可用以下门禁实测（需要运行中的 postgres 容器）：

```powershell
.\deployments\postgresql\verify-service-isolation.ps1
```

### 来源专属服务的端到端验收

在开发数据库与本地端口上启动三个服务并跑通两条链路（Web 文档命令 → 事件 → 正式文档索引；QQ 原始事件 → QQ 索引）：

```powershell
.\deployments\verify-source-owned-services.ps1
```

该脚本真实构建并以独立进程启动三个服务，等待 `/readyz`，然后运行 `apps/document-service/cmd/document-service-e2e`：它以已签名的服务身份断言扮演 `go-web` 与 `py-agent`，断言文档创建与详情读回、事件消费后的检索命中、越界请求整体拒绝、跨 audience capability 被拒、无 `document-index-writer` 的索引写入被拒、QQ 消息与文件索引互不串源、撤回后不再命中但记录仍为 `RECALLED`。任何一步失败即整体失败，不会降级为跳过。

空间与成员的管理入口同样在文档服务侧（旧 `gin-backend` spacectl 的直接写表路径已删除）：

```powershell
cd apps/document-service
go run ./cmd/document-service-spacectl `
  -endpoint 127.0.0.1:18081 `
  -capability-key-file ../../deployments/secrets/mixin_search_capability.key `
  -subject web:user:42 -actor admin@example.com -reason "create the team space" `
  create-team -name "Team A"
```

`-subject` 是断言代表的主体（也是 `CreateTeamSpace` 记录的空间 owner），`-actor` 与 `-reason` 必填并进入审计；`bind-group` / `revoke-group` / `add-member` / `revoke-member` / `list-subjects` / `get-space` 同理。

## 前置（全新克隆只需一次）

以下两类产物**刻意不入库**，全新克隆后直接 `docker compose up --build` 会失败：

| 缺失项 | 后果 |
| --- | --- |
| `apps/gin-backend/configs/rsa_private.pem` / `rsa_public.pem` | gin-backend 启动时 `LoadKeys` 失败。密钥不入镜像，由 Compose 只读挂载提供 |
| `deployments/secrets/mixin_search_capability.key` | mixin-search 拒绝启动（它不接受无身份的调用方），索引 Worker 与检索评测也会失败。密钥不入镜像，由 Compose 只读挂载提供 |
| `deployments/postgresql/vendor/*.deb` | postgres 镜像构建失败（ParadeDB `pg_search` 离线安装包，约 64 MB） |

执行一次引导脚本即可补齐（会生成密钥，并在校验 SHA-256 后下载 `.deb`）：

```powershell
.\deployments\bootstrap.ps1
```

## 开发启动

```bash
docker compose -f docker-compose.yaml up -d --build --wait
```

> 构建镜像时 Go 模块代理默认取 `proxy.golang.org`。若该地址不可达（例如国内网络），
> 会出现 `go mod download` 校验模块失败。`docker-compose.yaml` 已把宿主机 `GOPROXY`
> 作为 build arg 传入，因此先设置环境变量再启动即可：
>
> ```powershell
> $env:GOPROXY = 'https://goproxy.cn,direct'
> ```

浏览器访问 `http://localhost:15173`。数据库结构仅在全新数据卷上由 `postgresql/entryscript/00-init.sh` 确定性创建；本仓库处于可丢弃数据的开发期，不维护已有数据库升级、迁移账本、备份恢复或旧 schema 兼容。结构改变后应删除开发卷并重新启动：

```bash
docker compose -f docker-compose.yaml down --volumes
docker compose -f docker-compose.yaml up -d --build --wait
```

## 合并验证

在 Windows PowerShell 中从仓库根目录执行：

```powershell
.\deployments\verify.ps1
```

它按当前架构依次执行：

1. 协议生成物一致性（六份契约）；
2. 七个 Go module 的 `go build` / `go vet` / `gofmt -l` / `go test`；
3. 数据库写权限矩阵（`verify-service-isolation.ps1`）；
4. 三个来源服务的真实 gRPC 链路（`verify-source-owned-services.ps1`，输出 `ADR017_E2E=PASS`）；
5. 真实进程与真实认证下的 Web 文档链路（`verify-stage-a-web.ps1`，输出 `STAGE_A_WEB=PASS`）；
6. 前端阶段边界、类型检查与生产构建；
7. 文档链接检查。

不需要启动服务的一侧可用 `-SkipServices`，跳过前端构建可用 `-SkipFrontend`。

本脚本**取代**了 P1.5/P2.3/P2.4/P2.5 的旧整栈门禁：那套门禁驱动 `document-index-worker`/`document-index-admin`/`document-search-eval`，断言 `public.*` 投影、投递账本与影子观测；这些对象与入口已随阶段 C 退场，继续驱动已删除的二进制只会因错误的原因失败。mixin-search 自身的语料与控制面验收仍在该 module 内（`verify-control-store.ps1`、`verify-qdrant-control.ps1`），需要它们自己的 Compose 工程，因此不并入本门禁。

阶段 A 的验收结果与逐条断言见 [stage-a-web-acceptance.md](../docs/reports/evidence/phase4/stage-a-web-acceptance.md)。

### 验收产物的保留策略

一次完整验收会新增 6 个文件（3 个报告族 × json + md）。为避免无限增长，`test-results/` **每个报告族只保留最近 3 次运行**：

```powershell
.\deployments\prune-test-results.ps1 -DryRun   # 先看将删除什么
.\deployments\prune-test-results.ps1           # 执行
```

更早的产物可从 git 历史取回。该脚本只识别 `<族>_<YYYYMMDD>_<HHMMSS>.<ext>`（以及早期的 `<族>-<YYYYMMDD>_<HHMMSS>.<ext>`）命名；遇到其他命名会跳过并打印警告。仍被任何受版本控制 Markdown 引用的制品也会跳过删除（输出计入 `referencedHeld`），因为正式文档引用会被清理的产物正是阶段报告 23 处证据断链的成因。

正式文档**不应**链接到本目录。阶段 1 和阶段 2 报告引用的运行产物已固化在 `docs/reports/evidence/`，由 `docs/check-doc-links.ps1` 强制该规则。

镜像已确认无需重建时可使用 `-SkipImageBuild`；排查失败并希望保留临时环境时可使用 `-KeepEnvironment`。

只验证 PostgreSQL 镜像和空库基线：

```powershell
.\deployments\postgresql\verify-image.ps1 -UseCachedBase
```

此处的验证结论仅覆盖本地开发构建和功能正确性，不代表生产容量、升级或灾备能力。
