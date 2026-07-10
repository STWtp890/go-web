-- ============================================================
-- markdown.sql — Markdown 管理服务 DDL
--
-- ID 设计:
--   id (BIGINT PK)           — 内部主键
--   markdown_id (VARCHAR(64)) — 对象ID (UUIDv7)，防遍历攻击
--
-- 审核流程 (markdown_review_info):
--   review_status: pending → approved / rejected (管理员审核)
--   reviewer_id: 审核人 UUID (→ users.user_id)
--
-- 涉及接口：
--   ListMarkdowns / PreviewMarkdown / UploadMarkdown / UpdateMarkdown / DeleteMarkdown
--   ListUnreviewedMarkdowns / ReviewMarkdown (管理员审核)
-- ============================================================
USE `service_backend`;

SET NAMES utf8mb4;

CREATE TABLE `markdown` (
    `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
    `markdown_id` varchar(64) NOT NULL DEFAULT '' COMMENT '对象ID (UUIDv7)',
    `author_id` varchar(64) NOT NULL DEFAULT '' COMMENT '作者 users.user_id (UUIDv7)',
    `title` varchar(255) NOT NULL DEFAULT '',
    `summary` varchar(512) NOT NULL DEFAULT '',
    `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    `deleted_at` datetime DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_markdown_id` (`markdown_id`),
    KEY `idx_deleted_at` (`deleted_at`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `markdown_review_info` (
    `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
    `markdown_id` varchar(64) NOT NULL DEFAULT '' COMMENT '对象ID (UUIDv7)',
    `reviewer_id` varchar(64) NOT NULL DEFAULT '' COMMENT '审核人 users.user_id (UUIDv7)',
    `review_status` varchar(32) NOT NULL DEFAULT 'pending' COMMENT '审核状态: pending|approved|rejected',
    `review_comment` varchar(512) NOT NULL DEFAULT '' COMMENT '审核意见',
    `review_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_markdown_id` (`markdown_id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `markdown_content` (
    `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
    `markdown_id` varchar(64) NOT NULL DEFAULT '' COMMENT '对象ID (UUIDv7)',
    `content` text NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_markdown_id` (`markdown_id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

ALTER TABLE `markdown_review_info`
    ADD CONSTRAINT `fk_markdown_review_info_markdown_id` FOREIGN KEY (`markdown_id`) REFERENCES `markdown` (`markdown_id`) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE `markdown_content`
    ADD CONSTRAINT `fk_markdown_content_markdown_id` FOREIGN KEY (`markdown_id`) REFERENCES `markdown` (`markdown_id`) ON DELETE CASCADE ON UPDATE CASCADE;