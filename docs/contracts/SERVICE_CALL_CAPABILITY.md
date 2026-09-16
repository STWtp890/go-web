# mixin-search 调用方 capability 契约

> 状态：P3.1 已落地（调用身份、角色权限、范围包含校验、审计与限流）
> 日期：2026-09-17
> 相关决策：[ADR-012](../adr/012-multi-consumer-search-boundary-and-critical-path-shift.md) 决策 4
> 相关契约：[mixin-search/v1 文档索引契约](./MIXIN_SEARCH_V1_CONTRACT.md)

## 1. 目的

`mixin-search` 只对能够证明身份的调用方提供服务。本契约定义调用方如何证明“我是谁、我代表谁、我可以在多大范围内检索”，以及服务端如何校验。

它解决的是此前的一个 **capability confusion**：请求中的 `allowed_space_ids` / `allowed_document_ids` 曾被直接当作授权输入执行，因此任何能连上端口的调用方都可以自行扩大检索范围。

现在这项声明必须由事实源签发，且服务端校验 `requested ⊆ granted`。

## 2. 角色

角色决定**可调用哪些 RPC**，不决定数据范围。

| 角色 | 允许的 RPC | 持有者 |
| --- | --- | --- |
| `index-writer` | `IndexDocumentVersion`、`ActivateDocumentVersion`、`UpdateDocumentAccess`、`DeleteDocumentVersion`、`DeleteDocument`、`GetDocumentVersionState` | `go-web` 的索引 Worker、对账与全量重建 |
| `searcher` | `SearchDocuments` | `go-web` 影子查询、检索评测，以及后续经 `go-web` 授权的 `py-agent` |
| `ops` | `GetDocumentVersionState` | 运维/诊断工具，只读且不能检索或写入 |

索引写入与检索刻意分离：负责写派生索引的进程不是提供检索的进程，因此被攻陷的检索调用方无法改写它读取的内容。`SearchDocuments` 不接受 `index-writer`，即使请求只针对公开文档。

未在角色表中登记的方法一律拒绝（失败关闭）；新增 RPC 时必须同时登记策略，否则运行期拒绝，且 `internal/transport/grpc` 的测试会直接失败。

## 3. 凭据格式

调用方在 gRPC metadata 中携带：

```text
authorization: Bearer <token>
```

`token` 为：

```text
base64url(payload_json) "." base64url(HMAC-SHA256(key, base64url(payload_json)))
```

刻意不包含算法头：唯一可接受的算法是与边界密钥绑定的 HMAC-SHA256，调用方没有可协商或可降级的空间。

### 3.1 payload

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | int | 固定为 `1` |
| `issuer` | string | 签发方标识，例如 `go-web` |
| `subject` | string | 调用方标识，例如 `go-web-index-worker` |
| `audience` | string | 被调服务标识，必须是 `mixin-search` |
| `role` | string | `index-writer`、`searcher` 或 `ops` |
| `user_id` | string | 可选；调用方代表的最终用户，只用于审计 |
| `issued_at` | int64 | Unix 秒 |
| `expires_at` | int64 | Unix 秒，必须晚于 `issued_at` |
| `allowed_space_ids` | string[] | 可选；仅 `searcher` 携带，已授予的空间范围 |
| `allowed_document_ids` | string[] | 可选；仅 `searcher` 携带，已授予的文档范围 |

写入角色签发的 token 不携带范围字段：它的权限来自角色，因此一份泄露的写入 capability 不能当作受限检索重放。

### 3.2 密钥

- 只有 `go-web`（签发方）与 `mixin-search`（校验方）持有边界密钥；
- 密钥至少 32 字节，由 `deployments/bootstrap.ps1` 随机生成到 `deployments/secrets/mixin_search_capability.key`；
- 密钥文件不入库也不入镜像（`.gitignore` / `.dockerignore` 的 `*.key`），由 Compose 以只读卷挂载；
- 缺少密钥时 `mixin-search` 拒绝启动，而不是退化为接受匿名调用；
- 真实消费者（`py-agent`）**不得**持有该密钥，只能使用 `go-web` 签发的短时 capability。

## 4. 校验规则

服务端按顺序执行，任一步失败即拒绝：

1. 存在且仅存在一个 `authorization` 值，并使用 `Bearer` 方案；
2. 签名与边界密钥匹配（常量时间比较）；
3. payload 可解析、无未知字段、`version` 受支持、必填声明非空、`expires_at > issued_at`；
4. `audience` 等于本服务标识；
5. 当前时间不超过 `expires_at`（允许 30 秒时钟偏差），且 `issued_at` 不晚于当前时间加同一偏差；
6. 调用方角色在该方法的策略中；
7. 调用方未超出其请求预算；
8. `SearchDocuments` 的 `allowed_space_ids` 与 `allowed_document_ids` **全部**包含在已授予范围内。

失败时返回：

| 场景 | gRPC 状态 |
| --- | --- |
| 缺少、格式错误、签名无效、已过期、audience 不符 | `UNAUTHENTICATED` |
| 角色不允许该方法 | `PERMISSION_DENIED` |
| 请求范围超出已授予范围 | `PERMISSION_DENIED` |
| 超出该调用方的请求预算 | `RESOURCE_EXHAUSTED` |

`UNAUTHENTICATED`、`PERMISSION_DENIED` 在 `go-web` 客户端中归类为**不可重试**；`RESOURCE_EXHAUSTED` 可重试，因此限流会转化为投递退避而不是失败。

## 5. 范围规则：只允许缩小

范围判定是**包含**，不是取交集：

- 请求范围是已授予范围的子集（含空集，即只检索公开文档）→ 允许；
- 请求中出现任何未授予的空间或文档 → **整体拒绝**。

不允许“静默裁剪后按子集检索”：那会让调用方探测它无权读取的标识符，并返回它从未证明过的范围的结果。

空间与文档标识在签发和校验两侧都做规范化（去空白、去重、排序），因此集合顺序或重复项不会改变判定结果。

## 6. 审计与限流

每次受保护调用都产生一条结构化审计记录，回答“谁、代表谁、在什么范围内、调用了什么、结果如何”：

- 调用方标识、角色、最终用户；
- 方法名；
- 请求范围大小与已授予范围大小；
- 结果（`allowed` / `denied` / `throttled`）、状态码与耗时。

审计记录**不包含**查询文本或文档内容。

限流是按调用方的令牌桶，默认 200 请求/秒、突发 400，可配置或关闭。生产环境中的服务健康检查（`grpc.health.v1.Health`）刻意不要求 capability，使容器探针无需持有密钥；它只暴露 SERVING 状态。

## 7. 落地位置

- 认证与授权在**传输边界**执行（`internal/transport/grpc/auth.go`）；`internal/rag` 保持不感知身份，因此同一领域实现仍可被其他适配器（例如未来的 MCP 适配器）复用，并由各自边界负责认证；
- `go-web` 侧在 `internal/modules/document/infrastructure/mixinsearch` 逐调用签发 capability：写调用用 `index-writer`，检索调用用 `searcher` 并把本次请求的 allow-list 作为已授予范围写入 token；
- 可执行的格式约定由两侧的同一条 golden vector 固定：任何一侧改动格式都会让另一侧测试失败。

## 8. 尚未建立的部分

- **capability 签发入口**：`py-agent` 如何向 `go-web` 换取短时 capability（含 QQ 身份绑定与范围计算）属于 P3.4/P3.6；
- **传输加密**：当前仍为明文 gRPC，仅绑定回环地址；mTLS 属于后续在线暴露前的加固项；
- **按用户/会话的配额**：当前限流按调用方，不分用户；
- **reflection**：仍由服务端开关控制，本地默认开启，根 Compose 已关闭。
