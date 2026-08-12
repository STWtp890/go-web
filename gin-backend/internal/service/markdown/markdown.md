# Markdown 文档业务设计

## 定位与存储

markdown 业务用于**登录用户上传自己的 Markdown 文章并可随时取回**。

存储选用 **PostgreSQL** (auth/markdown/chat 统一使用 PostgreSQL), 连接复用
`internal/common/connection/postgresql` 的注册模式:

- 服务名: `connection.ServiceMarkdown = "markdown"`
- 生命周期: `service.Init` 中 `PostgreSQLManager.RegisterAndGet` 注册, cleanup 中 `Unregister` 释放
- 表结构: 经独立迁移工具 `utils/automigrate` 的 AutoMigrate 创建/更新 (不再随服务启动自动建表)

### 表设计 (元信息与内容分离)

| 表 | 字段要点 | 说明 |
| --- | --- | --- |
| `markdowns` | `id`(PK), `markdown_id`(UUID 唯一), `author_id`(JWT sub), `title`, `summary`, `visibility`(public/private), `search_text`(text, 供检索), 时间字段, `deleted_at`(软删除) | 列表查询不加载大字段; `search_text` 供全文检索 |
| `markdown_contents` | `id`(PK), `markdown_id`(唯一), `content`(text) | 与元信息 1:1, 详情时才读取 |

- `markdown_id` 为 UUID, 对外暴露防遍历攻击; 内部自增 `id` 不下发。
- **可见性** (`visibility`): `public` 公开 (所有登录用户可浏览列表/详情), `private` 私有 (仅作者可见, 默认); 详情接口对 private 非作者返回 403。
- **软删除** (GORM): `deleted_at` 为 `gorm.DeletedAt` 类型 (见 `orm.TimeFiled`), 删除自动转为 UPDATE 软删, 查询自动附加 `deleted_at IS NULL`, 物理删除需 `Unscoped()`。
- **全文检索** (`search_text`): 文本列, 内容 = 标题 + 摘要 + 正文;
  经 **ParadeDB pg_search** 的 BM25 索引 (`USING bm25`, 中文 **jieba** 分词) 实现智能检索,
  索引由迁移工具 `utils/automigrate -index` 幂等创建, 依赖 `../deployments/postgresql/sql/pg_search_setup.sql` 安装扩展。

## 接口设计

全部走鉴权路由 `protected` (JWT 中间件已注入 claims):

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/api/v1/protected/markdown/upload` | 上传文章 (title + content + 可选 visibility, 默认 private) |
| `GET` | `/api/v1/protected/markdown/mine` | 我的文章分页列表 (summary, 不含 content) |
| `GET` | `/api/v1/protected/markdown/public` | 公开文章分页列表 (visibility=public, 所有登录用户可浏览) |
| `GET` | `/api/v1/protected/markdown/search` | 全文搜索我的文章 (按相关度排序) |
| `GET` | `/api/v1/protected/markdown/:markdownId` | 文章详情 (含完整 content) |

### 上传

```json
POST /api/v1/protected/markdown/upload
{
  "title": "Go 语言入门",
  "content": "# 标题\n\n正文内容..."
}
```

- `title` 必填 (1~255); `content` 必填 (Markdown 原文)。
- `author_id` 取自 JWT claims 的 `sub`, 用户只能操作自己的文章。
- `summary` 自动取正文前 100 个字符。
- `search_text` 与元信息同事务写入 (标题+摘要+正文)。
- 元信息与内容在**同一事务**内写入 (经 `postgresql.Transaction`), 失败整体回滚。

### 全文搜索

```text
GET /api/v1/protected/markdown/search?keyword=Go 并发&page=1&pageSize=10
```

- 基于 ParadeDB pg_search 的 **BM25 智能检索**: `search_text ||| ?` (match disjunction, 命中任一词)
  与普通过滤 (`author_id` / `deleted_at`) 叠加, 按 `pdb.score(markdown_id)` 相关度倒序分页。
- `|||` 对原始用户输入安全 (按字段 tokenizer 分词匹配, 不解析查询语法); 如需 AND/OR/字段前缀等
  高级语法可改用 `pdb.parse()`。
- 中文分词使用内置 **jieba** (`pdb.jieba`, 词典+统计模型, 官方最先进中文分词器);
  也可换 lindera (`pdb.lindera(chinese)`) 或 unicode 等其它 tokenizer。
- **前置准备**: 执行 `../deployments/postgresql/sql/pg_search_setup.sql` 安装 pg_search 扩展 (推荐官方镜像
  `paradedb/paradedb`); BM25 索引由 `utils/automigrate -index` 创建, 未装扩展时创建会失败并提示。
- 如需 `pdb.snippet()` 高亮片段可作为后续扩展。
- 注意: 新增 `search_text` 列后, 存量文章需回填 (见 `../deployments/postgresql/sql/pg_search_setup.sql` 第 4 步):
  `UPDATE markdowns SET search_text = title || ' ' || summary || ' ' || (SELECT content FROM markdown_contents WHERE markdown_id = markdowns.markdown_id)`

### 返回

```json
// GET /api/v1/protected/markdown/mine?page=1&pageSize=10
{ "success": true, "data": { "markdownList": [...] }, "meta": { "page": 1, "per_page": 10, "total": 3, "total_pages": 1 } }

// GET /api/v1/protected/markdown/:markdownId
{ "success": true, "data": { "markdownId": "...", "title": "...", "summary": "...", "content": "# ...", "createdAt": ..., "updatedAt": ... } }
```

## 代码组织 (对齐 service/auth)

```text
internal/service/markdown/
├── api/markdown.go        # SetRouteGroup 路由注册
├── handler/               # HTTP 层: 绑定参数 + 提取用户 + 调用 logic
│   ├── common.go          # currentUserID 从 claims 提取 sub
│   ├── upload.go
│   ├── list.go
│   ├── search.go
│   └── detail.go
├── logic/                 # 业务层: 数据库访问 (markdownDB 复用 postgresql 管理器)
│   ├── common.go
│   ├── upload.go
│   ├── list.go
│   ├── search.go          # 全文搜索 (pg_search ||| + pdb.score)
│   └── detail.go
└── types/
    └── requests/          # 请求 DTO

GORM 模型统一迁移至 `internal/orm/markdown/` (包名 markdown, 引用时常用别名
markdownmodel; 含 Markdown / Content), 与 auth 的 `internal/orm/auth/` 对齐;
时间字段共用 `internal/orm/base.go` 的 `TimeFiled`。
```
