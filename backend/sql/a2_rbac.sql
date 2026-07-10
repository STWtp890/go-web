-- ============================================================
-- rbac.sql — RBAC 权限服务 DDL
--
-- ID 设计:
--   id (BIGINT PK)     — 内部主键，同服务内 JOIN 使用，直接对外暴露
--
-- 关联键设计原则:
--   user     → UUIDv7  (跨服务引用 auth.users.user_id)
--   role     → 内部 id  (同服务内引用 roles.id)
--   permission → 内部 id (同服务内引用 permissions.id)
--
-- 关联关系：
--   user_roles        — 用户-角色 多对多 (user_id=UUIDv7, role_id=roles.id)
--   role_permissions  — 角色-权限 多对多 (role_id=roles.id, permission_id=permissions.id)
--
-- 涉及接口：
--   CreatePermission / DeletePermission / ListPermissions / UpdatePermission
--   CreateRole / DeleteRole / ListRoles / UpdateRole
--   AssignUserRole / AssignRolePermission
-- ============================================================
USE `service_backend`;

SET NAMES utf8mb4;

-- ── permission ────────────────────────────────────────────
CREATE TABLE `permission` (
    `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
    `resource` varchar(128) NOT NULL DEFAULT '' COMMENT '资源标识',
    `action` varchar(64) NOT NULL DEFAULT '' COMMENT '操作类型',
    `label` varchar(128) NOT NULL DEFAULT '' COMMENT '权限名称',
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_resource_action` (`resource`, `action`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- ── roles ──────────────────────────────────────────────────
CREATE TABLE `role` (
    `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
    `name` varchar(64) NOT NULL DEFAULT '' COMMENT '角色名称',
    `description` varchar(255) NOT NULL DEFAULT '' COMMENT '角色描述',
    `deleted_at` datetime DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_name` (`name`),
    KEY `idx_deleted_at` (`deleted_at`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- ── user_role（用户-角色 多对多关联）──────────────────────
-- user_id (UUIDv7) → 跨服务引用 auth.users.user_id
-- role_id (内部id)  → 同服务内引用 roles.id
CREATE TABLE `user_role` (
    `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
    `user_id` varchar(64) NOT NULL COMMENT '用户UUID (→ users.user_id)',
    `role_id` bigint NOT NULL COMMENT '角色内部ID (→ roles.id)',
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_user_role` (`user_id`, `role_id`),
    KEY `idx_role_id` (`role_id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- ── role_permission（角色-权限 多对多关联）────────────────
-- role_id (内部id)       → 同服务内引用 roles.id
-- permission_id (内部id)  → 同服务内引用 permissions.id
CREATE TABLE `role_permission` (
    `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
    `role_id` bigint NOT NULL COMMENT '角色内部ID (→ roles.id)',
    `permission_id` bigint NOT NULL COMMENT '权限内部ID (→ permissions.id)',
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_role_perm` (`role_id`, `permission_id`),
    KEY `idx_permission_id` (`permission_id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
