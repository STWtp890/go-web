# Go-Web 全栈管理系统

基于 **Go + go-zero** 后端框架与 **Vue 3 + Vite** 前端框架构建的企业级全栈 Web 管理系统，集成 JWT 认证、RBAC 权限控制、公告管理、Markdown 文章系统等功能。

---

## 项目结构

```
go-web/
├── backend/                    # 后端服务 (Go + go-zero)
│   ├── app/                    # 应用主目录
│   │   ├── api.go              # 入口文件
│   │   ├── Dockerfile          # 后端容器构建
│   │   ├── docker-compose.yaml # 后端服务编排 (含 MySQL、Redis)
│   │   ├── go.mod / go.sum     # Go 模块依赖
│   │   ├── etc/
│   │   │   └── api.yaml        # 应用配置文件
│   │   └── internal/
│   │       ├── common/         # 公共工具（BizError、缓存Key、软删除、UUIDv7 等）
│   │       ├── config/         # 配置结构体定义
│   │       ├── handler/        # HTTP 路由 & 处理器
│   │       │   ├── routes.go   # 路由注册（goctl 生成）
│   │       │   ├── announcement/
│   │       │   ├── auth/
│   │       │   ├── health/
│   │       │   ├── markdown/
│   │       │   ├── rbac/
│   │       │   └── user/
│   │       ├── logic/          # 业务逻辑层
│   │       ├── model/          # 数据模型层（带缓存）
│   │       ├── svc/            # 服务上下文（依赖注入）
│   │       └── types/          # 请求/响应类型定义
│   ├── doc/                    # 项目文档
│   ├── dsl/api/                # API 定义文件 (.api DSL)
│   ├── scripts/                # 代码生成脚本
│   └── sql/                    # 数据库 DDL & 种子数据
│
├── vue-dashboard/              # 前端服务 (Vue 3)
│   ├── Dockerfile              # 前端容器构建
│   ├── docker-compose.yaml     # 前端 + Nginx 编排
│   ├── server.js               # SPA 静态资源服务器 (Express)
│   ├── vite.config.js          # Vite 构建配置（含代理）
│   ├── package.json            # 前端依赖
│   ├── nginx/                  # Nginx 反向代理模板
│   ├── public/                 # 静态资源
│   └── src/
│       ├── App.vue / main.js   # 应用入口
│       ├── router.js           # 路由配置（Hash 模式）
│       ├── api/                # API 请求层（axios）
│       ├── stores/             # Pinia 状态管理
│       ├── composables/        # 组合式函数
│       ├── components/         # 通用组件（布局、UI、Alert 等）
│       ├── utils/              # 工具函数
│       └── views/              # 页面视图
│           ├── announcement/   # 公告管理
│           ├── article/        # 文章中心（Markdown）
│           ├── auth/           # 登录/注册
│           ├── dashboard/      # 仪表盘
│           ├── rbac/           # 角色权限管理
│           └── user/           # 用户资料
```

---

## 功能特性

| 模块 | 功能 |
|------|------|
| **认证授权** | 注册、登录、JWT 双 Token（Access + Refresh）、登出 |
| **RBAC** | 角色管理、权限管理、基于中间件的接口鉴权 |
| **公告管理** | 创建/编辑/删除/列表查询，支持管理员专属操作 |
| **Markdown 文章** | 发布/编辑/预览/列表，支持代码高亮、KaTeX 数学公式 |
| **文章审核** | 审核列表、审核详情，支持审核流程 |
| **用户管理** | 个人资料查看与编辑 |
| **健康检查** | 服务健康状态接口 |
| **仪表盘** | 系统概览首页 |

---

## 技术栈

### 后端

| 技术 | 用途 |
|------|------|
| **Go 1.26** | 编程语言 |
| **[go-zero](https://github.com/zeromicro/go-zero)** | Web 框架（REST API、中间件、配置管理） |
| **MySQL 8.0** | 关系型数据库 |
| **Redis** | 缓存 & Token 存储 |
| **[golang-jwt](https://github.com/golang-jwt/jwt)** | JWT 认证 |
| **bcrypt** | 密码哈希 (`golang.org/x/crypto`) |
| **Docker** | 容器化部署 |

### 前端

| 技术 | 用途 |
|------|------|
| **Vue 3** (Composition API) | 前端框架 |
| **Vite 6** | 构建工具 |
| **[Pinia](https://pinia.vuejs.org/)** | 状态管理（持久化插件） |
| **[Vue Router 4](https://router.vuejs.org/)** | 路由管理（Hash 模式） |
| **[Axios](https://axios-http.com/)** | HTTP 请求库 |
| **[marked](https://marked.js.org/)** | Markdown 渲染 |
| **[highlight.js](https://highlightjs.org/)** | 代码语法高亮 |
| **[KaTeX](https://katex.org/)** | 数学公式渲染 |
| **[github-markdown-css](https://github.com/sindresorhus/github-markdown-css)** | Markdown 样式 |
| **Sass/SCSS** | CSS 预处理 |
| **Express** | 生产环境 SPA 静态服务器 |

### 基础设施

| 技术 | 用途 |
|------|------|
| **Nginx** | 反向代理（统一入口） |
| **Docker Compose** | 多服务编排 |

---

## 快速开始

### 前置要求

- [Go](https://go.dev/dl/) >= 1.26
- [Node.js](https://nodejs.org/) >= 18
- [Docker](https://www.docker.com/) & Docker Compose（容器化部署）
- MySQL 8.0 + Redis（本地开发需要）

### 1. 克隆项目

```bash
git clone https://github.com/STWtp890/go-web.git
cd go-web
```

### 2. 启动后端服务

#### 方式 A：Docker Compose（推荐）

```bash
cd backend/app
docker compose up -d
```

这将自动启动 MySQL、Redis 和 Go 后端服务，后端默认监听 `:8888`。

#### 方式 B：本地开发

```bash
# 1. 确保 MySQL 和 Redis 已运行

# 2. 导入数据库
mysql -u root -p < backend/sql/a1_user.sql
mysql -u root -p < backend/sql/a2_rbac.sql
# ... 按顺序导入所有 SQL 文件

# 3. 配置环境变量或修改 etc/api.yaml

# 4. 启动后端
cd backend/app
go run api.go -f etc/api.yaml
```

### 3. 启动前端服务

#### 方式 A：Docker Compose（推荐）

```bash
cd vue-dashboard
docker compose up -d
```

前端 + Nginx 将通过 `http://localhost:80` 提供服务。

#### 方式 B：本地开发

```bash
cd vue-dashboard
npm install
npm run dev
```

前端开发服务器默认监听 `http://localhost:8080`，API 请求自动代理到后端 `localhost:8888`。

### 4. 访问系统

| 环境 | 地址 |
|------|------|
| Docker 生产 | `http://localhost` |
| 本地开发 | `http://localhost:8080` |

---

## 配置说明

### 后端配置 (`backend/app/etc/api.yaml` / 环境变量)

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `AUTH_ACCESS_SECRET` | JWT Access Token 密钥 | `your-256-bit-secret` |
| `AUTH_ACCESS_EXPIRE` | Access Token 过期时间（秒） | `86400`（24h） |
| `REFRESH_ACCESS_SECRET` | Refresh Token 密钥 | `your-256-bit-secret` |
| `REFRESH_ACCESS_EXPIRE` | Refresh Token 过期时间（秒） | `604800`（7天） |
| `DB_HOST` | 数据库地址 | `mysql` |
| `DB_PORT` | 数据库端口 | `3306` |
| `DB_NAME` | 数据库名 | `service_backend` |
| `DB_USER` | 数据库用户 | `service_backend` |
| `DB_PASSWORD` | 数据库密码 | - |
| `CACHE_REDIS_HOST` | 缓存 Redis 地址 | `redis` |
| `CACHE_REDIS_PORT` | 缓存 Redis 端口 | `6379` |
| `REDIS_HOST` | 业务 Redis 地址 | `redis` |
| `REDIS_PORT` | 业务 Redis 端口 | `6379` |

---

## API 接口概览

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| POST | `/api/v1/auth/register` | 无 | 用户注册 |
| POST | `/api/v1/auth/login` | 无 | 用户登录 |
| POST | `/api/v1/auth/refresh` | Refresh | 刷新 Token |
| GET | `/api/v1/user/profile/:userId` | JWT | 获取用户资料 |
| PUT | `/api/v1/user/profile/:userId` | JWT | 更新用户资料 |
| POST | `/api/v1/user/logout/:userId` | JWT | 用户登出 |
| GET | `/api/v1/announcement/list` | 无 | 公告列表 |
| GET | `/api/v1/announcement/preview/:id` | 无 | 公告预览 |
| POST | `/api/v1/admin/announcement/create` | JWT | 创建公告 |
| PUT | `/api/v1/admin/announcement/update/:id` | JWT | 更新公告 |
| DELETE | `/api/v1/admin/announcement/delete/:id` | JWT | 删除公告 |
| GET | `/api/v1/markdown/list` | 无 | 文章列表 |
| GET | `/api/v1/markdown/preview/:id` | 无 | 文章预览 |
| POST | `/api/v1/admin/markdown/create` | JWT | 发布文章 |
| PUT | `/api/v1/admin/markdown/update/:id` | JWT | 编辑文章 |
| DELETE | `/api/v1/admin/markdown/delete/:id` | JWT | 删除文章 |
| GET | `/api/v1/admin/rbac/role/list` | JWT | 角色列表 |
| POST | `/api/v1/admin/rbac/role/create` | JWT | 创建角色 |
| GET | `/api/v1/admin/rbac/permission/list` | JWT | 权限列表 |
| GET | `/api/v1/health` | 无 | 健康检查 |

---

## 数据库设计

- **`user`** — 用户表（UUIDv7 主键、bcrypt 密码、软删除）
- **`role`** — 角色表
- **`permission`** — 权限表
- **`user_role`** — 用户-角色关联表
- **`role_permission`** — 角色-权限关联表
- **`announcement`** — 公告表
- **`markdown`** — Markdown 文章表
- **`refresh_token`** — Refresh Token 存储表

所有表均采用 `BIGINT` 内部主键 + `UUIDv7` 对外 ID 的双 ID 设计，防止遍历攻击。

---

## 架构说明

```
┌─────────────────────────────────────────────────┐
│                   Nginx (:80)                    │
│              反向代理 / 统一入口                   │
└──────┬──────────────────────┬───────────────────┘
       │                      │
       ▼                      ▼
┌──────────────┐    ┌──────────────────────────────┐
│  Frontend    │    │        Backend (:8888)        │
│  Vue 3 SPA   │───▶│        Go + go-zero           │
│  (:5173)     │    │                               │
└──────────────┘    │  ┌─────────┐  ┌───────────┐  │
                    │  │  MySQL  │  │   Redis   │  │
                    │  └─────────┘  └───────────┘  │
                    └──────────────────────────────┘
```

- **前端**：Vue 3 SPA，Hash 路由模式，通过 Axios 调用后端 API
- **后端**：go-zero REST 框架，分层架构（Handler → Logic → Model）
- **Nginx**：统一反向代理，处理静态资源缓存和 API 转发
- **认证**：JWT 双 Token 机制，Access Token 短期有效，Refresh Token 用于续期

---

## 安全设计

- **密码**：bcrypt 哈希存储
- **Token**：JWT Access + Refresh 双 Token 机制
- **ID 防遍历**：对外使用 UUIDv7，内部使用自增 BIGINT
- **软删除**：所有数据使用 `deleted_at` 实现软删除
- **RBAC**：基于角色的访问控制，中间件级别鉴权
- **CORS**：go-zero 框架内置跨域支持

---

## 开发指南

### 代码生成

```bash
# 生成 API 路由和处理器（基于 .api DSL 文件）
cd backend/scripts
./zero-api-gen.bat

# 生成数据模型（基于数据库表结构）
./zero-model-gen.bat
```

### 目录规范

- `handler/` — 薄层，仅负责参数绑定和调用 Logic
- `logic/` — 业务逻辑核心，可复用
- `model/` — 数据访问层，集成 go-zero 缓存
- `types/` — 请求/响应结构体定义
- `common/` — 跨模块工具函数

---