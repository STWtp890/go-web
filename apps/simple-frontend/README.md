# Paperplane 前端

基于 Vue 3、TypeScript、Pinia 和 Vue Router 的内容创作前端，对接同仓库 `gin-backend`。

## 本地运行

要求 Node.js 20.19+ 或 22.12+。后端默认运行在 `http://127.0.0.1:8080`。

```bash
npm install
npm run dev
```

开发服务器使用 Vite 代理 `/api`、`/healthz` 和 `/readyz`，默认访问地址为 `http://localhost:5173`。

生产环境可通过 `VITE_API_BASE_URL` 指定后端基地址：

```bash
VITE_API_BASE_URL=https://api.example.com npm run build
```

## 可用命令

- `npm run dev`：启动开发服务器。
- `npm run type-check`：运行 Vue/TypeScript 类型检查。
- `npm run build`：类型检查后生成生产构建。
- `npm run preview`：本地预览生产构建。

完整架构和接口覆盖说明见 [FRONTEND_SOLUTION.md](./FRONTEND_SOLUTION.md)。
