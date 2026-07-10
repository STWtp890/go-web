-- ============================================================
-- announcement.sql — 公告服务 DDL
--
-- ID 设计:
--   id (BIGINT PK)              — 内部主键
--   announcement_id (VARCHAR(36)) — 对象ID (UUIDv7)，防遍历攻击
--
-- 涉及接口：
--   CreateAnnouncement / UpdateAnnouncement
--   DeleteAnnouncement / ListAnnouncement
-- ============================================================
USE `service_backend`;
SET NAMES utf8mb4;

CREATE TABLE `announcement` (
  `id`              bigint       NOT NULL AUTO_INCREMENT  COMMENT '内部主键',
  `annouce_user_id` varchar(64)  NOT NULL DEFAULT ''      COMMENT '发布人 users.user_id (UUIDv7)',
  `announcement_id` varchar(64)  NOT NULL DEFAULT ''      COMMENT '对象ID (UUIDv7)',
  `title`           varchar(255) NOT NULL DEFAULT '',
  `created_at`      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at`      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  `deleted_at`      datetime     DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_announcement_id` (`announcement_id`),
  KEY `idx_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `announcement_content` (
  `id`              bigint       NOT NULL AUTO_INCREMENT  COMMENT '内部主键',
  `announcement_id` varchar(64)  NOT NULL DEFAULT ''      COMMENT '对象ID (UUIDv7)',
  `content`         text         NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_announcement_id` (`announcement_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE `announcement_content`
  ADD CONSTRAINT `fk_announcement_content_announcement_id` FOREIGN KEY (`announcement_id`) REFERENCES `announcement` (`announcement_id`) ON DELETE CASCADE ON UPDATE CASCADE;