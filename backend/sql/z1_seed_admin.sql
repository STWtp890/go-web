-- ============================================================
-- seed_admin.sql — 种子用户
-- 密码均为: Test@123456 (bcrypt)
-- ============================================================
USE `service_backend`;

INSERT INTO `user` (`user_id`, `username`, `password`, `email`, `created_at`, `updated_at`)
VALUES
    -- superadmin (role_id=1): 超级管理员，拥有全部权限
    (
        'user-00000000-0000-7000-8000-000000000001',
        'admin',
        '$2a$10$Gwjeeqai3EmDOjFvA3Jv1ONzysabbVbJc0495MPQODoUTNnV5jbwW',
        'admin@example.com',
        NOW(3),
        NOW(3)
    ),
    -- admin (role_id=2): 管理员，拥有业务管理权限，无权管理RBAC
    (
        'user-00000000-0000-7000-8000-000000000002',
        'manager',
        '$2a$10$Gwjeeqai3EmDOjFvA3Jv1ONzysabbVbJc0495MPQODoUTNnV5jbwW',
        'manager@example.com',
        NOW(3),
        NOW(3)
    ),
    -- user (role_id=3): 普通用户，可发布/编辑Markdown
    (
        'user-00000000-0000-7000-8000-000000000003',
        'user',
        '$2a$10$Gwjeeqai3EmDOjFvA3Jv1ONzysabbVbJc0495MPQODoUTNnV5jbwW',
        'user@example.com',
        NOW(3),
        NOW(3)
    );
