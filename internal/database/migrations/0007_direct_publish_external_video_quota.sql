ALTER TABLE posts ADD COLUMN external_video_url TEXT NOT NULL DEFAULT '';

UPDATE posts
SET status = 'published',
    published_at = COALESCE(published_at, submitted_at, updated_at),
    moderation_note = ''
WHERE status = 'pending';

DROP TRIGGER IF EXISTS users_media_quota_2gib;
DROP TRIGGER IF EXISTS users_media_quota_10gib;

UPDATE users SET media_quota_bytes = 10737418240;

CREATE TRIGGER users_media_quota_10gib
AFTER INSERT ON users
BEGIN
    UPDATE users SET media_quota_bytes = 10737418240 WHERE id = NEW.id;
END;
