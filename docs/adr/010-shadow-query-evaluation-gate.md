# ADR-010：异步影子查询、来源分层观测与读取切换门禁

> 状态：已接受
> 日期：2026-09-14

## 背景

P2.4 已持续把 Documents 事实投影到 mixin-search，但正式 HTTP 搜索仍只读取 PostgreSQL BM25。P2.5 需要对同一查询运行混合检索并记录差异，同时保证远程超时、停机和索引短暂滞后都不会进入正式响应链路。

SearchMine 是 owner-only 查询，而 mixin-search/v1 的授权语义是“公开、允许空间、显式文档”三路 OR。即使某个公开候选在协议上合法，它也可能不属于 SearchMine 的正式产品范围，因此评估必须区分协议权限违规与正式范围差异。

## 决策

### 1. 正式结果先完成，影子任务非阻塞提交

QueryService 先完成 PostgreSQL BM25 查询并确定 HTTP 返回，再把归一化查询、分页和 BM25 文档 ID 提交到有界内存队列。提交不等待远程调用；队列已满时记录 dropped 日志并立即返回。mixin-search 不进入 gin-backend readiness。

### 2. 查询正文不进入观测表

document_search_shadow_observations 只保存查询 SHA-256 和字符数，不复制用户查询正文。记录包括 runtime/evaluation 来源、BM25 与影子文档 ID、可比较的 owner-only 结果、两侧差异、耗时、gRPC 状态、超时和错误。

### 3. 用 gin-backend 事实复核每个候选

影子结果返回后，以当前 documents、document_access_policies 和活动版本事实分别检查：

- 非公开候选是否属于请求允许空间；
- 文档是否仍为 active；
- 命中版本是否仍为当前活动版本；
- 候选是否属于 SearchMine 的 owner-only 正式范围。

前三项分别记录权限、生命周期和活动版本违规；合法公开但不属于当前 owner 的结果记录为 formal scope mismatch，不与协议权限违规混合。

### 4. runtime 与 evaluation 分开判定

runtime 观测保留写后 Outbox 尚未收敛时的短暂版本/删除滞后，也记录停机期间的超时和错误；这些事实用于判断传播窗口和故障隔离。读取切换正确性门禁只使用完成 Outbox 收敛后的 evaluation 来源，避免把预期的异步传播窗口伪装成稳定索引违规。

### 5. 质量高分不自动批准读取切换

固定数据集覆盖中文、英文、代码、标题、正文、精确关键词和语义表达，并同时报告 Recall@K、MRR、nDCG、延迟和错误。报告还必须声明 embedding profile。评估型 local-hash profile 即使在小样本上超过数值阈值，也不能获批语义读取切换；正式切换仍需非评估型语义 embedding 和扩大后的真实标注集。

## 结果

- 正式 Documents/BM25 的返回值、延迟预算和可用性边界保持不变；
- 运维入口 shadow-status 可按 runtime 或 evaluation 来源汇总观测；
- 根验收在 mixin-search 停机期间继续通过 95 项 API 回归，并确认影子错误计数增长；
- 2026-09-14 的最终七类评测中，BM25 的 Recall@5/MRR/nDCG@5 为 1.0000/1.0000/1.0000，影子检索为 1.0000/0.9048/0.9286，影子 p95 为 459029 us；
- 收敛后的 evaluation 权限、生命周期、活动版本和正式范围差异均为 0；
- 当前 embedding profile 为 local-hash-v1，因此结论是 KEEP_BM25，不建立读取切换。

## 验证

- gin-backend 全量 go test 与 go vet；
- 固定 document-search-v1 数据集和 JSON/Markdown 双格式报告；
- 根 deployments/verify.ps1 的空卷构建、正常/停机 API 回归、观测来源门禁和恢复排空；
- 最终标记 P2.5_SHADOW_QUERY_EVALUATION=PASS。
