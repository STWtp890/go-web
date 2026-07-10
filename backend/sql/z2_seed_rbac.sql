-- ============================================================
-- z_seed_rbac.sql — RBAC 初始化种子数据
--
-- 包含:
--   permissions     — 权限定义
--   roles           — 角色定义
--   role_permissions — 角色-权限分配
--   user_roles      — 用户-角色分配 (依赖 z_seed_admin.sql)
--
-- 注: 所有 ID 均为数据库自增内部 ID，权限/角色不再使用 UUIDv7。
-- ============================================================
USE `service_backend`;
SET NAMES utf8mb4;

-- ── permissions ────────────────────────────────────────────
INSERT INTO
    `permission` (
        `resource`,
        `action`,
        `label`
    )
VALUES
    ('permission', 'create', '创建权限'),
    ('permission', 'update', '更新权限'),
    ('permission', 'assign', '分配权限'),
    ('permission', 'delete', '删除权限'),
    ('permission', 'list',   '查看权限列表'),
    ('role',       'create', '创建角色'),
    ('role',       'update', '更新角色'),
    ('role',       'assign', '分配角色'),
    ('role',       'delete', '删除角色'),
    ('role',       'list',   '查看角色列表'),
    ('announcement', 'create', '创建公告'),
    ('announcement', 'update', '更新公告'),
    ('announcement', 'delete', '删除公告'),
    ('announcement', 'list',   '查看公告列表'),
    ('markdown', 'create', '创建Markdown'),
    ('markdown', 'update', '更新Markdown'),
    ('markdown', 'delete', '删除Markdown'),
    ('markdown', 'list',   '查看Markdown列表'),
    ('markdown', 'review', '审核Markdown'),
    ('markdown', 'review:list', '查看审核列表');

-- ── roles ──────────────────────────────────────────────────
INSERT INTO
    `role` (
        `name`,
        `description`
    )
VALUES
    ('superadmin', '超级管理员'),
    ('admin',      '管理员'),
    ('user',       '普通用户'),
    ('visitor',    '访客');

-- ═══════════════════════════════════════════════════════════
-- role_permission — 角色-权限分配
-- 权限ID:  20=查看审核列表
-- ═══════════════════════════════════════════════════════════
INSERT INTO
    `role_permission` (`role_id`, `permission_id`)
VALUES
    -- superadmin (role_id=1): 全部
    (1,1),(1,2),(1,3),(1,4),(1,5),(1,6),(1,7),(1,8),(1,9),(1,10),
    (1,11),(1,12),(1,13),(1,14),(1,15),(1,16),(1,17),(1,18),(1,19),(1,20),
    -- admin (role_id=2): 业务管理
    (2,11),(2,12),(2,13),(2,14),
    (2,15),(2,16),(2,17),(2,18),(2,19),(2,20),
    -- user (role_id=3): Markdown CRUD + 公告只读
    (3,14),(3,15),(3,16),(3,17),(3,18),
    -- visitor (role_id=4): 只读
    (4,14),(4,18);

-- ═══════════════════════════════════════════════════════════
-- user_role — 用户-角色分配 (user_id=UUIDv7, role_id=内部id)
-- ═══════════════════════════════════════════════════════════
INSERT INTO
    `user_role` (`user_id`, `role_id`)
VALUES
    ( 'user-00000000-0000-7000-8000-000000000001', 1 ),
    ( 'user-00000000-0000-7000-8000-000000000002', 2 ),
    ( 'user-00000000-0000-7000-8000-000000000003', 3 ),
    ( 'user-00000000-0000-7000-8000-000000000004', 4 );
