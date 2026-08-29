CREATE TABLE media_processing_jobs (
    id TEXT PRIMARY KEY,
    media_id TEXT NOT NULL UNIQUE REFERENCES media(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id),
    staging_path TEXT NOT NULL,
    phase TEXT NOT NULL CHECK (phase IN ('staged', 'transcoding', 'uploading', 'verifying', 'completed', 'failed')),
    encoder TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (id) REFERENCES upload_jobs(id) ON DELETE CASCADE
);

CREATE INDEX media_processing_jobs_phase_idx ON media_processing_jobs(phase, updated_at);
