# Markdown 审核功能变更说明

> **版本**: v1.0 → v1.1  
> **日期**: 2026-07-05  
> **影响范围**: Markdown 模块全部接口（响应字段 + 列表行为）

---

## 一、核心变更

审核字段（`reviewer_id` / `review_status` / `review_comment`）已从独立的 `markdown_review_info` 表**并入 `markdowns` 主表**，实现"一次查表、多处使用"。前端无需关心后端表结构变化，但需适配以下接口行为变更。

---

## 二、`Markdown` 对象新增字段

> 所有返回 `Markdown` 对象的接口均受影响（List / Preview / Upload / Update）

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `reviewerId` | `string` | ❌ optional | 审核人 userId（空字符串 = 未审核） |
| `reviewStatus` | `string` | ❌ optional | 审核状态，取值见下表 |
| `reviewComment` | `string` | ❌ optional | 审核意见 |

### `reviewStatus` 枚举值

| 值 | 含义 |
|---|---|
| `pending` | 待审核（新建/修改后的初始状态） |
| `approved` | 审核通过 |
| `rejected` | 审核驳回 |

### 示例响应差异

```diff
// GET /api/v1/markdown/list 单条记录
{
  "markdownId": "md-00...001",
+ "reviewerId": "user-00...001",
+ "reviewStatus": "approved",
+ "reviewComment": "",
  "title": "Sample",
  ...
}
```

---

## 三、接口行为变更

### 3.1 `GET /api/v1/markdown/list` — 公开列表 ⚠️ 行为变更

| 项目 | 变更前 | 变更后 |
|---|---|---|
| 过滤条件 | 无，返回全部未删除 | **仅返回 `reviewStatus = 'approved'`** |
| 权限 | 无需登录 | 无需登录（不变） |

> 📌 **前端适配**：如果之前用此接口展示"全部文章"，现在只会看到已审核通过的。  
> 管理员查看待审核列表请使用 `GET /api/v1/admin/markdown/unreviewed`。

---

### 3.2 `POST /api/v1/markdown/upload` — 上传文章 ⚠️ 行为变更

| 项目 | 变更前 | 变更后 |
|---|---|---|
| 审核状态 | 写入 `markdown_review_info` 表 | **直接写入 `markdowns.reviewStatus = 'pending'`** |

请求体不变，响应新增审核字段（见第二章）。

---

### 3.3 `PUT /api/v1/markdown/update/:markdownId` — 更新文章 ⚠️ 行为变更

| 项目 | 变更前 | 变更后 |
|---|---|---|
| 审核状态 | 不变 | **重置为 `reviewStatus = 'pending'`**（需重新审核） |

> 📌 **前端适配**：更新成功后，前端应提示用户"内容已保存，需重新审核"。可依据 `reviewStatus` 字段展示审核状态标签。

---

### 3.4 `GET /api/v1/admin/markdown/review/:markdownId` — 获取审核详情

请求/响应格式不变，数据来源从 `markdown_review_info` 表切换为 `markdowns` 表。

---

### 3.5 `PUT /api/v1/admin/markdown/review/:markdownId` — 提交审核

请求体不变：
```json
{
  "status": "approved | rejected",
  "comment": "审核意见（可选）"
}
```

响应新增 `authorId`：
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "markdownId": "md-...",
    "authorId": "user-...",
    "status": "approved",
    "comment": ""
  }
}
```

---

### 3.6 `GET /api/v1/admin/markdown/unreviewed` — 待审核列表

| 项目 | 变更前 | 变更后 |
|---|---|---|
| 查询方式 | `JOIN markdown_review_info` | 单表 `WHERE review_status = 'pending'` |

请求/响应格式不变，性能提升。

---

### 3.7 不受影响的接口

| 接口 | 说明 |
|---|---|
| `GET /api/v1/markdown/preview/:markdownId` | 仍按 ID 直接返回，不限制审核状态 |
| `DELETE /api/v1/markdown/delete/:markdownId` | 软删除，无变更 |

---

## 四、Markdown 对象完整定义

```typescript
interface Markdown {
  markdownId:    string;  // 对象ID (UUIDv7)
  authorId:      string;  // 作者 userId
  title:         string;  // 标题
  summary:       string;  // 摘要（正文截取前100字）
  content:       string;  // Markdown 正文
  reviewerId:    string;  // [新增] 审核人 userId，未审核时为空字符串
  reviewStatus:  string;  // [新增] pending | approved | rejected
  reviewComment: string;  // [新增] 审核意见
  createdAt:     string;  // RFC3339
  updatedAt:     string;  // RFC3339
}
```

---

## 五、完整路由速查

```
# 公开接口（无需登录）
GET    /api/v1/markdown/list                         → 已审核文章列表
GET    /api/v1/markdown/preview/:markdownId          → 预览单篇

# 需登录（JWT Auth）
POST   /api/v1/markdown/upload                       → 上传文章 (→ pending)
PUT    /api/v1/markdown/update/:markdownId           → 更新文章 (→ pending)
DELETE /api/v1/markdown/delete/:markdownId           → 删除文章

# 管理员接口（JWT Auth + markdown:review 权限）
GET    /api/v1/admin/markdown/unreviewed             → 待审核列表
GET    /api/v1/admin/markdown/review/:markdownId     → 审核详情
PUT    /api/v1/admin/markdown/review/:markdownId     → 提交审核
```

---

## 六、前端适配建议

1. **列表页**：原 `/markdown/list` 现仅返回已审核文章，如需兼容旧逻辑请与后端沟通
2. **详情/卡片**：利用 `reviewStatus` 展示审核标签（待审核 / 已通过 / 已驳回）
3. **编辑后**：提示用户"已提交重新审核"，可引导至审核状态查看
4. **管理员面板**：审核列表无需额外 JOIN，加载速度更快
5. **可选字段**：`reviewerId` / `reviewStatus` / `reviewComment` 标记为 `optional`，未审核时可能为空字符串，请做空值防御
