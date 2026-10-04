# Eino RAG 快速示例

这个示例保留 Eino 编排与本地确定性 Embedder，并将文档解析管道和向量存储都拆成可切换组件：

```text
File -> Pipeline Registry -> Markdown/DOC/DOCX Loader -> DocumentBlock
DocumentBlock -> Structure-aware Split -> Eino Embedder -> VectorStore
Query -> Dense Recall + Sparse Recall -> RRF Fusion -> Evidence
```

同一套 Eino 业务 Service 通过 `mixin-search/v1` 暴露版本化文档索引边界：

```text
IndexDocumentVersion(bytes) -> owner_space + lifecycle fence -> split/embed -> VectorStore
ActivateDocumentVersion     -> activation + lifecycle fence -> 切换活动版本
UpdateDocumentAccess        -> access + lifecycle fence -> 替换完整 ACL 快照
SearchDocuments(query)      -> 公开/空间/显式文档 OR 授权 -> dense/sparse recall -> RRF
DeleteDocumentVersion       -> lifecycle fence -> 删除一个派生版本索引
DeleteDocument              -> lifecycle fence -> 删除全部派生索引并留下墓碑
GetDocumentVersionState     -> 查询版本状态与修订信息
```

文档使用 `owner_space_id` 表达真实归属空间；访问快照独立保存 `authenticated_public` 与完整 `granted_space_ids`。搜索只返回活动且未删除的版本，并按以下条件做 OR 授权：公开可读、请求 `allowed_space_ids` 与 `{owner_space_id} ∪ granted_space_ids` 相交，或请求 `allowed_document_ids` 显式命中。两个 allow-list 可以同时为空，此时只可能返回公开文档。

`activation_revision`、`access_revision`、`lifecycle_revision` 是三个独立高水位。低修订请求失败；同修订同 payload 幂等；同修订不同 payload 冲突。成功写入还会把 `operation_id` 绑定到 RPC 类型、规范化载荷和首次响应；同 ID 同载荷精确重放首次响应，同 ID 改绑其他载荷或 RPC 会冲突。`DeleteDocument` 按 `document_id + lifecycle_revision` 幂等并留下墓碑，旧事件不能复活文档。重新发布必须在更高 lifecycle 下先索引，再以不回退的 activation revision 激活。

P2.1 已将文档清单、版本状态、三类修订高水位、访问快照、墓碑、`operation_id` 首次响应及未完成向量操作持久化到独立 PostgreSQL 控制存储。P2.2 已把规范化控制投影写入 Qdrant，并在 dense/sparse 候选选择前下推 storage domain、ACL、活动版本和墓碑过滤；返回前仍执行契约复核与有界回填。P2.3–P2.5 的 gin-backend Outbox 投递 Worker、对账/重建命令与影子查询链路**已在 ADR-017 阶段 C 删除**，这些历史结论只说明当时的实现，不再代表当前可用的运维入口。当前 local-hash-v1 只用于确定性评估。

## 文档管道

`IngestFile` 根据扩展名选择独立加载管道，三种格式最终转换为统一的 `DocumentBlock`：

- `.md`、`.markdown`：移除 front matter 和 Markdown 行内标记，保留多级标题路径、正文及代码块。
- `.docx`：直接读取 ZIP 包内的 WordprocessingML，提取标题、段落和表格单元格文本。
- `.doc`：通过纯 Go `doc2txt` 解析旧版 OLE Word 二进制文件，再规范化为段落块。

结构化分块不会跨越章节边界，并把 `format`、`source`、`section` 元数据一直传递到检索结果及 Qdrant/pgvector。

支持以下 `VectorStore`：

- `memory`：内存 dense + BM25，开箱即跑。
- `qdrant`：一个 Collection 内使用 `content_dense` 与 `content_sparse` 命名向量；正式契约查询统一下推控制过滤，两路查询后由业务层 RRF 融合。
- `pgvector`：`vector` 余弦检索 + PostgreSQL `tsvector` 全文检索；两路查询后由业务层 RRF 融合。

这里将需求中的 `pv_vector` 按 PostgreSQL 生态的 `pgvector` 实现。示例仍使用本地哈希 Embedder，因此重点是验证 Eino 到持久化向量库的主链路，并非真实语义质量。

## 运行

本应用的运行入口是 `cmd/rag-server`（文档语料 gRPC 服务）与 `cmd/rag-healthcheck`（容器探针）。原先的手工演示与调试命令 `cmd/demo`、`cmd/rag-token`、`cmd/rag-grpc-client` 已在 ADR-017 阶段 C 退场：正式文档检索正被 `apps/document-search` 接管，仓库不再保留绕过契约、由本地进程手工签发 capability 的调试客户端。

启动本地依赖：

```powershell
docker compose up -d
```

Compose 包含控制 PostgreSQL、Qdrant 和 pgvector。只运行默认控制存储时可以执行：

```powershell
docker compose up -d --wait control-postgres
```

向量后端由 `cmd/rag-server -store` 选择：`memory`（仅测试与显式本地演示）、`qdrant`（首个生产候选，Go 客户端使用 gRPC 端口 `6334`）、`pgvector`（实验性替代，用 `-pg-dsn` 指定连接串）。向量存储可使用环境变量 `QDRANT_API_KEY` 和 `PGVECTOR_DSN`。各后端的集成测试见「验证」一节。

## gRPC 服务

服务端只接受携带调用方 capability 的请求：没有有效凭证的调用返回 `UNAUTHENTICATED`，角色不匹配返回 `PERMISSION_DENIED`，请求范围超出已授予范围同样返回 `PERMISSION_DENIED`。因此启动前必须准备边界密钥文件（仓库根执行一次 `./deployments/bootstrap.ps1` 会生成 `deployments/secrets/mixin_search_capability.key`）。

启动服务端（默认监听 `127.0.0.1:9090`，默认控制存储为 PostgreSQL）：

```powershell
docker compose up -d --wait control-postgres
go run ./cmd/rag-server -store memory -control-bootstrap `
  -capability-key-file ../../deployments/secrets/mixin_search_capability.key
```

默认控制连接为 `postgres://mixin_control:mixin_control@localhost:55432/mixin_control?sslmode=disable`，namespace 为 `default`。服务会幂等初始化 `mixin_search_control.control_states` schema，并以 namespace 隔离完整控制快照、使用 `generation` CAS 防止多实例覆盖写入。首次创建 namespace 时必须显式传入 `-control-bootstrap` 或设置 `CONTROL_STORE_BOOTSTRAP=true`；后续运行应去掉 bootstrap，缺失 namespace 将失败关闭。

控制存储参数：

| 命令行参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-control-store` | `CONTROL_STORE` | `postgres` | `postgres` 用于持久化运行；`memory` 仅用于测试和显式本地演示 |
| `-control-dsn` | `CONTROL_DATABASE_DSN` | 上述本地 DSN | 控制 PostgreSQL 连接串 |
| `-control-namespace` | `CONTROL_STORE_NAMESPACE` | `default` | 同一数据库中的控制状态隔离键 |
| `-control-bootstrap` | `CONTROL_STORE_BOOTSTRAP` | `false` | 仅首次创建缺失 namespace；正常运行保持关闭 |

调用边界参数：

| 命令行参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-capability-key-file` | `MIXIN_SEARCH_CAPABILITY_KEY_FILE` | 空 | 边界密钥文件；缺失即拒绝启动 |
| `-capability-issuer` | `MIXIN_SEARCH_CAPABILITY_ISSUER` | `go-web` | 唯一接受的 capability 签发方 |
| `-capability-audience` | `MIXIN_SEARCH_CAPABILITY_AUDIENCE` | `mixin-search` | 接受的 capability audience |
| `-caller-rate-per-second` | `MIXIN_SEARCH_CALLER_RATE_PER_SECOND` | `200` | 单调用方每秒请求预算；0 关闭限流 |
| `-caller-burst` | `MIXIN_SEARCH_CALLER_BURST` | `400` | 单调用方突发预算；0 关闭限流 |
| `-enable-reflection` | `MIXIN_SEARCH_ENABLE_REFLECTION` | `true` | gRPC reflection；端口对外暴露时必须关闭 |

凭据格式、角色划分与“只允许缩小”的范围规则见 [SERVICE_CALL_CAPABILITY.md](../../docs/contracts/SERVICE_CALL_CAPABILITY.md)。

完全内存化演示必须显式选择 memory 控制存储；这种方式不提供进程重启持久性：

```powershell
go run ./cmd/rag-server -store memory -control-store memory `
  -capability-key-file ../../deployments/secrets/mixin_search_capability.key
```

索引先持久化带 15 分钟租约的 pending write，再写入向量，最后提交版本映射和首次响应；删除先持久化逻辑删除、墓碑和 pending delete，再清理物理向量。generation 冲突返回 `ABORTED`，控制存储不可用或快照损坏返回 `UNAVAILABLE`。完整故障收敛语义见 [ADR-006](../../docs/adr/006-mixin-search-control-state-commit-order.md)。

索引写入与检索是不同角色，因此调用方需要两个 capability。阶段 C 之前由本模块的开发工具 `cmd/rag-token` 手工签发、`cmd/rag-grpc-client` 发起最小调用；两者已随 `cmd/demo` 一起删除，**当前模块内没有等价的手工调试入口**。需要端到端验证请使用仓库级验收脚本与包内集成测试：

```powershell
# 仓库根：启动根 Compose 并跑跨服务验收
./deployments/verify.ps1

# 本模块：控制面与 Qdrant 的一次性环境验收
./verify-control-store.ps1
./verify-qdrant-control.ps1
```

capability 的签发方固定为 `go-web`（见上文调用边界参数），签发与校验格式见 [SERVICE_CALL_CAPABILITY.md](../../docs/contracts/SERVICE_CALL_CAPABILITY.md)。

服务端注册标准 grpc.health.v1.Health，空服务名和 mixin_search.v1.RAGService 均在成功完成存储初始化后报告 SERVING。健康检查刻意不要求 capability，容器探针因此不需要持有密钥。

Qdrant 是首个完成候选级 ACL/活动版本/生命周期过滤的持久化后端；pgvector 保留为实验性替代，memory 仅用于测试和演示。P2.2 的本地候选语义通过不表示生产流量已经接入：

```powershell
go run ./cmd/rag-server -store qdrant -qdrant-host localhost -qdrant-port 6334 -control-bootstrap `
  -capability-key-file ../../deployments/secrets/mixin_search_capability.key
go run ./cmd/rag-server -store pgvector -pg-dsn "postgres://rag:rag@localhost:5432/rag?sslmode=disable" -control-bootstrap `
  -capability-key-file ../../deployments/secrets/mixin_search_capability.key
```

远程接口接收文件名和文件字节，不接收服务端本地路径；`.md`、`.markdown`、`.doc`、`.docx` 都会进入已有的独立文档管道。默认单次请求上限为 16 MiB，可分别通过服务端 `-max-receive-bytes` 和客户端 `-max-send-bytes` 调整。

重新生成 Protobuf 代码（两个语料契约各自独立）：

```powershell
protoc -I ../.. `
  --go_out=../../packages/gen --go_opt=module=packages/gen `
  --go-grpc_out=../../packages/gen --go-grpc_opt=module=packages/gen `
  ../../packages/proto/mixin-search/v1/mixin-search.proto `
  ../../packages/proto/mixin-search/chat/v1/chat.proto
```

`-I` 必须是仓库根：生成文件的包路径由 `go_package` 决定，`--go_opt=module=packages/gen` 只是剥掉该前缀，因此产物相对 `--go_out` 落在 `mixin-search/v1/` 与 `mixin-search/chat/v1/`。

## 验证

从仓库根目录先运行 `packages/proto/verify-generated.ps1` 检查协议生成物。契约与控制状态测试覆盖 `operation_id` 精确重放与改绑冲突、重复/冲突业务键、三类低修订事件、删除后的迟到事件、授权撤销后的迟到更新、空 allow-list 的公开搜索、显式文档授权、更高 lifecycle 的重新发布、服务实例恢复、generation CAS、失败关闭和未完成向量意图收敛。

```powershell
go test ./internal/architecture
go test ./...
go vet ./...
docker compose config --quiet
```

仓库级跨服务验收从仓库根目录执行（脚本由仓库主线维护）：

```powershell
./deployments/verify.ps1
```

根 Compose 使用本镜像内的 `rag-healthcheck` 执行标准 gRPC Health 探测。

控制 PostgreSQL 与 Qdrant 集成测试默认跳过。推荐使用一次性随机端口、空数据卷和随机 Compose project 的验收脚本，结束后默认删除容器、网络和数据卷：

```powershell
./verify-control-store.ps1
./verify-qdrant-control.ps1
```

已有控制 PostgreSQL 时也可手工运行：

```powershell
$env:CONTROL_STORE_INTEGRATION = "1"
$env:CONTROL_DATABASE_DSN = "postgres://mixin_control:mixin_control@localhost:55432/mixin_control?sslmode=disable"
go test ./internal/rag -run TestPostgresControlStoreIntegration -count=1 -v
```

`verify-qdrant-control.ps1` 运行基础存储、控制过滤和 storage domain 隔离测试，通过时输出 `P2.2_QDRANT_CONTROL=PASS`。已有 Qdrant 或 pgvector 时也可按需手工启用：

```powershell
$env:QDRANT_INTEGRATION = "1"
$env:PGVECTOR_INTEGRATION = "1"
go test ./internal/rag -run 'TestQdrant.*Integration|TestPGVectorStoreIntegration' -v
```

## 代码入口

- `internal/rag/workflow.go`：Eino Ingest/Search 工作流和业务 Service。
- `document_pipeline/types.go`：统一的 Pipeline、Block、FileRequest 与 LoadedDocument 类型。
- `document_pipeline/registry.go`：扩展名路由、管道注册和公共文档元数据准备。
- `document_pipeline/markdown.go`：Markdown 标题、正文、代码块解析。
- `document_pipeline/docx.go`：DOCX WordprocessingML 段落与表格解析。
- `document_pipeline/doc.go`：旧版 DOC/OLE 文本解析。
- `internal/rag/store.go`：`VectorStore` 接口及内存实现。
- `internal/rag/control_store.go`：与传输无关的控制状态模型、`ControlStore` 端口及 pending 向量操作收敛。
- `internal/rag/control_store_memory.go`：单元测试和显式本地演示使用的内存控制存储。
- `internal/rag/control_store_postgres.go`、`internal/rag/control_schema.sql`：默认 PostgreSQL 控制存储和 schema 基线。
- `internal/rag/control_store_test.go`、`internal/rag/control_store_integration_test.go`：重启、并发、失败关闭和真实 PostgreSQL 验证。
- `internal/rag/store_qdrant.go`：Qdrant dense/sparse 命名向量、规范化控制 payload 和候选级过滤实现。
- `internal/rag/store_pgvector.go`：pgvector + PostgreSQL 全文检索实现。
- `../../packages/proto/mixin-search/v1/mixin-search.proto`：monorepo 中统一维护的版本化文档索引 gRPC 契约。
- `../../packages/proto/mixin-search/chat/v1/chat.proto`：聊天语料的独立 gRPC 契约（[契约说明](../../docs/contracts/CHAT_SEARCH_V1_CONTRACT.md)），与文档契约不共享控制面或集合。
- `../../packages/gen/mixin-search/v1`、`../../packages/gen/mixin-search/chat/v1`：由协议生成并供应用共享的 Go 类型与 gRPC 代码。
- `internal/transport/grpc/server.go`：Protobuf DTO、gRPC 状态码与共享业务 Service 的适配层。
- `internal/transport/grpc/auth.go`：调用方 capability 认证、角色权限与方法策略的 gRPC 拦截器。
- `internal/security/capability.go`：capability 的签名、校验与“只允许缩小”的范围判定。
- `internal/security/identity.go`：已验证身份在请求上下文中的传递与结构化审计记录。
- `internal/security/limiter.go`：按调用方的令牌桶限流。
- `cmd/rag-server/main.go`：gRPC 监听、边界认证、后端选择与服务注册入口。
- `cmd/rag-healthcheck/main.go`：根 Compose 使用的标准 gRPC Health 探针。
- `verify-control-store.ps1`：创建并清理一次性控制 PostgreSQL 的 P2.1 验收入口。
- `verify-qdrant-control.ps1`：创建并清理一次性 Qdrant 的 P2.2 验收入口。
- `examples/knowledge.md`：可直接运行的 Markdown 示例文档。

阶段 C 退场的入口：`cmd/demo`（后端切换与混合查询演示）、`cmd/rag-grpc-client`（最小远程调用客户端）、`cmd/rag-token`（本地 capability 签发工具）。文档与聊天检索能力接管到 `apps/document-search` 与 `apps/qq-search` 后不再保留这些绕过契约的手工工具。
