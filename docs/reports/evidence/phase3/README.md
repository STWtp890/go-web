# 阶段 3 证据快照

本目录保存阶段 3 实施包引用的不可变验收证据。规则与 `docs/reports/evidence/README.md` 一致：这里的内容不随后续实现改写，正式文档只链接本目录，链接到会被清理的 `deployments/test-results/` 由 `docs/check-doc-links.ps1` 拒绝。

## 归档内容

| 实施包 | 验收时提交 | 结果报告 | 说明 |
| --- | --- | --- | --- |
| P3.1a | `54cc1c8` | `full-api-p15_20260917_044228`、`document-search-evaluation-p25_20260917_044232`、`full-api-p24_outage_20260917_044235` | 可信签发方绑定 + 限流调用方表硬上限 |
| P3.2 | `8e7abea` | `full-api-p15_20260917_045334`、`document-search-evaluation-p25_20260917_045338`、`full-api-p24_outage_20260917_045341` | 控制面不可变快照 + 后台投影收敛 |
| P3.3 | `5483b65` | `full-api-p15_20260919_034719`、`document-search-evaluation-p25_20260919_034724`、`full-api-p24_outage_20260919_034727`，另加 `p33-chat-corpus-container_20260919` | 多语料契约与索引隔离：独立契约、独立控制面、按语料 audience、Qdrant alias 与 `_g1` 基线、容量与账本保留机制、容器级验收（口径：上限数值待确认） |

每个运行包含三个报告族（正常拓扑 API 回归、停机拓扑 API 回归、检索质量评估），各有 `.json` 与 `.md` 两种形式。P3.3 的聊天语料专项证据（容器日志、控制 namespace 隔离、对部署端点的三态与隔离验收）记在 `p33-chat-corpus-container_20260919.md`，因为三个通用报告族覆盖的是文档路径。

## 验收范围与结果

两次运行都由 `deployments/verify.ps1` 从空数据卷构建完整根 Compose 环境，覆盖：

- Proto 生成物一致性、文档链接门禁、前端类型检查与构建；
- 三个 Go module 的 `go test ./...` 与 `go vet ./...`；
- 认证、文档、管理员、Nginx 代理、Chat 未注册的运行时 API 回归（正常与 mixin-search 停机两种拓扑，均 `95 passed / 0 failed / 95 total`）；
- 影子索引收敛、对账与全量重建；
- 七类固定样本的 BM25 与 mixin-search 质量与延迟评估，四类正确性违规均为 0；
- 停机期间影子失败隔离、恢复后自动排空积压；
- 结束时输出 `P1.5_BUILD_TEST_DEPLOYMENT=PASS`、`P2.4_SHADOW_INDEX=PASS`、`P2.5_SHADOW_QUERY_EVALUATION=PASS` 并销毁一次性环境。

评估结论在两次运行中都是 `KEEP_BM25`（评测 embedding 仍为评估型 `local-hash-v1`），因此正式搜索读取方未变，B 线交接仍未开始。

## 未归档的中间运行

`deployments/test-results/` 中同一实施包可能还有更早的运行（例如 P3.2 在收敛读路径锁之前的验收）。那些运行对应已被取代的实现状态，不作为交付证据，按保留策略滚动清理；它们出现在这里没有意义。

## 维护

- 阶段 3 的后续实施包（P3.3 起）在完成验收后，把当次三个报告族复制到本目录并在上表追加一行；
- 本目录不记录中间过程，只记录被正式文档引用的最终验收集。
