# Stage A / qq-search：`GetQQRecordState` 渠道范围校验（证据）

> 日期：2026-09-30
> 负责人：Subagent D（teammate `svc-qq`），共享任务 `task-3`
> 范围：`apps/qq-search/`，契约固定方为主 Agent（`packages/proto/qqsearch/v1` 与 `packages/gen/qqsearch/v1` 已由主 Agent 修改并重新生成，本任务未改动）
> 环境：PostgreSQL `127.0.0.1:15432/gin_demo`（真实实例，`qq_search_writer` 账号）；所有 Go 命令前设置 `$env:GOCACHE = Join-Path $env:TEMP 'gb-goweb'`

## 1. 缺陷与修复

`docs/planning/CURRENT_IMPLEMENTATION_PLAN.md` §0.8 记录：`GetQQRecordState` 只按 scope（`qq-searcher`）授权，请求里没有渠道范围字段，因此持有该 scope 的调用方可以探测其授权会话之外的记录是否存在。

修复后的调用链（全部为实际实现）：

```text
x-resource-capability ──serviceauth 校验──> capability.Claims
  → CheckChannelInclusion（请求中的标识必须在授权范围内，越界整体拒绝）
  → 授权范围（bot_ids / conversation_ids / external_group_ids）传入业务层
  → resolveScope（请求只能缩小授权范围）
  → 空范围 ⇒ 直接 exists=false，不访问数据库
  → 消息/文件各自在 messageStateStatement / fileStateStatement 上追加
     bot_id / conversation_id / external_group_id 谓词
  → 查不到（含范围外）⇒ exists=false 且 state 缺省，与"记录不存在"完全同形
```

关键性质：

- **越界整体拒绝**：请求 `scope` 中出现任何未授予标识 → `PERMISSION_DENIED`，不静默裁剪（复用 `CheckChannelInclusion` 与 `resolveScope`）。
- **空授权范围 = 没有会话**：`scope.empty()` 时直接返回 `exists=false`，绝不退化为全表读取；两个语句构造器另有 `AND FALSE` 兜底（若上游不变量被破坏也匹配不到任何行）。
- **过滤在 SQL 里**：范围外行根本不会被读出来，不存在"先读后过滤"的时间差；消息与文件各用自己表的列、各自的语句，不共用。
- **撤回仍在范围内可查**：状态查询不按 `status` 过滤，范围内 `RECALLED` 记录返回 `exists=true`、`status=RECALLED`、`record_revision` 为撤回修订号。
- 查询路径没有 py-agent 客户端（结构断言见 `internal/architecture/source_client_test.go`），本修复未引入任何出站调用。

## 2. 修改文件

| 文件 | 改动 |
| --- | --- |
| `apps/qq-search/internal/interfaces/grpcapi/handlers.go` | `GetQQRecordState` 先 `api.channelScope(ctx, request.GetScope())`，把已校验 capability 的授权范围传给业务层 |
| `apps/qq-search/internal/interfaces/grpcapi/api.go` | `Index` 接口的 `GetRecordState` 增加 `scope *qqsearchv1.QQChannelScope` 参数 |
| `apps/qq-search/internal/application/handlers.go` | `GetRecordState` 增加 `granted` 参数 |
| `apps/qq-search/internal/application/search.go` | `getRecordState` 增加范围解析/空范围短路；新增 `recordStateInScope` 分派 |
| `apps/qq-search/internal/application/messages.go` | 新增消息状态的范围查询构造器与 `loadMessageStateInScope`；扫描逻辑抽成 `scanMessageState` 单点 |
| `apps/qq-search/internal/application/files.go` | 文件模型镜像同一改动（自己的表、自己的列） |
| `apps/qq-search/internal/application/rebuild.go` | 修复重建丢失更新竞态（见 §5），不改变重建语义 |
| `apps/qq-search/internal/testkit/pgserial.go` | 新增：集成测试的跨包会话级 advisory lock（见 §6），仅测试支持，无生产路径 |
| `apps/qq-search/internal/application/integration_test.go` | 仅更新 5 处 `GetRecordState` 调用点传入已授予范围；接入测试串行锁；断言语义不变 |
| `apps/qq-search/internal/interfaces/grpcapi/transport_integration_test.go` | 接入测试串行锁（断言不变） |
| `apps/qq-search/internal/application/model_test.go` | 同上 2 处调用点 |
| `apps/qq-search/internal/interfaces/grpcapi/inclusion_test.go` | 方法策略表新增"capability 查询方法"集合，`GetQQRecordState` 必须属于其中 |
| `apps/qq-search/internal/application/recordstate_integration_test.go` | 新增 3 个集成测试（范围外/空范围/范围内撤回） |
| `apps/qq-search/internal/interfaces/grpcapi/recordstate_transport_integration_test.go` | 新增 1 个传输层集成测试（能力决定答案） |
| `apps/qq-search/internal/application/rebuild_fence_integration_test.go` | 新增 1 个重建栅栏回归测试 |

未改动（按要求）：`packages/proto`、`packages/gen`、`packages/serviceauth`、`go.work`、`docker-compose.yaml`、`deployments/`、`docs/contracts/`、`docs/adr/`、`apps/qq-search/schema/`。

## 3. 验收项与测试映射

| 验收项 | 测试 | 结果 |
| --- | --- | --- |
| 跨 Bot 查询不暴露记录 | `TestIntegrationGetRecordStateIsBlindOutsideTheGrantedChannelScope`（另一 Bot 的 `record_id` → `exists=false`）、`TestIntegrationTransportRecordStateUsesTheChannelCapability`（同一记录在不同 capability 下分别为可见/不可见） | PASS |
| 跨会话查询不暴露记录 | 同上（另一群记录 → `exists=false`；请求显式指名越界会话 → `PERMISSION_DENIED`） | PASS |
| 范围外与不存在返回一致 | 上述测试断言"越界响应"与"从未存在记录的响应"序列化后逐字节相同，且 `state` 缺省 | PASS |
| 空授权范围不返回记录 | `TestIntegrationGetRecordStateWithAnEmptyGrantFindsNothing`（消息与文件均 `exists=false`，且数据库里确有该行）、传输层空 capability 用例 | PASS |
| 消息与文件都执行授权检查 | 两个模型各自的越界/空范围/越界请求用例 | PASS |
| 范围内撤回状态正确 | `TestIntegrationGetRecordStateReportsAnInScopeRecall`（消息与文件 `exists=true` / `RECALLED` / revision=2）、传输层撤回用例 | PASS |
| 原检索、撤回、来源隔离语义不回归 | 原有 12 个 `TestIntegration*` 全部 PASS，断言未放宽（仅调用点补传已授予范围） | PASS |
| 方法策略表覆盖新行为 | `TestMethodScopesCoverEveryRPCExactlyOnce`（`qq-searcher` 的三个查询 RPC 必须全部登记为需要 capability 的方法） | PASS |

## 4. 实际命令、退出码与关键输出

工作目录 `apps/qq-search`，前置 `$env:GOCACHE = Join-Path $env:TEMP 'gb-goweb'`。

```text
=== go build ./... ===
BUILD_EXIT=0                      （无输出）

=== go vet ./... ===
VET_EXIT=0                        （无输出）

=== gofmt -l . ===
GOFMT_EXIT=0
GOFMT_OUTPUT_LINES=0              （0 行输出）

=== go test ./... -count=1 ===
TEST_EXIT=0
ok  	qq-search/internal/application     1.130s
ok  	qq-search/internal/architecture    0.044s
ok  	qq-search/internal/ingress/pyagent 0.024s
ok  	qq-search/internal/interfaces/grpcapi 0.256s
（cmd/qq-search、cmd/qq-search-healthcheck、internal/app、internal/config、internal/infrastructure/postgres、internal/testkit 无测试文件）

=== go test ./... -count=1 -run Integration -v ===
INTEGRATION_EXIT=0
PASS_COUNT=17   FAIL_COUNT=0   SKIP_COUNT=0
```

`-run Integration -v` 的 17 个用例（12 原有 + 5 新增，全部连真实 PostgreSQL，无 skip）：

```text
--- PASS: TestIntegrationSaveMessageIsSearchableInsideItsGrantedConversation (0.05s)
--- PASS: TestIntegrationMessageUpdateReplacesTheSearchableContent (0.05s)
--- PASS: TestIntegrationRecallKeepsTheRowAndHidesItFromSearch (0.05s)
--- PASS: TestIntegrationDuplicateEventAppliesOnce (0.04s)
--- PASS: TestIntegrationOutOfOrderEventsAreFenced (0.10s)
--- PASS: TestIntegrationFilesFollowTheSameRulesInTheirOwnTable (0.09s)
--- PASS: TestIntegrationMessageAndFileCorporaStayIsolated (0.14s)
--- PASS: TestIntegrationChannelScopeLimitsResults (0.05s)
--- PASS: TestIntegrationRebuildRestoresTheDerivedIndexFromLocalData (0.08s)
--- PASS: TestIntegrationWritesOnlyTheQQSearchSchema (0.03s)
--- PASS: TestIntegrationReplaysThePyAgentInboundTranscript (0.05s)
--- PASS: TestIntegrationRebuildCannotResurrectARecordRecalledWhileItRuns (0.08s)   [新增]
--- PASS: TestIntegrationGetRecordStateIsBlindOutsideTheGrantedChannelScope (0.06s)  [新增]
--- PASS: TestIntegrationGetRecordStateWithAnEmptyGrantFindsNothing (0.04s)          [新增]
--- PASS: TestIntegrationGetRecordStateReportsAnInScopeRecall (0.07s)                [新增]
--- PASS: TestIntegrationTransportRecordStateUsesTheChannelCapability (0.25s)        [新增]
--- PASS: TestIntegrationTransportBoundary (0.20s)
```

集成测试连接真实实例：DSN 默认 `postgres://qq_search_writer:qq_search@127.0.0.1:15432/gin_demo?sslmode=disable`（可用 `QQ_SEARCH_TEST_DSN` 覆盖），连不上即 `t.Fatalf`，无 `t.Skip`。

## 5. 附带发现并修复的既有竞态（重建丢失更新）

**现象（修复前）**：并行跑 `go test ./internal/application/ ./internal/interfaces/grpcapi/ -count=1 -run Integration` 时，未改动的 `TestIntegrationRecallKeepsTheRowAndHidesItFromSearch` 会以

```text
integration_test.go:388: a stale upsert resurrected a recalled record: hits=[qqsit-...-msg-3]
```

失败；复现率 **5/8 次运行**（连续 8 次并行运行记录）。单独运行 `go test ./internal/application/ -count=1 -run Integration` 始终通过。

**根因**：传输层测试 `TestIntegrationTransportBoundary` 会调用 `RebuildIndex(confirm=true)`，其 `rebuild_messages`/`rebuild_files` 语句在**语句开始时**取 `qq_applied_events` 快照并全表重写。若某条撤回在语句取快照之后、且在该语句等待行锁期间提交，语句持有的 `latest` 仍是旧修订号，于是把"已撤回"的新行写回 `indexed` 旧修订 —— 复活了撤回记录（同一条语句也被并行的应用层测试观察到，所以搜索会命中它）。这与重建自身的承诺"A recall at the newest revision always wins, so a rebuild can never resurrect a recalled message"冲突，也直接破坏本任务的"范围内撤回状态仍要能查到"。

**修复**：两个重建 `UPDATE` 增加修订号栅栏 `AND m.record_revision <= latest.record_revision`（文件模型为 `f.record_revision`）。当行内修订号比快照更新时跳过该行，交给已经推进它的写入者；行落后于账本（受损行）时仍照常修复。

**负向对照（去掉消息侧栅栏后运行新测试）**：

```text
--- FAIL: TestIntegrationRebuildCannotResurrectARecordRecalledWhileItRuns
    rebuild_fence_integration_test.go:121: row after the concurrent rebuild = indexed/1,
    want recalled/2: the rebuild rewound a recall that committed while it was running
```

新增测试 `TestIntegrationRebuildCannotResurrectARecordRecalledWhileItRuns` 用两条真实连接确定性复现该交错：连接 A 持有未提交的撤回（行已更新、事务未提交），连接 B 执行真实重建语句并阻塞在该行锁上（用 `pg_stat_activity.wait_event_type='Lock'` 轮询证明它确实阻塞），随后 A 提交、B 放行；断言最终行为 `recalled`/修订号 2，且 `GetQQRecordState` 在授权范围内返回 `RECALLED`。消息与文件两个模型各验证一次。

**修复后回归**：同一并行命令连续 8 次运行 **8/8 通过**（修复前 5/8 失败）。

## 6. 附带发现并修复的测试并发缺陷（并行包共享全局计数）

**现象（修复前）**：修好重建竞态后，`go test ./... -count=1` 仍会偶发失败，但换成**未改动的** `TestIntegrationDuplicateEventAppliesOnce`：

```text
integration_test.go:458: the applied-event count did not grow; before=19 after=19
integration_test.go:462: the indexed-message count did not grow; before=1 after=1
```

实测：连续 12 次全量运行失败 3 次（run 10–12 连续失败，且数值都是 `19/19`）；随后连续 6 次失败 1 次。

**根因（测试运行层面，不是服务缺陷）**：`internal/application` 与 `internal/interfaces/grpcapi` 是同一 `go test ./...` 进程中的两个并行包，都对同一个 `qq_search` schema 索引、重建并**按各自前缀删除**自己的行，而 `Status` 的 `events_applied` / `indexed_messages` 是 schema 全局计数。一个包在 `t.Cleanup` 里删除自己行的时间点，可能正好落在另一个包某个测试的 `before` / `after` 两次读取之间，于是"计数没有增长"。该测试的注释原本只假设"其他包的行不可归因"，但没有考虑其他包的删除也会改变全局计数。

**修复**：新增测试支持包 `internal/testkit`（`pgserial.go`），两个集成套件在 `openTestDatabase` / `startServer` 里用会话级 advisory lock（`pg_try_advisory_lock`，90s 上限）串行化：一个数据库上同时只跑一个 qq-search 集成测试；释放函数注册在行清理之前，使清理也在临界区内。这是测试运行策略，不是服务行为：`cmd/qq-search` 不引用该包，服务的并发语义未改。

**修复后回归**：`go test ./... -count=1` 连续 **12/12 通过**（修复前 3/12 失败）；整套 12 次运行合计 29.2s（单次约 2.4s）。

## 7. 未做项、跳过项与限制

- 无跳过测试：`-run Integration -v` 的 `SKIP_COUNT=0`；未使用 `t.Skip`。
- 未申请沙箱升级：全部源码改动经 read/edit/write 文件工具完成，命令只在数据库中写数据，未向工作区写文件。
- 未改契约与生成物：`GetQQRecordStateRequest.scope` 字段由主 Agent 提供，本任务只消费。
- `GetQQRecordState` 之外的 `SearchQQMessages`/`SearchQQFiles` 语义未改（既有范围规则与失败关闭行为保持）。
- 重建栅栏只解决"重建覆盖更新行"；重建仍会锁全表（并发写入短暂阻塞），这是既有实现特性，未在本任务改动。
- §6 的串行锁只覆盖使用 `internal/testkit` 的集成测试。与集成测试同时运行的**非测试**写入者（例如 `deployments/verify-source-owned-services.ps1` 启动的真实服务）不参与该锁，仍可能改变 schema 全局计数；建议该脚本不要在集成测试运行期间执行。
- 其他已知限制（容量、账本保留、record_id 唯一性义务、共享边界密钥等）见 `stage-a-qq-search-pyagent-requirements.md` §9 与 `CURRENT_IMPLEMENTATION_PLAN.md` §0.8。

## 8. 关联证据

- py-agent 接口事实清单：`docs/reports/evidence/phase4/stage-a-qq-search-pyagent-requirements.md`
- 缺陷登记与遗留：`docs/planning/CURRENT_IMPLEMENTATION_PLAN.md` §0.8（本修复后该条第一项已不成立，需主 Agent 在汇总时更新）
