# ADR-009：影子索引 Compose 装配与健康边界

> 状态：已接受
> 日期：2026-09-14

## 背景

P2.3 已建立与 Documents 事务同提交的 Outbox、独立 Worker、重试、死信、对账和全量重建。P2.4 需要让这条链路在统一开发环境持续运行，同时保证可重建的影子依赖不会改变正式 Documents 与 PostgreSQL BM25 的可用性。

## 决策

### 1. 根 Compose 统一装配八个默认服务

根 Compose 默认运行 PostgreSQL、Redis、gin-backend、simple-frontend、控制 PostgreSQL、Qdrant、mixin-search 与 document-index-worker。控制 PostgreSQL 与 Qdrant 只在 Compose 网络内提供服务；mixin-search gRPC 仅映射到主机回环地址。

gin-backend 工作区镜像同时构建 HTTP、Worker 与管理命令，mixin-search 镜像同时构建 gRPC 服务与健康检查器。应用容器使用非 root 用户、只读根文件系统、临时 `/tmp`、移除 Linux capabilities，并启用 `no-new-privileges`。

### 2. 正式就绪性与影子健康分离

gin-backend `/readyz` 只检查正式请求所需的 PostgreSQL 与 Redis，不依赖 mixin-search、Qdrant、控制 PostgreSQL 或 Outbox 是否为空。mixin-search 使用标准 gRPC Health；Worker 容器通过 `document-index-admin status` 检查 Outbox 数据库可读性。

`status` 同时报告 mixin-search 可用性、各投递状态数量、过期租约、失败次数、最老未完成事件与最后投递延迟。mixin-search 不可用会出现在状态 JSON 中，但不会让 Worker 容器失去健康；数据库状态不可读才失败。

### 3. 开关只控制消费

`document_index_delivery.enabled` 只控制 Worker 是否消费事件。Documents 事务无论开关状态都写入 Outbox，避免停用消费期间丢失已提交事实。恢复消费后继续按 P2.3 的顺序、租约和幂等规则收敛。

### 4. 当前模型严格失败

对账和重建只接受当前文档模型：文档、活动版本和访问策略必须满足现行完整性约束。缺失访问策略等不完整记录视为数据错误，不引入历史数据兼容分支；测试产生的临时不完整聚合必须在测试边界内补齐并清理。

### 5. 影子运行不改变正式读取

根验收先验证正常流量排空，再停止 mixin-search 并运行完整 API 回归，确认 Documents 和 PostgreSQL BM25 保持可用；恢复 mixin-search 后等待积压自动排空。Worker 崩溃与租约接管继续由 P2.3 专项门禁覆盖，不在 P2.4 重复制造长租约。

## 结果

- 从空数据卷可以一条命令启动完整影子索引栈；
- 影子依赖故障不会改变正式 HTTP readiness 或 BM25 结果；
- 运维状态能够区分 HTTP 就绪、gRPC 可用和投递积压；
- 恢复后不依赖人工重放即可排空可重试积压；
- PostgreSQL BM25 继续是正式读取方，影子查询与质量评估属于 P2.5。

## 验证

- `docker compose build gin-backend mixin-search`：workspace-aware 多阶段镜像构建；
- `deployments/verify.ps1`：空卷启动、八服务健康、正常与停机期间两轮 95 项 API 回归、积压观测和自动恢复；
- 通过标记：`P2.4_SHADOW_INDEX=PASS` 与 `P1.5_BUILD_TEST_DEPLOYMENT=PASS`。
