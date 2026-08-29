CREATE TABLE media_variants (
    media_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('display', 'playback')),
    object_path TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (media_id, kind),
    FOREIGN KEY (media_id) REFERENCES media(id) ON DELETE CASCADE
);

CREATE INDEX media_variants_kind_idx ON media_variants(kind, media_id);
