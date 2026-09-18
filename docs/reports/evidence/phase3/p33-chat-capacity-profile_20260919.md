# P3.3 聊天控制快照容量画像（2026-09-19）

本文件是 P3.3 容量治理的**测量证据**：1k / 10k / 30k / 50k 消息规模下，聊天控制快照的大小、账本占比、写路径 CPU 与真实 compare-and-swap 延迟。它回答"上限该定在哪里"，**不代表上限已启用**——当前 Compose 默认值为 0（不限），容量风险尚未受控，启用属于 P3.6 前置步骤。

功能树最后变更提交：`a544a34` 之后的本片提交。测量工具：`apps/mixin-search/internal/chat/capacity_test.go`。

## 测量环境与建模假设（读数字前必看）

- 机器：`windows/amd64`、`ncpu=20`、`go1.26.8`，Docker Desktop（WSL2）容器内的 PostgreSQL 17；
- 合成消息：**内容为短标识串**（与真实聊天文本相比偏短），每条消息的 metadata 相同；10 万条消息分布在 100 个会话；
- 每批 50 条消息提交一次索引（另在账本预算测试中对比每批 5 条）；
- **账本按生产模型建模**：每个批次一条账本记录，记录内包含该批**每条**消息的响应状态（上一版把记录建模为"只含一个响应"，因此低估约 1.8 倍，已作废）；
- 无撤回、无删除（撤回本身保留消息记录），pending 状态为空；
- 真实语料的平均消息长度、metadata 数量、会话分布都会影响绝对值；本表用于**相对比较与量级判断**，不是真实语料的容量承诺。

## 复跑方式

```powershell
# 纯本地：快照大小、账本占比、clone+编码 p50/p95、每次写入的分配量
cd apps/mixin-search
$env:GOPROXY='https://goproxy.cn,direct'; $env:GOCACHE="$env:TEMP\dsh-WaJ68W\go-build-cache"
$env:CHAT_CAPACITY_PROFILE='1'
go test ./internal/chat -run 'TestChatCapacityProfile|TestChatCapacityLedgerCeilingBudget' -v

# 真实 CAS（20 次写入，报 p50/p95）：需要可用的控制 PostgreSQL
docker compose -p p33cap -f docker-compose.yaml up -d --wait control-postgres
$env:CAPACITY_CAS_DSN='postgres://mixin_control:mixin_control@127.0.0.1:15433/mixin_control?sslmode=disable'
go test ./internal/chat -run TestChatCapacityCASLatency -v
docker compose -p p33cap -f docker-compose.yaml down -v
```

## 快照画像（每批 50 条，合成短标识符）

| 消息数 | 快照大小 | 其中账本 | 账本占比 | clone+编码 p50 / p95 |
| ---: | ---: | ---: | ---: | ---: |
| 1,000 | 764 KiB | 343 KiB | 45% | 9.0 / 12.0 ms |
| 10,000 | 7.5 MiB | 3.4 MiB | 46% | 95.8 / 100.5 ms |
| 30,000 | 21.9 MiB | 10.1 MiB | 46% | 444.0 / 483.3 ms |
| 50,000 | 37.4 MiB | 17.3 MiB | 46% | 800.7 / 821.5 ms |

**账本稳定占快照的 45–48%**：它把每条消息的响应再存一份，因此"消息 + 账本"大致各占一半。这不是可忽略项，而是决定快照大小的两大部分之一。

## 两个被实测推翻的直觉（重要）

**消息长度不进入这份快照。** 控制快照只存 `content_sha256` 与 ID/修订号，不存正文；实测把内容从 20 字节放大到 4,000 字节，快照长度恒为 782,128 字节（`TestChatCapacityIsContentLengthIndependent`）。消息长度影响的是**向量集合**，那是另一个容量域。

**真实标识符与 metadata 会显著抬高快照。** 用 QQ 风格的会话/消息/发送者 ID、生产形态的 storage key（含域、会话、消息、操作号）与每消息 3 项 metadata 重跑（`TestChatCapacityRealisticShape`）：

| 消息数 | 合成形态 | 真实形态 | 差额 | 24 MiB |
| ---: | ---: | ---: | ---: | --- |
| 10,000 | 7.5 MiB | **9.1 MiB** | +1.8 MiB | 通过 |
| 30,000 | 21.9 MiB | **27.2 MiB** | +5.6 MiB | **超出** |

即：**在真实形态下，24 MiB 大约在 26,000 条消息处先触发**，而不是 30,000 条。这与拍板时确定的语义一致（有效容量 = min(条数, 字节)），但意味着 **metadata 是容量杠杆**：生产者给每条消息附带的 metadata 越多，可容纳的消息越少。建议把它写进 P3.6 的接入约定（例如每消息 metadata ≤ 少量键、总量 ≤ 约 64 字节），否则"30k 条"这个数字对 metadata 重的生产者不成立。

## 真实 CAS（整份快照单行 `UPDATE ... RETURNING`，20 次写入）

| 消息数 | 快照 | 均值 | p50 | p95 | 最差 |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1,000 | 764 KiB | 21.7 ms | 20.3 ms | 29.8 ms | 32.7 ms |
| 10,000 | 7.5 MiB | 202.7 ms | 190.0 ms | 230.3 ms | 230.9 ms |
| **30,000（直接实测，非内插）** | **21.9 MiB** | **721.0 ms** | **717.9 ms** | **750.9 ms** | 792.0 ms |
| 50,000 | 37.4 MiB | 1229.0 ms | 1227.0 ms | **1276.3 ms** | 1295.1 ms |

**30k 实测 CAS p95 = 750.9 ms，低于 1 秒目标**，因此不需要下调消息上限。（50k 的 p95 约 1.28 s，也低于原先假设的 1.58 s——那是上一轮 5 次采样的最差抖动；仍不选 B 档，因为缺少运行余量且真实形态更快触顶。）

## 两个上限的相互作用（评审提问的答案）

| 场景 | 快照 | 其中账本 | 24 MiB 预算 |
| --- | ---: | ---: | --- |
| 30k 消息 + 600 条 receipt（每批 50） | 21.9 MiB | 10.1 MiB | 通过 |
| 30k 消息 + 6,000 条 receipt（每批 5） | 23.0 MiB | 11.1 MiB | 勉强通过（余量 1 MiB） |
| **30k 消息 + 100,000 条 receipt（每条 1 个响应）** | **63.8 MiB** | **51.9 MiB（81%）** | **超出 2.7 倍** |

结论：**"100,000 条"这个条数上限与"24 MiB"这个字节上限互不相容**。账本大小由**保留的响应状态总数**驱动（每条 receipt 携带该批每条消息的状态 ≈ 350 字节/状态），而不是 receipt 条数本身；固定的 100k 条在每批 1 条的小批量下会带来约 52 MiB 的账本。

因此：

- **字节上限是约束性的**，条数上限只能作为粗粒度护栏；真正决定账本稳态大小的是**保留窗口**——在本次"一轮完整索引、每批 50 条"的画像模型下，7 天窗口约保留一份语料对应的 receipt；**实际占用取决于操作速率、批大小与操作类型**（消息被反复归档、授权变更或撤回时，同一批消息会在窗口内产生多份 receipt），并受 30,000 条账本上限与 24 MiB 总快照上限共同约束；
- 条数上限应当**按字节预算推导**，而不是独立取值：

  ```text
  maxEntries ≈ (快照预算 − 消息占用) / (每 receipt 平均状态数 × ~350 B + receipt 开销)
  ```

  以 A 档（30k 消息 / 24 MiB）为例：消息约占 11.2 MiB，留给账本约 12.8 MiB →
  每批 50 条时约 700 条 receipt；每批 5 条时约 7,000 条；每条 1 个响应时约 38,000 条。
  拍板取 **30,000 条**（约 20% 余量，不采用 100,000）；
- **保留窗口与容量上限同批启用**（已写入根 Compose）。

## 已启用的 A 档参数（2026-09-19 拍板）

```text
maxMessages         = 30,000
maxSnapshotBytes    = 24 MiB (25,165,824)
operationRetention  = 7 days (168h)
operationMaxEntries = 30,000
CAS p95 target      ≤ 1 second   （30k 直接实测 750.9 ms）
```

语义：`有效容量 = min(30,000 条消息, 24 MiB 快照)`、`账本保留 = min(7 天, 30,000 条记录)`。
四个参数已写入根 Compose（`MIXIN_SEARCH_CHAT_MAX_*` 可覆盖）；`rag-server` 的运行期默认仍为 0（不限），
保护由部署配置给出——根 Compose 是本项目唯一的生产基线。

**注**：30k 是数量天花板，不保证所有消息长度分布都能装到 30k；真实标识符形态下 24 MiB 约在 26k 处先触发
（见上文"两个被实测推翻的直觉"），metadata 重的生产者会更早触顶。

## 启用验证（2026-09-19）

参数写入根 Compose 后，容器启动日志确认生效值：

```
chat capacity (effective): max_messages=30000 max_snapshot_bytes=25165824 operation_retention=168h0m0s operation_max_entries=30000 (0 means unlimited)
```

同一次验证跑通了三项要求：`TestCapacityGuardAgainstPostgres` 的三个子测试（消息上限、快照上限、账本清理）、
文档控制存储的 PostgreSQL 集成、以及整栈门禁 `deployments/verify.ps1`（退出码 0，四个隔离步骤与
P1.5/P2.4/P2.5 全 PASS，同次运行的报告族见本目录 `full-api-p15_20260919_042449` 等）。
"超限不产生部分提交"由 `TestSnapshotCeilingRefusesEveryMutationNotJustIndexing` 断言（generation、
会话/消息/pending 状态与向量计数在被拒后全部不变）。

## 未覆盖

- 未测 100k 消息以上（结论只到 50k；更高规模属于外推）；
- 未测并发写入下的排队时间（此处是单次写入的串行代价）；
- 真实语料的 metadata 规模未采样：本表用了每消息 3 项、约 60 字节的假设值，metadata 更重的生产者应重新测量；
- 单次测量样本 20 个（p95 由最近秩法给出），同一机器上的重复运行会有抖动。
