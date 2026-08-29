package media

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dorm-memorial/internal/identity"
	"dorm-memorial/internal/storage"
)

type ProcessingJob struct {
	ID        string  `json:"id"`
	MediaID   string  `json:"media_id"`
	Phase     string  `json:"phase"`
	Step      string  `json:"step"`
	Encoder   string  `json:"encoder"`
	ErrorCode string  `json:"error_code"`
	Media     *Record `json:"media,omitempty"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

func (s *Store) StageVideo(ctx context.Context, actor identity.User, input UploadInput) (ProcessingJob, error) {
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	input.Filename = cleanFilename(input.Filename)
	input.MimeType = strings.ToLower(strings.TrimSpace(strings.Split(input.MimeType, ";")[0]))
	mediaType, ext, err := validateUpload(input)
	if err != nil || mediaType != "video" {
		return ProcessingJob{}, ErrInvalid
	}
	if s.objects == nil {
		return ProcessingJob{}, ErrStorageUnavailable
	}
	if existing, ok, err := s.processingByRequest(ctx, actor.ID, input.ClientRequestID); err != nil {
		return ProcessingJob{}, err
	} else if ok {
		return existing, nil
	}

	now := time.Now().UTC()
	mediaID, jobID := newID(), newID()
	objectPath := fmt.Sprintf("/originals/%s/%s/%s%s", remoteOwnerSegment(actor.ID), now.Format("2006/01"), mediaID, ext)
	if err := s.reserve(ctx, actor.ID, jobID, input.ClientRequestID, objectPath, input.Size); err != nil {
		return ProcessingJob{}, err
	}
	s.setJobState(ctx, jobID, "uploading", "")

	stagingDir, err := filepath.Abs(s.stagingDir)
	if err != nil || os.MkdirAll(stagingDir, 0o750) != nil {
		s.setJobState(context.WithoutCancel(ctx), jobID, "failed", "staging_directory_failed")
		return ProcessingJob{}, fmt.Errorf("prepare video staging directory: %w", ErrStorageUnavailable)
	}
	stagingPath := filepath.Join(stagingDir, jobID+ext)
	temp, err := os.CreateTemp(stagingDir, jobID+"-*.part")
	if err != nil {
		s.setJobState(context.WithoutCancel(ctx), jobID, "failed", "staging_create_failed")
		return ProcessingJob{}, fmt.Errorf("create video staging file: %w", ErrStorageUnavailable)
	}
	tempPath := temp.Name()
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hasher), input.Body)
	closeErr := temp.Close()
	if copyErr != nil || closeErr != nil || written != input.Size {
		_ = os.Remove(tempPath)
		s.setJobState(context.WithoutCancel(ctx), jobID, "failed", "staging_write_failed")
		return ProcessingJob{}, fmt.Errorf("stage video upload: %w", ErrInvalid)
	}
	if err := os.Rename(tempPath, stagingPath); err != nil {
		_ = os.Remove(tempPath)
		s.setJobState(context.WithoutCancel(ctx), jobID, "failed", "staging_commit_failed")
		return ProcessingJob{}, fmt.Errorf("commit video staging file: %w", ErrStorageUnavailable)
	}
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
	if duplicate, ok, err := s.processingByHash(ctx, actor.ID, hex.EncodeToString(hasher.Sum(nil))); err != nil {
		_ = os.Remove(stagingPath)
		s.setJobState(context.WithoutCancel(ctx), jobID, "failed", "database_failed")
		return ProcessingJob{}, err
	} else if ok {
		_ = os.Remove(stagingPath)
		s.setJobState(context.WithoutCancel(ctx), jobID, "cleaned", "duplicate_upload")
		return duplicate, nil
	}

	record := Record{ID: mediaID, OwnerID: actor.ID, OriginalFilename: input.Filename, MediaType: "video", MimeType: input.MimeType, SizeBytes: input.Size, SHA256: hex.EncodeToString(hasher.Sum(nil)), Status: "uploading", CreatedAt: now}
	if input.Width > 0 && input.Width <= 16384 && input.Height > 0 && input.Height <= 16384 {
		record.Width, record.Height = &input.Width, &input.Height
	}
	if input.DurationMS > 0 && input.DurationMS <= int64((48*time.Hour)/time.Millisecond) {
		record.DurationMS = &input.DurationMS
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		_ = os.Remove(stagingPath)
		s.setJobState(context.WithoutCancel(ctx), jobID, "failed", "database_failed")
		return ProcessingJob{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO media(id, owner_id, object_path, preview_path, original_filename, media_type, mime_type, size_bytes, sha256, width, height, duration_ms, status, created_at, updated_at)
		VALUES(?, ?, ?, '', ?, 'video', ?, ?, ?, ?, ?, ?, 'uploading', ?, ?)`, mediaID, actor.ID, objectPath, input.Filename, input.MimeType, input.Size, record.SHA256, nullableInt(record.Width), nullableInt(record.Height), nullableInt64(record.DurationMS), nowText(now), nowText(now)); err != nil {
		_ = tx.Rollback()
		_ = os.Remove(stagingPath)
		s.setJobState(context.WithoutCancel(ctx), jobID, "failed", "database_failed")
		return ProcessingJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO media_processing_jobs(id, media_id, user_id, staging_path, phase, created_at, updated_at) VALUES(?, ?, ?, ?, 'staged', ?, ?)`, jobID, mediaID, actor.ID, stagingPath, nowText(now), nowText(now)); err != nil {
		_ = tx.Rollback()
		_ = os.Remove(stagingPath)
		s.setJobState(context.WithoutCancel(ctx), jobID, "failed", "database_failed")
		return ProcessingJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE upload_jobs SET state = 'verifying', updated_at = ? WHERE id = ?`, nowText(now), jobID); err != nil {
		_ = tx.Rollback()
		_ = os.Remove(stagingPath)
		s.setJobState(context.WithoutCancel(ctx), jobID, "failed", "database_failed")
		return ProcessingJob{}, err
	}
	if err := tx.Commit(); err != nil {
		_ = os.Remove(stagingPath)
		s.setJobState(context.WithoutCancel(ctx), jobID, "failed", "database_failed")
		return ProcessingJob{}, err
	}

	job := ProcessingJob{ID: jobID, MediaID: mediaID, Phase: "staged", Media: &record, CreatedAt: nowText(now), UpdatedAt: nowText(now)}
	s.scheduleStagedVideo(jobID)
	return job, nil
}

func (s *Store) ProcessingStatus(ctx context.Context, actor identity.User, jobID string) (ProcessingJob, error) {
	job, userID, err := s.processingJob(ctx, jobID)
	if err != nil {
		return ProcessingJob{}, err
	}
	if actor.Role != "admin" && actor.ID != userID {
		return ProcessingJob{}, ErrForbidden
	}
	if job.Phase == "completed" {
		record, err := s.recordByID(ctx, job.MediaID)
		if err != nil {
			return ProcessingJob{}, err
		}
		job.Media = &record
	}
	return job, nil
}

func (s *Store) processingByRequest(ctx context.Context, userID, requestID string) (ProcessingJob, bool, error) {
	var jobID string
	err := s.db.QueryRowContext(ctx, `SELECT p.id FROM media_processing_jobs p JOIN upload_jobs u ON u.id = p.id WHERE p.user_id = ? AND u.client_request_id = ?`, userID, requestID).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return ProcessingJob{}, false, nil
	}
	if err != nil {
		return ProcessingJob{}, false, err
	}
	job, _, err := s.processingJob(ctx, jobID)
	return job, err == nil, err
}

func (s *Store) processingByHash(ctx context.Context, userID, sum string) (ProcessingJob, bool, error) {
	var jobID string
	err := s.db.QueryRowContext(ctx, `SELECT p.id FROM media_processing_jobs p JOIN media m ON m.id = p.media_id
		WHERE p.user_id = ? AND m.sha256 = ? AND p.phase != 'failed' ORDER BY CASE p.phase WHEN 'completed' THEN 0 ELSE 1 END, p.created_at LIMIT 1`, userID, sum).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return ProcessingJob{}, false, nil
	}
	if err != nil {
		return ProcessingJob{}, false, err
	}
	job, _, err := s.processingJob(ctx, jobID)
	return job, err == nil, err
}

func (s *Store) processingJob(ctx context.Context, jobID string) (ProcessingJob, string, error) {
	var job ProcessingJob
	var userID string
	err := s.db.QueryRowContext(ctx, `SELECT id, media_id, user_id, phase, step, encoder, error_code, created_at, updated_at FROM media_processing_jobs WHERE id = ?`, jobID).
		Scan(&job.ID, &job.MediaID, &userID, &job.Phase, &job.Step, &job.Encoder, &job.ErrorCode, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProcessingJob{}, "", ErrNotFound
	}
	return job, userID, err
}

func (s *Store) recordByID(ctx context.Context, mediaID string) (Record, error) {
	var objectPath string
	if err := s.db.QueryRowContext(ctx, `SELECT object_path FROM media WHERE id = ?`, mediaID).Scan(&objectPath); errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	} else if err != nil {
		return Record{}, err
	}
	return s.recordByObjectPath(ctx, objectPath)
}

type stagedVideo struct {
	jobID, mediaID, userID, stagingPath, objectPath, filename, mimeType, sha256, createdText string
	size                                                                                     int64
	durationMS                                                                               sql.NullInt64
}

func (s *Store) scheduleStagedVideo(jobID string) {
	s.runBackground("staged-video:"+jobID, func(ctx context.Context) {
		s.processStagedVideo(ctx, jobID)
	})
}

func (s *Store) processStagedVideo(ctx context.Context, jobID string) {
	var video stagedVideo
	err := s.db.QueryRowContext(ctx, `SELECT p.id, p.media_id, p.user_id, p.staging_path, m.object_path, m.original_filename, m.mime_type, m.size_bytes, m.sha256, m.created_at, m.duration_ms
		FROM media_processing_jobs p JOIN media m ON m.id = p.media_id WHERE p.id = ?`, jobID).
		Scan(&video.jobID, &video.mediaID, &video.userID, &video.stagingPath, &video.objectPath, &video.filename, &video.mimeType, &video.size, &video.sha256, &video.createdText, &video.durationMS)
	if err != nil {
		return
	}
	if info, err := os.Stat(video.stagingPath); err != nil || info.Size() != video.size {
		s.failProcessing(ctx, video, "staging_missing")
		return
	}
	playbackPath := video.stagingPath + ".playback.mp4"
	select {
	case s.videoTranscodes <- struct{}{}:
	case <-ctx.Done():
		s.failProcessing(ctx, video, "transcode_timeout")
		return
	}
	encoder, err := transcodeVideoFileWithProgress(ctx, s.ffmpegPath, s.videoEncoder, video.stagingPath, playbackPath, targetVideoBitrateKbps(video.size, video.durationMS.Int64), func(step, activeEncoder string) {
		s.updateProcessing(ctx, jobID, "transcoding", step, activeEncoder, "")
	})
	<-s.videoTranscodes
	if err != nil {
		s.failProcessing(ctx, video, "transcode_failed")
		return
	}
	playbackInfo, err := os.Stat(playbackPath)
	if err != nil || playbackInfo.Size() <= 0 {
		s.failProcessing(ctx, video, "transcode_output_invalid")
		return
	}

	createdAt, _ := time.Parse(time.RFC3339Nano, video.createdText)
	playbackObjectPath := "/playback/" + remoteOwnerSegment(video.userID) + "/" + createdAt.UTC().Format("2006/01") + "/" + video.mediaID + ".mp4"
	previewObjectPath := ""
	previewLocalPath := video.stagingPath + ".poster.jpg"
	seek := videoPreviewSeek(video.mediaID, video.durationMS.Int64)
	if err := extractVideoFrame(ctx, s.ffmpegPath, video.stagingPath, previewLocalPath, seek); err == nil {
		previewObjectPath = "/previews/" + remoteOwnerSegment(video.userID) + "/" + createdAt.UTC().Format("2006/01") + "/" + video.mediaID + ".jpg"
	}
	s.updateProcessing(ctx, jobID, "uploading", "", encoder, "")
	if err := putLocalFile(ctx, s.objects, video.objectPath, video.stagingPath); err != nil {
		s.failProcessing(ctx, video, "original_upload_failed")
		return
	}
	if err := putLocalFile(ctx, s.objects, playbackObjectPath, playbackPath); err != nil {
		_ = s.objects.Delete(context.WithoutCancel(ctx), video.objectPath)
		s.failProcessing(ctx, video, "playback_upload_failed")
		return
	}
	if previewObjectPath != "" {
		if err := putLocalFile(ctx, s.objects, previewObjectPath, previewLocalPath); err != nil {
			previewObjectPath = ""
		}
	}
	s.updateProcessing(ctx, jobID, "verifying", "", encoder, "")
	if err := s.verifySize(ctx, video.objectPath, video.size); err != nil || s.verifySize(ctx, playbackObjectPath, playbackInfo.Size()) != nil {
		_ = s.objects.Delete(context.WithoutCancel(ctx), video.objectPath)
		_ = s.objects.Delete(context.WithoutCancel(ctx), playbackObjectPath)
		if previewObjectPath != "" {
			_ = s.objects.Delete(context.WithoutCancel(ctx), previewObjectPath)
		}
		s.failProcessing(ctx, video, "storage_verify_failed")
		return
	}

	now := nowText(time.Now().UTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		_ = tx.Rollback()
		_ = s.objects.Delete(context.WithoutCancel(ctx), video.objectPath)
		_ = s.objects.Delete(context.WithoutCancel(ctx), playbackObjectPath)
		if previewObjectPath != "" {
			_ = s.objects.Delete(context.WithoutCancel(ctx), previewObjectPath)
		}
		s.failProcessing(ctx, video, "database_failed")
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE media SET preview_path = ?, status = 'ready', updated_at = ? WHERE id = ?`, previewObjectPath, now, video.mediaID); err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO media_variants(media_id, kind, object_path, mime_type, size_bytes, created_at, updated_at)
			VALUES(?, 'playback', ?, 'video/mp4', ?, ?, ?)
			ON CONFLICT(media_id, kind) DO UPDATE SET object_path = excluded.object_path, mime_type = excluded.mime_type, size_bytes = excluded.size_bytes, updated_at = excluded.updated_at`, video.mediaID, playbackObjectPath, playbackInfo.Size(), now, now)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE upload_jobs SET state = 'completed', error_code = '', updated_at = ? WHERE id = ?`, now, jobID)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE media_processing_jobs SET phase = 'completed', encoder = ?, error_code = '', staging_path = '', updated_at = ? WHERE id = ?`, encoder, now, jobID)
	}
	if err == nil {
		_, _ = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, metadata_json, created_at) VALUES(?, 'media.upload', 'media', ?, ?, ?)`, video.userID, video.mediaID, fmt.Sprintf(`{"size_bytes":%d,"encoder":%q}`, video.size, encoder), now)
		err = tx.Commit()
	}
	if err != nil {
		s.failProcessing(ctx, video, "database_failed")
		return
	}
	_ = os.Remove(video.stagingPath)
	_ = os.Remove(playbackPath)
	_ = os.Remove(previewLocalPath)
}

func putLocalFile(ctx context.Context, objects storage.ObjectStorage, objectPath, localPath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return objects.Put(ctx, objectPath, file, info.Size())
}

func (s *Store) updateProcessing(ctx context.Context, jobID, phase, step, encoder, code string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE media_processing_jobs SET phase = ?, step = ?, encoder = CASE WHEN ? = '' THEN encoder ELSE ? END, error_code = ?, updated_at = ? WHERE id = ?`, phase, step, encoder, encoder, code, nowText(time.Now().UTC()), jobID)
}

func (s *Store) failProcessing(ctx context.Context, video stagedVideo, code string) {
	now := nowText(time.Now().UTC())
	_, _ = s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE media_processing_jobs SET phase = 'failed', error_code = ?, updated_at = ? WHERE id = ?`, code, now, video.jobID)
	_, _ = s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE upload_jobs SET state = 'failed', error_code = ?, updated_at = ? WHERE id = ?`, code, now, video.jobID)
	_, _ = s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE media SET status = 'unavailable', updated_at = ? WHERE id = ?`, now, video.mediaID)
	_ = os.Remove(video.stagingPath)
	_ = os.Remove(video.stagingPath + ".playback.mp4")
	_ = os.Remove(video.stagingPath + ".poster.jpg")
}

func (s *Store) resumeStagedVideos(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM media_processing_jobs WHERE phase NOT IN ('completed', 'failed')`)
	if err != nil {
		return err
	}
	var jobIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		jobIDs = append(jobIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range jobIDs {
		s.scheduleStagedVideo(id)
	}
	return nil
}
