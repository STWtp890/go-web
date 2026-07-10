-- ============================================================
-- auth.sql — 认证服务 DDL
--
-- ID 设计:
--   id (BIGINT PK)       — 内部主键，用于 JOIN
--   user_id (VARCHAR(36)) — 对象ID (UUIDv7)，对外暴露，防遍历攻击
--
-- 涉及接口：
--   Register / Login / RefreshToken / Logout
-- ============================================================
USE `service_backend`;
SET NAMES utf8mb4;

CREATE TABLE `user` (
  `id`         bigint       NOT NULL AUTO_INCREMENT  COMMENT '内部主键',
  `user_id`    varchar(64)  NOT NULL DEFAULT ''      COMMENT '对象ID (UUIDv7)',
  `username`   varchar(64)  NOT NULL DEFAULT ''      COMMENT '用户名',
  `password`   varchar(255) NOT NULL DEFAULT ''      COMMENT 'bcrypt 密码哈希',
  `email`      varchar(128) NOT NULL DEFAULT ''      COMMENT '邮箱',
  `created_at` DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at` DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  `deleted_at` datetime     DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_user_id` (`user_id`),
  UNIQUE KEY `idx_username` (`username`),
  UNIQUE KEY `idx_email` (`email`),
  KEY `idx_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;