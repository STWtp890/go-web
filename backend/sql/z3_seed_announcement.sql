-- ============================================================
-- z_seed_announcement.sql — Announcement seed data
-- ============================================================
USE `service_backend`;
SET NAMES utf8mb4;

INSERT INTO `announcement` (`annouce_user_id`, `announcement_id`, `title`, `created_at`, `updated_at`) VALUES
  ('user-00000000-0000-7000-8000-000000000001', 'anno-00000000-0000-7000-8000-000000000001', 'Hello World!',                 NOW(3), NOW(3)),
  ('user-00000000-0000-7000-8000-000000000001', 'anno-00000000-0000-7000-8000-000000000002', 'Welcome to MyWeb',            NOW(3), NOW(3)),
  ('user-00000000-0000-7000-8000-000000000001', 'anno-00000000-0000-7000-8000-000000000003', 'Announcement System Sample',  NOW(3), NOW(3));

INSERT INTO `announcement_content` (`announcement_id`, `content`) VALUES
  ('anno-00000000-0000-7000-8000-000000000001', 'Hello World! This is the first announcement.'),
  ('anno-00000000-0000-7000-8000-000000000002', 'Welcome to MyWeb. Enjoy your stay!'),
  ('anno-00000000-0000-7000-8000-000000000003', 'This is a sample announcement to demonstrate the system.');
