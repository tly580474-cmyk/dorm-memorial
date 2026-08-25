CREATE TABLE posts (
    id TEXT PRIMARY KEY,
    author_id TEXT NOT NULL REFERENCES users(id),
    body TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'pending', 'published', 'hidden', 'deleted')),
    visibility TEXT NOT NULL DEFAULT 'members' CHECK (visibility IN ('members', 'private')),
    content_date TEXT,
    moderation_note TEXT NOT NULL DEFAULT '',
    submitted_at TEXT,
    published_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE INDEX posts_feed_idx ON posts(status, published_at DESC, id DESC);
CREATE INDEX posts_author_idx ON posts(author_id, updated_at DESC, id DESC);
CREATE INDEX posts_content_date_idx ON posts(status, content_date DESC, id DESC);

CREATE TABLE media (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id),
    object_path TEXT NOT NULL UNIQUE,
    preview_path TEXT NOT NULL DEFAULT '',
    original_filename TEXT NOT NULL,
    media_type TEXT NOT NULL CHECK (media_type IN ('image', 'video')),
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

CREATE INDEX media_owner_idx ON media(owner_id, created_at DESC);
CREATE INDEX media_status_idx ON media(status, created_at DESC);

CREATE TABLE post_media (
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id),
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (post_id, media_id),
    UNIQUE (post_id, position)
);

CREATE TABLE tags (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE content_tags (
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (post_id, tag_id)
);

CREATE TABLE comments (
    id TEXT PRIMARY KEY,
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    author_id TEXT NOT NULL REFERENCES users(id),
    parent_id TEXT REFERENCES comments(id),
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'visible' CHECK (status IN ('visible', 'hidden', 'deleted')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE INDEX comments_post_idx ON comments(post_id, created_at, id);

CREATE TABLE reactions (
    post_id TEXT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL DEFAULT 'like' CHECK (kind IN ('like')),
    created_at TEXT NOT NULL,
    PRIMARY KEY (post_id, user_id, kind)
);

CREATE TABLE guestbook_entries (
    id TEXT PRIMARY KEY,
    author_id TEXT NOT NULL REFERENCES users(id),
    recipient_id TEXT REFERENCES users(id),
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'visible' CHECK (status IN ('visible', 'hidden', 'deleted')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE INDEX guestbook_recipient_idx ON guestbook_entries(recipient_id, created_at DESC, id DESC);

CREATE TABLE upload_jobs (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id),
    client_request_id TEXT NOT NULL,
    object_path TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'uploading', 'verifying', 'completed', 'failed', 'cleanup_required', 'cleaned')),
    expected_size INTEGER NOT NULL CHECK (expected_size >= 0),
    error_code TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (user_id, client_request_id)
);

CREATE INDEX upload_jobs_state_idx ON upload_jobs(state, updated_at);

ALTER TABLE users ADD COLUMN media_quota_bytes INTEGER NOT NULL DEFAULT 21474836480 CHECK (media_quota_bytes >= 0);
