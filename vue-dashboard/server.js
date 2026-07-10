// ============================================================
// SPA 静态资源服务器 — 用于 Docker 生产环境
// 处理 Vue Router (hash 模式) 的 SPA 回退
// ============================================================
import express from 'express'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const __dirname = dirname(fileURLToPath(import.meta.url))
const port = parseInt(process.env.PORT || '5173', 10)

const app = express()

// 注意：Gzip 压缩由上游 Nginx 统一处理，此处不再重复

// 静态资源（带 1 年强缓存）
app.use(express.static(join(__dirname, 'dist'), {
  maxAge: '1y',
  immutable: true,
  setHeaders: (res, filePath) => {
    // HTML 不缓存（SPA 入口）
    if (filePath.endsWith('.html')) {
      res.setHeader('Cache-Control', 'no-cache, no-store, must-revalidate')
    }
  }
}))

// SPA 回退：所有非静态资源路由 → index.html
app.get('*', (_req, res) => {
  res.sendFile(join(__dirname, 'dist', 'index.html'))
})

app.listen(port, '0.0.0.0', () => {
  console.log(`[frontend] SPA server running on http://0.0.0.0:${port}`)
})
