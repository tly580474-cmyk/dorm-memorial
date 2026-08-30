-- Checkpoints describe durable local artifacts and completed remote saves.
-- A marker is always validated before it is reused after a restart.
ALTER TABLE media_processing_jobs ADD COLUMN checkpoint_json TEXT NOT NULL DEFAULT '{}';
