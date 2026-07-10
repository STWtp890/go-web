# ===== 构建阶段 =====
FROM node:22-alpine AS builder

WORKDIR /app

# 利用 Docker 缓存层：先复制依赖文件
COPY package.json ./

RUN npm install --legacy-peer-deps --registry=https://registry.npmmirror.com

# 复制源码并构建
COPY . .
RUN npm run build

# ===== 运行阶段 =====
FROM node:22-alpine

WORKDIR /app

# 仅复制运行时所需的文件
COPY --from=builder /app/package.json ./
COPY --from=builder /app/server.js ./
COPY --from=builder /app/dist ./dist

# 仅安装生产依赖（express）
RUN npm install --omit=dev --legacy-peer-deps --registry=https://registry.npmmirror.com

EXPOSE 5173

CMD ["node", "server.js"]
