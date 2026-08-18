-- ============================================================
-- service/manager/seed_admin.sql — 预置初始管理员 (可选, 引导首个管理员后走审批流)
--
-- 替代 utils/automigrate 的 -seed-admin 职责 (bcrypt 哈希由 pgcrypto 等价生成)
-- 前置: 需先执行 service/manager/schema_init.sql 建 managers 表
-- 用法: psql -U postgres -d gin_demo \
--       -v admin_user=admin -v admin_pass='初始密码' \
--       -f deployments/postgresql/sql/service/manager/seed_admin.sql
-- 说明: 幂等 (已存在同名管理员时跳过); 密码经 pgcrypto crypt/gen_salt('bf')
--       生成 bcrypt 兼容哈希 ($2a$), Go bcrypt.CompareHashAndPassword 可验证
-- ============================================================

-- 1. pgcrypto 扩展 (crypt / gen_salt)
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- 2. 预置初始管理员 (已存在则跳过)
INSERT INTO managers (username, password, email, status)
SELECT :'admin_user',
       crypt(:'admin_pass', gen_salt('bf')),
       '',
       'active'
WHERE NOT EXISTS (
    SELECT 1 FROM managers WHERE username = :'admin_user'
);

-- 3. 验证
SELECT id, username, status,
       (password LIKE '$2a$%' OR password LIKE '$2b$%') AS is_bcrypt
  FROM managers
 WHERE username = :'admin_user';
