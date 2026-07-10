Optimized tool selection

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/backend/app/internal/handler/routes.go#1-1), lines 1 to 200

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/backend/app/internal/types/types.go#1-1), lines 1 to 300

Read [](file:///d%3A/Users/STWtp/Desktop/Go/go-web/backend/app/internal/types/types.go#300-300), lines 300 to 326

以下是完整的后端 API 接口文档：

---

# 后端 API 接口文档

**Base URL**：`http://<host>:8888/api/v1`  
**鉴权方式**：JWT Bearer Token（Header: `Authorization: Bearer <accessToken>`）  
**响应格式**：所有接口返回 HTTP 200，业务状态码在 JSON body 中

---

## 通用约定

### 统一响应结构

```json
{
  "code": 200,
  "message": "success",
  "data": { ... }
}
```

| code | 含义 |
|------|------|
| 200 | 成功 |
| 401 | 未认证 / token 无效 |
| 403 | 权限不足 |
| 404 | 资源不存在 |
| 500 | 服务器内部错误 |

### 分页参数

```json
{
  "page": 1,
  "pageSize": 10
}
```

分页响应 `data` 中会额外包含：

```json
{
  "totalCount": 42,
  "page": 1,
  "pageSize": 10
}
```

---

## 1. 认证模块 `/auth`

### 1.1 注册

```
POST /api/v1/auth/register
```

**请求体**：

```json
{
  "registerInfo": {
    "userId": "019f0f0f-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "userName": "testuser",
    "userPassword": "password123",
    "email": "test@example.com"
  }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| userId | string | 否 | UUIDv7，不填则自动生成 |
| userName | string | 是 | 用户名（唯一） |
| userPassword | string | 是 | 密码（bcrypt 哈希存储） |
| email | string | 是 | 邮箱（唯一） |

**响应**：

```json
{
  "code": 200,
  "message": "Success Registered!",
  "data": {
    "userId": "019f0f0f-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "userName": "testuser",
    "email": "test@example.com"
  }
}
```

---

### 1.2 登录

```
POST /api/v1/auth/login
```

**请求体**：

```json
{
  "userId": "019f0f0f-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "userPassword": "password123"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| userId | string | 是 | 注册时获得的 UUID |
| userPassword | string | 是 | 密码 |

**响应**：

```json
{
  "code": 200,
  "message": "Login successful",
  "accessToken": "eyJhbGciOiJIUzI1NiIs...",
  "refreshToken": "eyJhbGciOiJIUzI1NiIs...",
  "expiresIn": 86400,
  "userInfo": {
    "userId": "019f0f0f-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "userName": "testuser",
    "email": "test@example.com"
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| accessToken | string | 访问令牌，24h 有效 |
| refreshToken | string | 刷新令牌，7d 有效 |
| expiresIn | int64 | accessToken 有效秒数（86400） |

---

### 1.3 刷新 Token

```
POST /api/v1/auth/refresh
```

**鉴权**：需要 Refresh Token JWT（不是 Access Token）  
**请求体**：

```json
{
  "refreshToken": "eyJhbGciOiJIUzI1NiIs..."
}
```

**响应**：同登录接口，返回新的 accessToken + refreshToken

---

## 2. 用户模块 `/user`

> 所有接口需要 `Authorization: Bearer <accessToken>`

### 2.1 获取用户信息

```
GET /api/v1/user/profile/:userId
```

**路径参数**：

| 参数 | 类型 | 说明 |
|------|------|------|
| userId | string | 用户 UUID |

**响应**：

```json
{
  "code": 200,
  "message": "Success.",
  "data": {
    "userId": "019f0f0f-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "userName": "testuser",
    "email": "test@example.com"
  }
}
```

### 2.2 登出

```
POST /api/v1/user/logout/:userId
```

**路径参数**：

| 参数 | 类型 | 说明 |
|------|------|------|
| userId | string | 用户 UUID |

**响应**：

```json
{
  "code": 200,
  "message": "Logged out successfully"
}
```

---

## 3. 公告模块 `/announcement`

### 3.1 公告列表（公开）

```
GET /api/v1/announcement/list
```

**查询参数**：

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| page | int | 1 | 页码 |
| pageSize | int | 10 | 每页条数 |

**响应**：

```json
{
  "code": 200,
  "message": "success",
  "totalCount": 15,
  "page": 1,
  "pageSize": 10,
  "data": {
    "announcementList": [
      {
        "announcementId": "019f0f0f-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
        "title": "系统维护通知",
        "content": "# 维护通知\n...",
        "createdAt": 1750000000,
        "updatedAt": 1750000000
      }
    ]
  }
}
```

> 该接口带 30s Redis 缓存

### 3.2 创建公告

```
POST /api/v1/announcement/create
```

> 需要 `announcement:create` 权限

**请求体**：

```json
{
  "title": "公告标题",
  "content": "公告内容（支持 Markdown）"
}
```

**响应**：

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "announcementId": "019f0f0f-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "title": "公告标题",
    "content": "公告内容",
    "createdAt": 1750000000,
    "updatedAt": 1750000000
  }
}
```

### 3.3 更新公告

```
PUT /api/v1/announcement/update/:announcementId
```

> 需要 `announcement:update` 权限

**路径参数**：

| 参数 | 类型 | 说明 |
|------|------|------|
| announcementId | string | 公告 UUID |

**请求体**：

```json
{
  "title": "新标题",
  "content": "新内容"
}
```

### 3.4 删除公告（软删除）

```
DELETE /api/v1/announcement/delete/:announcementId
```

> 需要 `announcement:delete` 权限

**路径参数**：

| 参数 | 类型 | 说明 |
|------|------|------|
| announcementId | string | 公告 UUID |

---

## 4. 文档模块 `/markdown`

### 4.1 文档列表（公开）

```
GET /api/v1/markdown/list
```

**查询参数**：

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| page | int | 1 | 页码 |
| pageSize | int | 10 | 每页条数 |

**响应**：

```json
{
  "code": 200,
  "message": "success",
  "totalCount": 30,
  "page": 1,
  "pageSize": 10,
  "data": {
    "markdownList": [
      {
        "markdownId": "019f0f0f-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
        "title": "Go 语言入门",
        "summary": "Go 语言基础教程...",
        "content": "# Go 语言入门\n\n...",
        "createdAt": 1750000000,
        "updatedAt": 1750000000
      }
    ]
  }
}
```

> 该接口带 30s Redis 缓存，列表不含完整 `content`（由 summary 代替）

### 4.2 文档预览（公开）

```
GET /api/v1/markdown/preview/:markdownId
```

**路径参数**：

| 参数 | 类型 | 说明 |
|------|------|------|
| markdownId | string | 文档 UUID |

**响应**：返回完整 content

### 4.3 上传文档

```
POST /api/v1/markdown/upload
```

> 需要 `markdown:create` 权限

**请求体**：

```json
{
  "title": "文档标题",
  "content": "# Markdown 内容\n..."
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| title | string | 是 | 文档标题 |
| content | string | 是 | 完整 Markdown 内容（前 100 字符自动生成 summary） |

### 4.4 更新文档

```
PUT /api/v1/markdown/update/:markdownId
```

> 需要 `markdown:update` 权限

**请求体**：

```json
{
  "title": "新标题",
  "content": "新内容"
}
```

### 4.5 删除文档（软删除）

```
DELETE /api/v1/markdown/delete/:markdownId
```

> 需要 `markdown:delete` 权限

---

## 5. RBAC 权限管理 `/rbac`

> 所有接口需要 `Authorization: Bearer <accessToken>` + 对应权限

### 5.1 角色管理

| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| `GET` | `/rbac/roles/list` | `role:list` | 角色列表（分页） |
| `POST` | `/rbac/roles/create` | `role:create` | 创建角色 |
| `PUT` | `/rbac/roles/update/:roleId` | `role:update` | 更新角色 |
| `DELETE` | `/rbac/roles/delete/:roleId` | `role:delete` | 删除角色（软删除） |
| `POST` | `/rbac/roles/assign/:userId` | `role:assign` | 为用户分配角色 |

**创建/更新角色请求体**：

```json
{
  "roleId": "admin",
  "name": "管理员",
  "description": "系统管理员",
  "parentId": "moderator",
  "permissions": ["announcement:create", "markdown:create"]
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| roleId | string | 是（创建时） | 角色标识 |
| name | string | 是 | 角色名称 |
| description | string | 否 | 描述 |
| parentId | string | 否 | 父角色 ID（L2 RBAC 继承） |
| permissions | []string | 否 | 权限列表，格式 `"resource:action"` |

**用户分配角色请求体**：

```json
{
  "roles": ["admin", "editor"]
}
```

### 5.2 权限管理

| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| `GET` | `/rbac/permissions/list` | `permission:list` | 权限列表（分页） |
| `POST` | `/rbac/permissions/create` | `permission:create` | 创建权限 |
| `PUT` | `/rbac/permissions/update/:permissionId` | `permission:update` | 更新权限 |
| `DELETE` | `/rbac/permissions/delete/:permissionId` | `permission:delete` | 删除权限（物理删除） |
| `POST` | `/rbac/permissions/assign/:permissionId` | `permission:assign` | 为角色分配权限 |

**创建权限请求体**：

```json
{
  "permission": {
    "permissionId": "announcement_create",
    "resource": "announcement",
    "action": "create",
    "label": "创建公告"
  }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| permissionId | string | 是 | 权限标识（唯一） |
| resource | string | 是 | 资源名 |
| action | string | 是 | 操作类型（create/read/update/delete/list/assign） |
| label | string | 否 | 权限中文描述 |

**为角色分配权限请求体**：

```json
{
  "permissions": ["announcement:create", "markdown:create"]
}
```

---

## 6. 健康检查 `/status`

### 6.1 存活检查

```
GET /api/v1/status/healthz
```

**响应**（SSE 流）：

```json
{"code": 200, "message": "Server is healthy."}
```

### 6.2 就绪检查

```
GET /api/v1/status/readyz
```

**响应**（SSE 流）：

```json
{"code": 200, "message": "Server is ready: All dependencies are healthy."}
```

> 检查 MySQL + Redis 连通性

---

## 附录

### 鉴权总览

```
无鉴权:
  GET  /announcement/list
  GET  /markdown/list
  GET  /markdown/preview/:markdownId
  POST /auth/login
  POST /auth/register
  GET  /status/healthz
  GET  /status/readyz

Access Token JWT:
  所有 /announcement/* (除 list)
  所有 /markdown/* (除 list, preview)
  所有 /rbac/*
  所有 /user/*

Refresh Token JWT:
  POST /auth/refresh
```

### 错误响应示例

```json
// 401 - 未认证
{"code": 401, "message": "Incorrect password"}

// 403 - 权限不足
{"code": 403, "message": "Permission denied"}

// 404 - 资源不存在
{"code": 404, "message": "announcement not found"}

// 500 - 服务端错误
{"code": 500, "message": "query failed"}
```

### JWT 使用方式

```
Authorization: Bearer <accessToken>
```