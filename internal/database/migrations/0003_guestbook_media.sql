CREATE TABLE guestbook_media (
    entry_id TEXT NOT NULL REFERENCES guestbook_entries(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id),
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (entry_id, media_id)
);

CREATE INDEX guestbook_media_media_idx ON guestbook_media(media_id);
