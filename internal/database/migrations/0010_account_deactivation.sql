-- non-transactional
-- 账号注销：users.status 从 ('active','disabled') 扩展为 ('active','disabled','deactivated')。
-- SQLite 不能就地修改 CHECK 约束，且存在外键引用时无法在事务内 DROP 父表，
-- 因此本迁移以非事务方式重建 users 表（PRAGMA foreign_keys 只能在事务外修改）。

PRAGMA foreign_keys = OFF;

CREATE TABLE users_new (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL COLLATE NOCASE UNIQUE,
    email TEXT NOT NULL COLLATE NOCASE UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'deactivated')),
    media_quota_bytes INTEGER NOT NULL DEFAULT 21474836480 CHECK (media_quota_bytes >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO users_new (id, username, email, password_hash, role, status, media_quota_bytes, created_at, updated_at)
    SELECT id, username, email, password_hash, role, status, media_quota_bytes, created_at, updated_at FROM users;

DROP TABLE users;

ALTER TABLE users_new RENAME TO users;

-- 恢复 0007 建立的 10 GiB 新用户配额触发器（重建表会连带删除挂在 users 上的触发器）。
CREATE TRIGGER users_media_quota_10gib
AFTER INSERT ON users
BEGIN
    UPDATE users SET media_quota_bytes = 10737418240 WHERE id = NEW.id;
END;

PRAGMA foreign_keys = ON;