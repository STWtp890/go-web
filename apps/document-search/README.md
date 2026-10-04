# document-search

正式文档检索服务（独立 Go module、独立进程、独立 schema `document_search`、独立 Qdrant
alias `go_web_document_v1` / storage domain `document-search:documents:v1`）。

它只消费 `document-service` 的正式文档变更事件，维护**关键词索引（PostgreSQL tsvector + GIN）
与向量集合（Qdrant）**，并在已离线校验的资源范围 capability 内回答查询。查询与重建路径不回调
事实源（`go-web`、`document-service`、`py-agent`），由
`internal/architecture` 的结构断言固定。

## 能力与其唯一负责人

| 能力 | 唯一负责人 | 实现落点 |
| --- | --- | --- |
| 正式文档事件消费与幂等/乱序栅栏 | **document-search** | `internal/application/{consumer,events}.go` |
| 正式文档关键词索引与查询 | **document-search** | `internal/application/{search,hybrid}.go`、`schema/schema_init.sql` |
| 正式文档向量集合、embedding、collection/alias 生命周期 | **document-search** | `internal/application/{embedding,vectorindex,vector_write}.go`、`internal/infrastructure/qdrant/store.go` |
| 关键词 + 向量混合检索（RRF 融合） | **document-search** | `internal/application/hybrid.go` |
| 索引重建（关键词 + 向量，本地事件重放）与过期 collection 清理 | **document-search** | `internal/application/rebuild.go` |
| 固定数据集评测 | **document-search**（数据集 `deployments/evaluation/document-search-v1.json` 只读引用） | `internal/application/evaluation_integration_test.go` |

`apps/mixin-search` 的正式文档检索实现是本次接管前的旧基线，不再是任何一项能力的负责人。

## 向量流程

### embedding profile

`local-hash-v1`：与旧本地哈希实现同一算法（小写 unigram + 相邻 bigram、FNV-1a 64、
64 维有符号桶、L2 归一化），纯函数、无外部模型。同一文本永远得到同一向量，因此重建可以在
不联系事实源的前提下逐字节复现集合。profile 名写入配置（`vector.profile`）与索引记录
（`document_search.index_generations.vector_profile`、`document_index.vector_profile`）。

`internal/application/embedding_test.go` 用**旧实现的原始代码**跑出的黄金向量固定该 profile：
改动分词、哈希、维度或归一化顺序都会让它失败。

### collection 身份来自索引记录

服务不硬编码第二套命名。启动时读取 `document_search.index_generations` 的活动行，得到
`collection_alias`、`storage_domain`、`vector_profile`、`vector_dimensions`；配置与记录不一致
即启动失败。物理 collection 名唯一由 `<collection_alias>_<generation>` 派生
（`qdrant.GenerationCollection`）。alias 在 collection 缺失时被创建并指向该物理 collection。

生命周期入口（`internal/infrastructure/qdrant/store.go`）：`EnsureGeneration`（创建/校验）、
`PrepareGeneration`（预建下一代）、`SwitchAlias`（原子切换 + 失败补偿）、`DropCollection`、
`PruneGenerations`（删除无 `index_generations` 行引用的物理 collection）、`DropAlias`。
被 alias 服务的 collection 永不被删除；切换目标必须属于同一 alias 前缀。

### 写入路径

`IndexDocumentEvent`（upsert）在同一份事件流程内写 SQL 投影与向量：先过 revision 栅栏，
被栅栏拒绝的事件不触碰集合；随后按与关键词索引**完全相同的分块**写入 `chunk_count` 个点，
chunk 0 另外携带标题与摘要。集合写发生在 SQL 事务提交之前：向量失败 → SQL 回滚、
事件保持待处理、消费者重投；向量成功而提交失败 → 重投覆盖同样的确定性 point id。
`delete`/tombstone 事件删除该文档的全部点。

因此 `document_index.chunk_count` 等于该文档在集合中的点数（测试
`TestIntegrationVectorUpsertWritesAndReplacesPoints` 断言）。

### 重建

`RebuildIndex` 重放本地已应用事件账本（payload 逐字保存），同时重建 SQL 投影与向量集合，
不访问事实源；完成后删除不再被 `index_generations` 引用的物理 collection。重建可反复执行：
确定性 point id 使同一文档被覆盖而不是重复。

### 查询与融合

`SearchDocuments` 由两条臂组成，融合规则为 RRF（`vector.rrf_k`，默认 60），融合只发生在一个
有界**窗口**内：

1. **关键词臂**：`searchCountSQL` 给出范围内真实匹配数；`searchKeywordWindowSQL` 给出按
   `ts_rank_cd` + 标题加成排序、以 `document_id` 破平的前 `vector.max_keyword_candidates`
   条匹配，它们构成融合窗口的关键词侧。
2. **向量臂**：用同一段文本的 embedding 在集合中召回 `vector.recall` 个点（按文档折叠取最高分），
   再用 `selectScopedDocumentsSQL` 把召回文档 id 读回**同一个授权/生命周期过滤条件**下；
   通过过滤的召回与关键词窗口一起进入 RRF。

窗口内按 RRF 分数降序、`document_id` 升序排序，`SearchHit.score` 就是该融合分数；**窗口之外**的
其余关键词匹配由 `searchKeywordTailSQL` 按关键词原始顺序（`keyword_score` 降序、`document_id`
升序）读出并排在窗口之后，`score` 为 0——它们没有 RRF 证据，因此不会高于任何窗口内分数。于是整条
答案集是一个确定的完整序列：**窗口决定向量臂能影响多深的排名，不决定可读范围**。

- **`total` = 调用方真正可以翻页读到的结果数** = 范围内关键词匹配数 + 向量臂额外召回且通过同一
  过滤的文档数（两臂都命中的文档只计一次）。对任意 `N ≤ ceil(total/page_size)`，第 N 页必定非空；
  `page`/`page_size` 各页互不重叠、不遗漏。
- **`truncated` = 本页之后还有结果**（`offset+len(hits) < total`），与窗口是否截断无关。
- `page_size` 超过 `index.max_top_k` 被下调时仍报 `truncated=true`（阶段 A 行为不变）。
- `vector.enabled=false` 时窗口只剩关键词侧，分页覆盖全部匹配：`total` = 匹配数，每个合法页都可读。

向量臂召回的是相似度最高的 `vector.recall` 个点：当获授范围内集合点数少于该上限时（例如固定
评测集的 7 篇文档），整个范围都会被召回，此时 `total` 等于该范围内的文档数，有意义的输出是融合
顺序（关键词命中排在前）。相似度完全相同的候选按 `document_id` 升序破平，因此对固定的数据库
状态，同一查询的顺序完全确定（同一进程内重复查询、以及重建前后，顺序逐字相同）；若文档 id 本身
在两次运行间重新生成（测试夹具如此），并列候选之间的先后可能变化。

向量臂的召回族是调用方点名或获授权的**空间与文档**；`authenticated_public` 兜底是授权下限而
非语料订阅，不作为召回族（否则任何未收窄查询都会把整个部署的公开语料算进结果）。但每个被召回
的文档仍要按**完整范围（含公开兜底）**重新过滤才可能成为命中，因此授权包含规则、主体过滤、
生命周期与发布过滤与阶段 A 完全一致。

Qdrant 不可用时查询返回 `UNAVAILABLE`，`/readyz` 同时检查数据库与向量后端：服务不静默退化为
只查关键词然后报告一个与混合检索不符的总数。

## 配置

`configs/config.yaml`（本机）与 `configs/config.docker.yaml`（Compose）是同一组键的两种取值；
全部键都有默认值（`internal/config.Default()`）并在 `Config.Validate()` 中校验。
核心项：`index.*`（关键词侧与分页上限）、`vector.*`（见上）。其中
`vector.max_keyword_candidates`（默认 1000）是 **RRF 融合窗口宽度**：只有前这么多条关键词匹配
参与窗口内排序，窗口之外的匹配仍按关键词顺序完整可读。环境变量覆盖：
`DOCUMENT_SEARCH_VECTOR_{ENABLED,ENDPOINT,API_KEY,USE_TLS,TIMEOUT,PROFILE,DIMENSIONS,RECALL,RRF_K,MAX_KEYWORD_CANDIDATES,PRUNE_ON_REBUILD}`。

`vector.enabled: false` 时服务退回关键词检索：融合只剩一条臂，分页、`total`、过滤规则不变，
且 `total` 覆盖全部关键词匹配。

## 测试

集成测试连接**真实 PostgreSQL**（`127.0.0.1:15432/gin_demo`，角色
`document_search_writer`）与**真实 Qdrant**（`127.0.0.1:16334`）。连不上即 `t.Fatalf`，
没有任何 skip；`DOCUMENT_SEARCH_TEST_DSN` 可覆盖数据库 DSN。

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'gb-goweb'
cd apps/document-search
go build ./... ; go vet ./... ; gofmt -l .
go test ./... -count=1
go test ./... -count=1 -run Integration -v
```

`go test ./internal/application/ -run TestIntegrationEvaluationDatasetIsStableAndReproducible -v`
会打印固定数据集的逐查询结果（文档键、命中臂、`total`）。

## 数据库

`schema/schema_init.sql` 是新库初始化基线（Compose 只读挂载到 `/service-schema/document-search`），
且对已存在的开发库幂等：`document_index` 与 `index_generations` 的向量列通过
`ADD COLUMN IF NOT EXISTS` 补齐。运行中的旧开发库需要用同一组语句同步一次
（见阶段 B 证据文件的"数据库变更"一节）。
