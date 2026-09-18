# P3.3 聊天语料容器级验收证据（2026-09-19）

本文件记录 P3.3（多语料契约与索引隔离）在**容器内**取得的聊天语料专项证据。同一次运行的三个通用报告族见
`full-api-p15_20260919_001036`、`full-api-p24_outage_20260919_001045`、
`document-search-evaluation-p25_20260919_001041`；本文件只记录那些报告族没有覆盖的部分。

验收时提交：`f60233f`（含本轮新增的两步门禁与宿主发布端口参数化）。

## 运行方式

由 `deployments/verify.ps1` 从空数据卷构建完整根 Compose 环境（`GIN_BACKEND_PORT=18080`，
因为宿主 Windows 保留了 TCP 8070–8169，默认的 8080 无法绑定）。本轮在该门禁中新增两步：

1. `Run chat corpus container acceptance` → `go test ./cmd/rag-server -run TestChatCorpusAgainstADeployedStack`；
2. `Verify the corpora keep separate control namespaces` → 对 `control-postgres` 执行 psql 断言。

## 证据 1：服务与两个语料的装配

容器日志（mixin-search）：

```
RAG gRPC server listening on [::]:9090 (store=qdrant control_store=postgres chat=true
 chat_collection=go_web_chat_v1 issuer=go-web audience=mixin-search throttling=true reflection=false)
```

即：同一个进程同时提供文档语料与聊天语料，聊天语料启用在自己的 collection
`go_web_chat_v1` 上，控制状态走后端 `postgres`，而文档集合是 `go_web_shadow_v1`。

## 证据 2：控制状态在 PostgreSQL 中互不相干

`verify.ps1` 的输出行：

```
PASS control namespace isolation: chat=[chat-v1] documents=[go-web-shadow-v1]
```

另有一次聊天写入（索引 + 归档 + 撤回 + 幂等重放）之后的直接查询：聊天表
`mixin_search_control.chat_control_states` 只有 `chat-v1` 且 `generation = 4`，
文档表 `mixin_search_control.control_states` 只有 `go-web-shadow-v1` 且 `generation = 0`。

这正是 ADR-014 两条最低验收条件的容器内证据：

- 聊天 generation 变化（0 → 4）**没有**触发文档快照重新加载（文档 generation 仍为 0）；
- 聊天活动**没有**增大文档控制快照（文档行的 payload 未被触碰）。

## 证据 3：对部署端点的聊天契约验收

`TestChatCorpusAgainstADeployedStack`（门控 `CHAT_CONTAINER_INTEGRATION=1`）对着
`127.0.0.1:19090` 的真实 gRPC 端点断言，并在门禁中 `--- PASS`：

- 两个语料的健康检查都返回 `SERVING`（聊天语料未启用时该服务根本不注册）；
- 索引后**不可检索** → 归档后**可检索** → 撤回后**不可检索但计数仍为 1**（三态独立，走真实
  Qdrant collection 与真实 PostgreSQL 控制状态）；
- 文档凭证调用聊天 RPC 返回 `PermissionDenied`（角色集合不相交）；
- 用文档凭证检索同一关键词，文档语料返回 0 条（聊天内容不出现在文档集合）；
- 用同一 `operation_id` 重放索引，返回持久化账本里记录的响应而不是重复写入。

## 未覆盖的部分

- 没有直接列出 Qdrant 的 collection 清单：qdrant 镜像内没有 `curl`，且该端口未发布到宿主，
  因此 collection 的存在是由"向量确实写入并经检索命中"间接证明的；
- Qdrant alias 的原子切换仍未实现（两侧都没有 alias，属于 P3.5 蓝绿重建路径）；
  P3.3 的"聊天重建不切换文档 alias"当前由"集合名独立 + 聊天重建只操作自己的集合"保证。
