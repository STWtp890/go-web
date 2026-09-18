# P3.3 聊天语料容器级验收证据（2026-09-19）

本文件记录 P3.3（多语料契约与索引隔离）在**容器内**取得的聊天语料专项证据。同一次运行的三个通用报告族见
`full-api-p15_20260919_040430`、`full-api-p24_outage_20260919_040439`、
`document-search-evaluation-p25_20260919_040435`；本文件只记录那些报告族没有覆盖的部分。

验收时提交：`a544a34`（功能树最后变更；含按语料划分 audience、Qdrant alias 与 `_g1` 基线、容量与账本保留机制、官方形式的 alias 切换与失败补偿，以及两轮对抗性复审后的修复）。

## 运行方式

由 `deployments/verify.ps1` 从空数据卷构建完整根 Compose 环境（`GIN_BACKEND_PORT=18080`，
因为宿主 Windows 保留了 TCP 8070–8169，默认的 8080 无法绑定）。本轮在该门禁中共有四步与语料隔离相关：

1. `Run chat corpus container acceptance` → `go test ./cmd/rag-server -run TestChatCorpusAgainstADeployedStack`；
2. 采样两个语料的控制 generation（在步骤 1 之前）并在其后比较；
3. `Verify the chat alias switches without moving the document alias` → `go test ./cmd/rag-server -run TestChatAliasSwitchAgainstADeployedStack`；
4. `Verify the corpora keep separate control namespaces` → 对 `control-postgres` 执行 psql 断言。

## 证据 1：服务与两个语料的装配

容器日志（mixin-search）：

```
RAG gRPC server listening on [::]:9090 (store=qdrant control_store=postgres chat=true
 chat_collection=go_web_chat_v1 issuer=go-web audience=mixin-search throttling=true reflection=false)
```

即：同一个进程同时提供文档语料与聊天语料，聊天语料启用在自己的 collection
`go_web_chat_v1` 上，控制状态走后端 `postgres`，而文档集合是 `go_web_shadow_v1`。

## 证据 2：控制状态在 PostgreSQL 中互不相干

门禁输出行：

```
==> Sample control generations before chat activity: document=0 chat=0
PASS control generation isolation: chat 0 -> 4, document unchanged at 0
PASS control namespace isolation: chat=[chat-v1] documents=[go-web-shadow-v1]
```

第一条是 ADR-014 第 1 条的可重复断言：这一轮聊天写入（索引、归档、撤回、幂等重放）把聊天
namespace 从 generation 0 推到 4，而文档 namespace 在同一窗口内保持 0——**聊天 generation
变化没有推进文档控制面**。该断言每次整栈门禁都会执行，不再是专项取证。

第二条证明两个语料各自拥有独立持久化状态：聊天表 `chat_control_states` 只有 `chat-v1`，
文档表 `control_states` 只有 `go-web-shadow-v1`。

## 证据 3：对部署端点的聊天契约验收

`TestChatCorpusAgainstADeployedStack`（门控 `CHAT_CONTAINER_INTEGRATION=1`）对着
`127.0.0.1:19090` 的真实 gRPC 端点断言，并在门禁中 `--- PASS`：

- 两个语料的健康检查都返回 `SERVING`（聊天语料未启用时该服务根本不注册）；
- 索引后**不可检索** → 归档后**可检索** → 撤回后**不可检索但计数仍为 1**（三态独立，走真实
  Qdrant collection 与真实 PostgreSQL 控制状态）；
- 文档凭证、以及"聊天角色 + 文档 audience"的混合凭证调用聊天 RPC 均返回 `Unauthenticated`
  （audience 锁先于角色判定），聊天凭证调文档 RPC 同理；
- 用文档凭证检索同一关键词，文档语料返回 0 条（聊天内容不出现在文档集合）；
- 用同一 `operation_id` 重放索引，返回持久化账本里记录的响应而不是重复写入。

## 证据 3：alias 独立、切换原子且可补偿（部署栈上的直接映射断言）

容器日志确认配置名是 alias，并**直接列出**物理集合与 alias 表（不再依赖容器内 `curl`）：

```
qdrant collections: [go_web_chat_v1_g1 go_web_shadow_v1_g1]
alias "go_web_chat_v1" -> collection "go_web_chat_v1_g1"
alias "go_web_shadow_v1" -> collection "go_web_shadow_v1_g1"
```

切换本身使用 Qdrant 官方文档形式：同一次 `UpdateAliases` 里提交 `Delete(alias)` + `Create(alias→目标)`。
它的语义需要准确表述，既不能说"完全原子"，也不能说"不原子"：

> **Alias 切换对并发观察者原子可见，但不是失败全回滚事务**——服务端在整批期间持有 alias 写锁，
> 其他请求看不到批次执行到一半的中间状态；但批内 action 顺序执行、边做边改 alias 映射，遇到错误
> 直接返回且**没有 undo**，因此失败可能留下前缀效果（删掉了旧映射、新映射没建起来）。
> 应用侧据此执行**显式补偿**恢复原映射。

该结论由锁定版本（qdrant v1.19.1）上的决定性实验确定：

```
observed server behaviour: a failed create action leaves alias "..." deleted (no rollback)
```

`SwitchAlias` 因此在批次失败时把 alias 重新建在原集合上（错误里带上原始失败与修复结果），
并有 `TestQdrantAliasBatchFailureIsCompensated` 证明补偿有效；`RestoreAlias` 是同一路径的操作员入口。
补偿的已知边界（单写者/租约、补偿竞态、预检、告警）登记在 P3.5 待办中，不属于本包。

`TestChatAliasSwitchAgainstADeployedStack` 对着**运行中的部署**执行并 PASS：两个配置名必须是
alias 且**精确指向** `_g1`（只要求 `_g1` 后缀会让"两个语料共用同一集合"也判为健康）；断言两个
`_g1` 物理集合确实存在；把 chat alias 切到新建的空世代后聊天检索立即为空（无需重启），
**文档 alias 的映射保持不变**；切回后聊天检索恢复；切到不存在的集合被拒绝且映射不动；清理阶段
恢复**两个** alias，任一映射无法恢复时不删除本测试创建的世代，并在结束时报告两个 alias 的最终
指向与临时世代确实已删除。

## 未覆盖的部分

- 没有直接列出 Qdrant 的 collection 清单：qdrant 镜像内没有 `curl`；alias 与物理集合的存在
  由 qdrant gRPC 客户端（`ListAliases` 与集合存在性检查）以及"向量写入并检索命中"共同证明；
- 蓝绿重建的**编排**（把数据填进新世代、完整性校验、保留上一代用于回退）仍未实现，属 P3.5；
  本片交付的是 alias 机制、切换与切换的隔离性证明；
- 聊天控制快照的容量上限**数值已于 2026-09-19 写入根 Compose 并验证生效**（A 档：
  30,000 条 / 24 MiB / 账本 7 天与 30,000 条，metadata 预算 8 项与 32/64/64 字节；本次容器
  验收运行时这些值尚未写入，因此本条目的边界语义不由本次运行证明）。测量数据、档位推导与启用
  验证见 `p33-chat-capacity-profile_20260919.md`；`maxMessages` 是第二道保险而非产品容量指标，
  有效容量取 `min(条数, 字节)` 的先到达者。仍未完成的是 P3.6 的 metadata 接入约定；
- 远程 CI 没有本次提交的运行记录，这里只声明本地门禁与容器验收的结果。
