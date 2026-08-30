ALTER TABLE upload_jobs ADD COLUMN preview_path TEXT NOT NULL DEFAULT '';
ALTER TABLE upload_jobs ADD COLUMN request_fingerprint TEXT NOT NULL DEFAULT '';
