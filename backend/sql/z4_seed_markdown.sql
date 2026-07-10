-- ============================================================
-- z_seed_markdown.sql — Markdown 示例种子数据
-- 分表结构: markdown + markdown_content + markdown_review_info
-- ============================================================
USE `service_backend`;
SET NAMES utf8mb4;

INSERT INTO `markdown` (`markdown_id`, `author_id`, `title`, `summary`, `created_at`, `updated_at`) VALUES
  ('md-00000000-0000-7000-8000-000000000001', 'user-00000000-0000-7000-8000-000000000001', 'Sample Markdown',  'A sample markdown entry', NOW(3), NOW(3)),
  ('md-00000000-0000-7000-8000-000000000002', 'user-00000000-0000-7000-8000-000000000003', 'Getting Started',  'Quick start guide',       NOW(3), NOW(3));

INSERT INTO `markdown_content` (`markdown_id`, `content`) VALUES
  ('md-00000000-0000-7000-8000-000000000001', '# Hello\n\nThis is a sample markdown.'),
  ('md-00000000-0000-7000-8000-000000000002', '## Quick Start\n\n1. Install\n2. Configure\n3. Run');

INSERT INTO `markdown_review_info` (`markdown_id`, `reviewer_id`, `review_status`, `review_comment`, `review_at`) VALUES
  ('md-00000000-0000-7000-8000-000000000001', 'user-00000000-0000-7000-8000-000000000001', 'approved', '', NOW(3)),
  ('md-00000000-0000-7000-8000-000000000002', '',                                        'pending',  '', NOW(3));
