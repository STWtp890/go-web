# P3.3 聊天控制快照容量画像（2026-09-19）

本文件是 P3.3 容量治理的**测量证据**：1k / 10k / 50k 消息规模下，聊天控制快照的大小、写路径的 CPU 与真实 compare-and-swap 延迟。它回答的是"上限该定在哪里"，不是"上限已经启用"——**当前 Compose 默认值为 0（不限），容量风险尚未受控**，启用属于 P3.6 前置步骤。

功能树最后变更提交：`5483b65`（本文档归档时为 `bc23a32` 之后）。测量工具：`apps/mixin-search/internal/chat/capacity_test.go`。

## 复跑方式

```powershell
# 纯本地：快照大小、clone+编码耗时、每次写入的分配量
cd apps/mixin-search
$env:GOPROXY='https://goproxy.cn,direct'; $env:GOCACHE="$env:TEMP\dsh-WaJ68W\go-build-cache"
$env:CHAT_CAPACITY_PROFILE='1'
go test ./internal/chat -run TestChatCapacityProfile -v

# 真实 CAS：需要可用的控制 PostgreSQL
docker compose -p p33cap -f docker-compose.yaml up -d --wait control-postgres
$env:CAPACITY_CAS_DSN='postgres://mixin_control:mixin_control@127.0.0.1:15433/mixin_control?sslmode=disable'
go test ./internal/chat -run TestChatCapacityCASLatency -v
docker compose -p p33cap -f docker-compose.yaml down -v
```

## 实测数据（2026-09-19，本机 Docker Desktop / WSL2）

| 消息数 | 快照大小 | 快照 clone+编码 | 真实 CAS 均值 | 真实 CAS 最差 |
| ---: | ---: | ---: | ---: | ---: |
| 1,000 | 764 KiB | 12.7 ms | 24.9 ms | 29.6 ms |
| 10,000 | 7.5 MiB | 103.0 ms | 254.9 ms | 263.7 ms |
| 50,000 | 37.4 MiB | 557.3 ms | 1353.2 ms | 1416.7 ms |

约 **766 字节/消息**，随规模线性增长（10 倍消息 ≈ 10 倍体积与耗时）。

## 画像的假设（读数字前必须知道）

- 每条消息索引一次，按每批 50 条提交；
- **账本按生产模型建模**：每个批次一条账本记录，记录内包含该批**每条**消息的响应状态。上一版画像把记录建模为"只含一个响应"，因此低估约 1.8 倍（上一版的 423 字节/消息与 20.2 MiB@50k 已作废）；
- 无撤回、无删除（撤回本身保留消息记录），pending 状态为空。

## 结论与建议

- 写路径的代价是**整份快照重写**：50k 时每次写入约 1.35 s、重写约 37 MiB，写入频率与 WAL 放大直接由快照大小决定；
- clone+编码约占其中 41%，换更快的数据库压不下去；
- **账本不是可忽略项**：它约等于把每条消息的响应再存一份，因此 ADR-015 的保留窗口直接决定快照大小；
- 两个自洽的候选档位（**待拍板**）：
  - **A（推荐）**：30,000 条消息 / 24 MiB 快照 / SLO p95 ≤ 1 s（30k 处约 22.5 MiB、CAS 约 0.8 s）；
  - **B**：50,000 条消息 / 48 MiB 快照 / SLO p95 ≤ 1.5 s（50k 实测最差 1.42 s）；
- 账本建议保留 7 天 + 100,000 条上限，与容量上限同批启用；
- 迁移触发（改成分区/行级 CAS 的判据，任一命中即启动独立 ADR）：命中任一硬限制，或持续观测到 p95 写入超过所选 SLO。

## 未覆盖

- 未测 100k 以上（结论只到 50k，更高规模属于外推）；
- 未测并发写入下的排队时间（此处测的是单次写入的串行代价）；
- 未测真实语料的分布差异（此处是等长消息与均匀批次）。
