ALTER TABLE post_media RENAME TO post_media_v1;
ALTER TABLE guestbook_media RENAME TO guestbook_media_v1;
ALTER TABLE media RENAME TO media_v1;

CREATE TABLE media (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id),
    object_path TEXT NOT NULL UNIQUE,
    preview_path TEXT NOT NULL DEFAULT '',
    original_filename TEXT NOT NULL,
    media_type TEXT NOT NULL CHECK (media_type IN ('image', 'video', 'audio')),
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    sha256 TEXT NOT NULL,
    width INTEGER,
    height INTEGER,
    duration_ms INTEGER,
    status TEXT NOT NULL DEFAULT 'uploading' CHECK (status IN ('uploading', 'ready', 'unavailable', 'deleted')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

INSERT INTO media(id, owner_id, object_path, preview_path, original_filename, media_type, mime_type, size_bytes, sha256, width, height, duration_ms, status, created_at, updated_at, deleted_at)
SELECT id, owner_id, object_path, preview_path, original_filename, media_type, mime_type, size_bytes, sha256, width, height, duration_ms, status, created_at, updated_at, deleted_at FROM media_v1;

CREATE TABLE post_media (
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id),
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (post_id, media_id),
    UNIQUE (post_id, position)
);
INSERT INTO post_media(post_id, media_id, position) SELECT post_id, media_id, position FROM post_media_v1;

CREATE TABLE guestbook_media (
    entry_id TEXT NOT NULL REFERENCES guestbook_entries(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id),
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (entry_id, media_id)
);
INSERT INTO guestbook_media(entry_id, media_id, position) SELECT entry_id, media_id, position FROM guestbook_media_v1;

DROP TABLE post_media_v1;
DROP TABLE guestbook_media_v1;
DROP TABLE media_v1;

CREATE INDEX media_owner_idx ON media(owner_id, created_at DESC);
CREATE INDEX media_status_idx ON media(status, created_at DESC);
CREATE INDEX guestbook_media_media_idx ON guestbook_media(media_id);

CREATE TABLE message_media (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id),
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (message_id, media_id),
    UNIQUE (message_id, position)
);
CREATE INDEX message_media_media_idx ON message_media(media_id);
