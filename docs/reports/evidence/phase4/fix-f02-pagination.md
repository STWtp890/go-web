# F02 修复证据：候选上限与检索分页总数不一致（document-search）

- 任务：`task-8`（F02 [P1]），负责人 `fix-search`
- 写入范围：`apps/document-search/`、本文件
- 环境：PostgreSQL `127.0.0.1:15432/gin_demo`、Qdrant gRPC `127.0.0.1:16334`（均为运行中的容器）
- 状态：**完成**。`go build` / `go vet` / `gofmt -l` / `go test ./... -count=1` / `-run Integration -v`（0 fail、0 skip）/ `-run Integration` 连跑 3 次全部通过，实测数据见第 5 节

## 1. 根因

旧实现有三处口径不一致：

1. `retrieveBounds.KeywordCandidates`（`vector.max_keyword_candidates`）把关键词排名**截断**为前 K 条，`fuseRanks` 只能在 K 条候选上排序；
2. `total = 关键词完整匹配数 + 向量臂额外召回数`，即 total 描述的是**完整答案集**，而能翻页的只有候选列表（≤ K + 召回）；
3. `truncated` 在候选被截断时置真，语义混用。

验收复现：3 篇匹配、`max_keyword_candidates=2`、`vector.enabled=false`、第 3 页每页 1 篇 → `total=3 hits=0 truncated=true`（第 3 篇永远读不到）。

## 2. 行为/契约变化（`SearchDocumentsResponse.total` 字段定义不变）

| 语义 | 旧行为 | 新行为 |
| --- | --- | --- |
| `total` | 完整匹配数 + 向量额外召回，但可能读不到 | **可翻页读到的结果数**：范围内关键词匹配数 + 向量臂额外召回且通过同一过滤的文档数（两臂同命中只计一次）。对任意 `N ≤ ceil(total/page_size)`，第 N 页必定非空 |
| `truncated` | 本页之后还有结果 **或** 候选被截断 | 仅 `offset+len(hits) < total`（本页之后还有结果）；`page_size` 超过 `index.max_top_k` 被下调时仍为真（阶段 A 行为） |
| `vector.max_keyword_candidates` | 关键词候选上限（同时是分页上限） | **RRF 融合窗口宽度**：只有前 K 条关键词匹配参与窗口内 RRF；窗口之外的其余匹配按关键词原始顺序（`keyword_score` 降序、`document_id` 升序）排在窗口之后，仍可完整分页 |
| `SearchHit.score` | 所有命中都是 RRF 融合分 | 窗口内命中是 RRF 融合分；窗口外命中记 `0`（无 RRF 证据），保证"融合分降序"与返回顺序一致 |
| `vector.enabled=false` | 分页受 K 限制 | 完整关键词分页：`total` = 匹配数，每个合法页都可读 |

完整答案集 = `fused window` ++ `keyword tail`，两段互斥且并集恰为 `total` 条：

- 窗口 = 关键词 top-K ∪ 向量臂召回族（按文档去重，RRF 降序、`document_id` 破平）；
- 尾部 = 关键词匹配中不在窗口内的部分，按关键词顺序；
- 计数不变量：`|window| + |tail| = keywordTotal + |向量额外召回| = total`。

保持不变：去重、排序确定性、范围收窄即实际过滤、越界整体拒绝、`owned_by_subject_only`、活动版本/`active` 生命周期/`published` 发布过滤、命中与总数同条件、`page`/`page_size` 语义、命中展示字段、向量召回族（点名/获授权的空间与文档，公开兜底不作为召回族但参与过滤）。

## 3. 修改文件

| 文件 | 变更 |
| --- | --- |
| `apps/document-search/internal/application/hybrid.go` | `retrieveBounds.KeywordCandidates` → `KeywordWindow`；新增 `mergeWindow`（两臂按文档去重、统计"仅向量召回"数）、`pageWindowFor`（纯函数，把页映射到"窗口 + 尾部"）、`pageHits`、`newSearchHit`、`keywordTail`；`vectorCandidates` 现在返回**全部**通过范围复检的召回行的展示行（不再只返回关键词未命中者）；`Truncated` 仅表示"本页之后还有结果" |
| `apps/document-search/internal/application/search.go` | `searchKeywordCandidatesSQL` → `searchKeywordWindowSQL`（新语义注释）；新增 `searchKeywordTailSQL`（`NOT (document_id = ANY(window))` + `LIMIT/OFFSET`）；参数契约注释更新；`countMatches` 注释更新 |
| `apps/document-search/internal/application/search_pagination_test.go` | 新增纯函数单测：`pageWindowFor` 页映射（含验收分页边界）、"页不丢不重"性质测试、`mergeWindow`+`fuseRanks` 的答案计数不变量性质测试 |
| `apps/document-search/internal/application/search_pagination_integration_test.go` | 新增 F02 回归集成测试（真实 PostgreSQL + 真实 Qdrant，无 skip） |
| `apps/document-search/internal/application/zz_review_probe_compat_test.go` | 验收方探针**原样保留**（仅 gofmt），函数名 `TestIntegrationReviewProbePastCandidateCap` |
| `apps/document-search/internal/config/config.go` | `max_keyword_candidates` 注释改为"RRF 融合窗口宽度"（键名、默认值 1000、环境变量名保持不变，兼容既有配置与验收探针） |
| `apps/document-search/configs/config.yaml`、`configs/config.docker.yaml` | 同上注释更新（键名与取值不变） |
| `apps/document-search/README.md` | 查询与融合、配置两节改写：窗口语义、`total`/`truncated` 定义、窗口外 `score=0` |

未改动：`packages/proto`、`packages/gen`、`packages/serviceauth`、`go.work`、`docker-compose.yaml`、`deployments/`、`docs/contracts/`、`docs/adr/`。

## 4. 验收项 → 回归用例映射

| 验收要求 | 用例 |
| --- | --- |
| 验收方复现：3 篇匹配、窗口 2、关闭向量、第 3 页每页 1 篇 → 命中 1 篇且 `total=3` | `TestIntegrationReviewProbePastCandidateCap`（探针原样保留）、`TestIntegrationSearchPagePastFusionWindowIsReadable`（并验证各页与越界页） |
| 匹配数低于/等于/超过窗口时的分页 | `TestIntegrationSearchWindowSizeDoesNotBoundReadability`（窗口 1/2/3/8，匹配数 3） |
| 改变 `page_size` 后分页定义一致 | `TestIntegrationSearchPaginationIsTheSameUnderAnyPageSize`（page_size 1/2/3/5） |
| 窗口对排名的实际影响（向量臂能改变窗口内顺序） | `TestIntegrationSearchVectorArmReordersInsideTheFusionWindow`（关键词顺序 [标题命中, 内容命中] → 融合顺序 [被召回文档, 标题命中]，`total` 不变；recall=1，被召回文档与查询的余弦 0.65 vs 标题文档最近块 0.03） |
| 向量与关键词重复命中按文档去重 | `TestIntegrationSearchHybridPaginationDeduplicatesBothArms`（3 篇同命中、窗口 2、recall 10） |
| 混合检索下尾部仍可分页 | `TestIntegrationSearchHybridTailIsPageable`（4 篇、窗口 2、recall 1） |
| 所有者/权限/生命周期过滤不回退；窗口不绕过过滤 | `TestIntegrationSearchWindowedPaginationKeepsScopeAndLifecycle`（窗口 1：授权信封 5 篇、个人检索 3 篇、收窄到 B 1 篇、个人+收窄 0 篇；草稿与已删除文档永不出现） |
| 页映射算术（含验收边界） | `TestPageWindowForSlicesTheAnswerAfterTheFusedWindow`、`TestPageWindowForMatchesTheConcatenatedAnswer`、`TestPageWindowForNeverSkipsOrRepeatsAResult` |
| 答案计数不变量（`len(window) + len(tail) = total`，两臂去重） | `TestFusionWindowCountsEveryAnswerOnce`（组合枚举 keywordTotal 0–6 × 窗口 1–6 × 仅向量召回 0–2 × 两臂重叠 0–2） |
| 验收探针仍可编译（配置字段未改名） | 把验收方 `search_probe_test.go` 原文放入包内 → `go vet ./internal/application/` = 0；该用例随后整理为 `TestIntegrationReviewProbePastCandidateCap` 保留在 `zz_review_probe_compat_test.go` |

## 5. 实际命令与结果

工作目录 `apps/document-search`，`$env:GOCACHE = Join-Path $env:TEMP 'gb-goweb'`；PostgreSQL `127.0.0.1:15432/gin_demo`（容器 healthy）、Qdrant gRPC `127.0.0.1:16334`（容器 healthy）。

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `go build ./...` | 0 | 通过 |
| `go vet ./...` | 0 | 通过 |
| `gofmt -l .` | 0 | 无输出 |
| `go test ./... -count=1` | 0 | 全绿（最终一轮：`app` 0.091s、`application` 17.689s、`architecture` 0.048s、`config` 0.018s、`dbtest` 0.062s、`grpcapi` 0.118s） |
| `go test ./... -count=1 -run Integration -v` | 0 | `--- PASS` 42 条、`--- FAIL` 0 条、`--- SKIP` 0 条（脚本统计 `PASS=42 FAIL=0 SKIP=0`） |
| `-run Integration` 连跑 3 次 | 0 / 0 / 0 | run1 `application` 18.843s、run2 17.882s、run3 16.507s，全部 `ok`，无 FAIL |
| `go test ./... -count=1 -skip Integration`（环境未恢复时的纯逻辑基线） | 0 | 全部纯逻辑测试通过 |
| 验收方探针原文入包 → `go vet ./internal/application/` | 0 | 配置字段未改名，探针可编译；该用例已整理为 `TestIntegrationReviewProbePastCandidateCap` 保留 |

关键输出（验收方复现用例，`-v`）：

```text
=== RUN   TestIntegrationReviewProbePastCandidateCap
    zz_review_probe_compat_test.go:28: page 3 with 3 matches and candidate cap 2: total=3 hits=1 truncated=false
--- PASS: TestIntegrationReviewProbePastCandidateCap (0.05s)
```

即：修复前 `total=3 hits=0 truncated=true` → 修复后 `total=3 hits=1 truncated=false`，第 3 篇可读且总数不变。

`-run Integration` 输出（节选）：

```text
--- PASS: TestIntegrationSearchPagePastFusionWindowIsReadable (0.06s)
--- PASS: TestIntegrationSearchWindowSizeDoesNotBoundReadability (0.08s)
--- PASS: TestIntegrationSearchPaginationIsTheSameUnderAnyPageSize (0.08s)
--- PASS: TestIntegrationSearchVectorArmReordersInsideTheFusionWindow (0.05s)
--- PASS: TestIntegrationSearchHybridPaginationDeduplicatesBothArms (0.08s)
--- PASS: TestIntegrationSearchHybridTailIsPageable (0.07s)
--- PASS: TestIntegrationSearchWindowedPaginationKeepsScopeAndLifecycle (0.11s)
--- PASS: TestIntegrationReviewProbePastCandidateCap (0.05s)
ok  	document-search/internal/application	17.127s
```

**越界页不变量（Web 侧依赖，实测）**：`TestIntegrationSearchPagePastFusionWindowIsReadable` 断言 `page=4, page_size=1` 且 `total=3` 时返回 **0 命中、`total=3`、`truncated=false`**；`pageThrough` 辅助断言在每个分页走查（关键词单路与混合两路都覆盖）里对终止空页同样要求 `truncated=false`，并断言"只要还有结果，页就不为空且是整页"。

## 6. 已完成 / 遗留说明

- 集成测试已全部执行：`go test ./... -count=1`、`-run Integration -v`（0 fail / 0 skip）、`-run Integration` 连跑 3 次全部通过（见第 5 节）。
- 契约文件未改：`total`/`truncated` 的对外定义不变，因此不需要修改 `docs/contracts/`；窗口外命中 `score=0` 属于实现层补充，已在 README 与本文件说明。
- 历史文档措辞：`docs/reports/evidence/phase4/stage-b-vector-flow.md:57` 仍写着旧的「关键字候选超过 `vector.max_keyword_candidates` 或本页之后仍有答案时 `truncated=true`」。该文件不在本任务写入范围内（阶段 B 历史证据），建议主 Agent 决定是否加注"该措辞已被 F02 取代"。
- `vector.max_keyword_candidates` 键名保持不变（只改注释）：验收探针 `fixture.service.cfg.Vector.MaxKeywordCandidates = 2`、既有 `configs/*.yaml`、环境变量名 `DOCUMENT_SEARCH_VECTOR_MAX_KEYWORD_CANDIDATES` 都不受影响。
