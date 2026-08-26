CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL CHECK (type IN ('group', 'direct')),
    title TEXT NOT NULL DEFAULT '',
    direct_key TEXT UNIQUE,
    created_by TEXT REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE conversation_members (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TEXT NOT NULL,
    last_read_at TEXT NOT NULL,
    PRIMARY KEY (conversation_id, user_id)
);

CREATE INDEX conversation_members_user_idx ON conversation_members(user_id, conversation_id);

CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sender_id TEXT NOT NULL REFERENCES users(id),
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'sent' CHECK (status IN ('sent', 'recalled')),
    created_at TEXT NOT NULL,
    recalled_at TEXT
);

CREATE INDEX messages_conversation_idx ON messages(conversation_id, created_at DESC, id DESC);

CREATE TABLE notifications (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    kind TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    event_key TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    read_at TEXT
);

CREATE INDEX notifications_user_idx ON notifications(user_id, read_at, created_at DESC, id DESC);
