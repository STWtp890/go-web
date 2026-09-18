# P3.3 聊天语料容器级验收证据（2026-09-19）

本文件记录 P3.3（多语料契约与索引隔离）在**容器内**取得的聊天语料专项证据。同一次运行的三个通用报告族见
`full-api-p15_20260919_031530`、`full-api-p24_outage_20260919_031539`、
`document-search-evaluation-p25_20260919_031536`；本文件只记录那些报告族没有覆盖的部分。

验收时提交：`2d6a184`（含按语料划分 audience、Qdrant alias 与 `_g1` 基线、以及门禁里的
generation 与 alias 断言；此前的 `bf2ec4a` 运行只有 namespace 断言与专项取证，已被本次取代）。

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

## 证据 3：alias 独立且切换原子（部署栈上的直接映射断言）

容器日志确认配置名是 alias：

```
document corpus alias "go_web_shadow_v1" -> physical collection "go_web_shadow_v1_g1"
chat corpus alias "go_web_chat_v1" -> physical collection "go_web_chat_v1_g1"
```

`TestChatAliasSwitchAgainstADeployedStack` 对着**运行中的部署**执行并 PASS：两个配置名必须是
alias 且指向 `_g1`；把 chat alias 切到一个新建的空世代后，聊天检索立即为空（无需重启），
**文档 alias 的映射保持不变**；切回后聊天检索恢复；切到不存在的集合被拒绝且映射不动。
测试无论成败都恢复原映射并删除它创建的那个世代。

## 未覆盖的部分

- 没有直接列出 Qdrant 的 collection 清单：qdrant 镜像内没有 `curl`；alias 与物理集合的存在
  由 qdrant gRPC 客户端（`ListAliases` 与集合存在性检查）以及"向量写入并检索命中"共同证明；
- 蓝绿重建的**编排**（把数据填进新世代、完整性校验、保留上一代用于回退）仍未实现，属 P3.5；
  本片交付的是 alias 机制、切换与切换的隔离性证明；
- 聊天控制快照的容量上限尚未写入配置：已提交 1k/10k/50k 的实测数据与建议值，等待确认
  （见实施计划 §7.1 的"容量测量与上限建议"）；
- 远程 CI 没有本次提交的运行记录，这里只声明本地门禁与容器验收的结果。
