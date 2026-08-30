package media

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"net"
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
	s.scheduleStagedVideo(jobID, true)
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
	err := s.db.QueryRowContext(ctx, `SELECT p.id FROM media_processing_jobs p JOIN upload_jobs u ON u.id = p.id JOIN media m ON m.id = p.media_id WHERE p.user_id = ? AND u.client_request_id = ? AND m.status IN ('uploading', 'ready') AND p.phase != 'failed'`, userID, requestID).Scan(&jobID)
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
		WHERE p.user_id = ? AND m.sha256 = ? AND m.status IN ('uploading', 'ready') AND p.phase != 'failed' ORDER BY CASE p.phase WHEN 'completed' THEN 0 ELSE 1 END, p.created_at LIMIT 1`, userID, sum).Scan(&jobID)
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

// Checkpoints are committed only after a complete artifact or remote save. They
// are hints, not proof: local files and remote objects are checked before reuse.
type videoCheckpoint struct {
	Prepared         bool             `json:"prepared"`
	Preparation      videoPreparation `json:"preparation"`
	PlaybackSize     int64            `json:"playback_size"`
	PreviewSize      int64            `json:"preview_size"`
	OriginalUploaded bool             `json:"original_uploaded"`
	PlaybackUploaded bool             `json:"playback_uploaded"`
	PreviewUploaded  bool             `json:"preview_uploaded"`
}

func (s *Store) scheduleStagedVideo(jobID string, freshlyStaged bool) {
	s.runBackground("staged-video:"+jobID, func(ctx context.Context) {
		s.processStagedVideoWithSource(ctx, jobID, freshlyStaged)
	})
}

func (s *Store) processStagedVideo(ctx context.Context, jobID string) {
	s.processStagedVideoWithSource(ctx, jobID, false)
}

func (s *Store) processStagedVideoWithSource(ctx context.Context, jobID string, freshlyStaged bool) {
	var video stagedVideo
	var checkpointJSON string
	err := s.db.QueryRowContext(ctx, `SELECT p.id, p.media_id, p.user_id, p.staging_path, m.object_path, m.original_filename, m.mime_type, m.size_bytes, m.sha256, m.created_at, m.duration_ms, p.checkpoint_json
		FROM media_processing_jobs p JOIN media m ON m.id = p.media_id WHERE p.id = ? AND p.phase NOT IN ('completed', 'failed') AND m.status = 'uploading'`, jobID).
		Scan(&video.jobID, &video.mediaID, &video.userID, &video.stagingPath, &video.objectPath, &video.filename, &video.mimeType, &video.size, &video.sha256, &video.createdText, &video.durationMS, &checkpointJSON)
	if err != nil {
		return
	}
	// StageVideo just hashed a newly received file. Rehash only on recovery,
	// when another process/lifetime may have changed the staged original.
	if !validStagedOriginal(video, !freshlyStaged) {
		s.failProcessing(ctx, video, "staging_missing")
		return
	}
	var checkpoint videoCheckpoint
	// Invalid/old checkpoint data is safe to discard and regenerate.
	if json.Unmarshal([]byte(checkpointJSON), &checkpoint) != nil {
		checkpoint = videoCheckpoint{}
	}
	playbackPath := video.stagingPath + ".playback.mp4"
	preparedPath := playbackPath
	if checkpoint.Preparation.UseOriginal {
		preparedPath = video.stagingPath
	}
	if !checkpoint.Prepared || !validPreparedVideo(preparedPath, checkpoint.PlaybackSize) {
		checkpoint.Prepared, checkpoint.PlaybackUploaded = false, false
		if err := s.saveVideoCheckpoint(ctx, jobID, checkpoint); err != nil {
			s.failProcessing(ctx, video, "database_failed")
			return
		}
		select {
		case s.videoTranscodes <- struct{}{}:
		case <-ctx.Done():
			s.failProcessing(ctx, video, "transcode_timeout")
			return
		}
		preparation, prepareErr := prepareVideoPlaybackFile(ctx, s.ffmpegPath, s.videoEncoder, video.stagingPath, playbackPath, video.size, video.durationMS.Int64, func(step, activeEncoder string) {
			s.updateProcessing(ctx, jobID, "transcoding", step, activeEncoder, "")
		})
		<-s.videoTranscodes
		if prepareErr != nil {
			s.failProcessing(ctx, video, "transcode_failed")
			return
		}
		preparedPath = playbackPath
		if preparation.UseOriginal {
			preparedPath = video.stagingPath
		}
		info, statErr := os.Stat(preparedPath)
		if statErr != nil || !validPreparedVideo(preparedPath, info.Size()) {
			s.failProcessing(ctx, video, "transcode_output_invalid")
			return
		}
		checkpoint.Prepared, checkpoint.Preparation, checkpoint.PlaybackSize = true, preparation, info.Size()
		if err := s.saveVideoCheckpoint(ctx, jobID, checkpoint); err != nil {
			s.failProcessing(ctx, video, "database_failed")
			return
		}
	}

	createdAt, _ := time.Parse(time.RFC3339Nano, video.createdText)
	playbackObjectPath := "/playback/" + remoteOwnerSegment(video.userID) + "/" + createdAt.UTC().Format("2006/01") + "/" + video.mediaID + ".mp4"
	if checkpoint.Preparation.UseOriginal {
		playbackObjectPath = video.objectPath
	}
	previewObjectPath := ""
	previewLocalPath := video.stagingPath + ".poster.jpg"
	s.updateProcessing(ctx, jobID, "transcoding", "finalizing", checkpoint.Preparation.Encoder, "")
	if !validPreparedPreview(previewLocalPath, checkpoint.PreviewSize) {
		checkpoint.PreviewSize, checkpoint.PreviewUploaded = 0, false
		seek := videoPreviewSeek(video.mediaID, checkpoint.Preparation.DurationMS)
		previewCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		previewErr := extractVideoFrame(previewCtx, s.ffmpegPath, video.stagingPath, previewLocalPath, seek)
		if previewErr != nil && seek != 0 && previewCtx.Err() == nil {
			previewErr = extractVideoFrame(previewCtx, s.ffmpegPath, video.stagingPath, previewLocalPath, 0)
		}
		cancel()
		if info, statErr := os.Stat(previewLocalPath); previewErr == nil && statErr == nil && validPreparedPreview(previewLocalPath, info.Size()) {
			checkpoint.PreviewSize = info.Size()
		}
		if err := s.saveVideoCheckpoint(ctx, jobID, checkpoint); err != nil {
			s.failProcessing(ctx, video, "database_failed")
			return
		}
	}
	if checkpoint.PreviewSize > 0 {
		previewObjectPath = "/previews/" + remoteOwnerSegment(video.userID) + "/" + createdAt.UTC().Format("2006/01") + "/" + video.mediaID + ".jpg"
	}
	encoder := checkpoint.Preparation.Encoder
	s.updateProcessing(ctx, jobID, "uploading", "", encoder, "")
	if err := s.saveCheckpointedObject(ctx, video, &checkpoint, &checkpoint.OriginalUploaded, video.objectPath, video.stagingPath, video.size); err != nil {
		s.failProcessing(ctx, video, "original_upload_failed")
		return
	}
	if !checkpoint.Preparation.UseOriginal {
		if err := s.saveCheckpointedObject(ctx, video, &checkpoint, &checkpoint.PlaybackUploaded, playbackObjectPath, playbackPath, checkpoint.PlaybackSize); err != nil {
			s.failProcessing(ctx, video, "playback_upload_failed")
			return
		}
	}
	if previewObjectPath != "" {
		if err := s.saveCheckpointedObject(ctx, video, &checkpoint, &checkpoint.PreviewUploaded, previewObjectPath, previewLocalPath, checkpoint.PreviewSize); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			_ = s.objects.Delete(cleanupCtx, previewObjectPath)
			cancel()
			previewObjectPath = ""
		}
	}
	s.updateProcessing(ctx, jobID, "verifying", "", encoder, "")
	if err := s.verifySize(ctx, video.objectPath, video.size); err != nil {
		s.failProcessing(ctx, video, "storage_verify_failed")
		return
	}
	if playbackObjectPath != video.objectPath {
		if err := s.verifySize(ctx, playbackObjectPath, checkpoint.PlaybackSize); err != nil {
			s.failProcessing(ctx, video, "storage_verify_failed")
			return
		}
	}
	if err := s.completeStagedVideo(ctx, video, checkpoint, playbackObjectPath, previewObjectPath); err != nil {
		s.failProcessing(ctx, video, "database_failed")
		return
	}
	removeStagedVideoFiles(video.stagingPath)
}

// Returning from this helper releases the sole SQLite connection before the
// caller writes failure status; BeginTx errors must never dereference a nil tx.
func (s *Store) completeStagedVideo(ctx context.Context, video stagedVideo, checkpoint videoCheckpoint, playbackObjectPath, previewObjectPath string) error {
	now := nowText(time.Now().UTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	preparation := checkpoint.Preparation
	result, err := tx.ExecContext(ctx, `UPDATE media SET preview_path = ?, status = 'ready', duration_ms = CASE WHEN ? > 0 THEN ? ELSE duration_ms END, width = CASE WHEN ? > 0 THEN ? ELSE width END, height = CASE WHEN ? > 0 THEN ? ELSE height END, updated_at = ? WHERE id = ? AND status = 'uploading'`, previewObjectPath, preparation.DurationMS, preparation.DurationMS, preparation.Width, preparation.Width, preparation.Height, preparation.Height, now, video.mediaID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	// Delete/Purge may run while encoding or saving objects. Finalization must
	// not resurrect deleted media or create variants for a removed record.
	if updated != 1 {
		return ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO media_variants(media_id, kind, object_path, mime_type, size_bytes, created_at, updated_at)
		VALUES(?, 'playback', ?, 'video/mp4', ?, ?, ?)
		ON CONFLICT(media_id, kind) DO UPDATE SET object_path = excluded.object_path, mime_type = excluded.mime_type, size_bytes = excluded.size_bytes, updated_at = excluded.updated_at`, video.mediaID, playbackObjectPath, checkpoint.PlaybackSize, now, now)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE upload_jobs SET state = 'completed', error_code = '', updated_at = ? WHERE id = ?`, now, video.jobID)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE media_processing_jobs SET phase = 'completed', step = '', encoder = ?, error_code = '', staging_path = '', updated_at = ? WHERE id = ?`, preparation.Encoder, now, video.jobID)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, metadata_json, created_at) VALUES(?, 'media.upload', 'media', ?, ?, ?)`, video.userID, video.mediaID, fmt.Sprintf(`{"size_bytes":%d,"encoder":%q}`, video.size, preparation.Encoder), now)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
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

func (s *Store) saveVideoCheckpoint(ctx context.Context, jobID string, checkpoint videoCheckpoint) error {
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE media_processing_jobs SET checkpoint_json = ?, updated_at = ? WHERE id = ?`, string(encoded), nowText(time.Now().UTC()), jobID)
	return err
}

func validStagedOriginal(video stagedVideo, verifyHash bool) bool {
	file, err := os.Open(video.stagingPath)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != video.size || video.size <= 0 {
		return false
	}
	if !verifyHash {
		return true
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	return err == nil && hex.EncodeToString(hash.Sum(nil)) == video.sha256
}

func validPreparedVideo(filename string, expectedSize int64) bool {
	info, err := os.Stat(filename)
	if err != nil || !info.Mode().IsRegular() || expectedSize <= 0 || info.Size() != expectedSize {
		return false
	}
	fast, err := MP4FastStart(filename)
	return err == nil && fast
}

func validPreparedPreview(filename string, expectedSize int64) bool {
	if expectedSize <= 0 || expectedSize > maxVideoPreviewBytes {
		return false
	}
	file, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != expectedSize {
		return false
	}
	_, err = jpeg.DecodeConfig(file)
	return err == nil
}

func retryableVideoStorageError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, storage.ErrUnauthorized) || errors.Is(err, storage.ErrInvalidPath) {
		return false
	}
	var networkError net.Error
	return errors.Is(err, storage.ErrUnavailable) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &networkError)
}

// Transient storage failures retry just this stage, keeping prepared artifacts
// and prior saves. Each attempt reopens the file from byte zero.
func retryVideoStorage(ctx context.Context, operation func() error) error {
	var err error
	for _, delay := range []time.Duration{0, time.Second, 3 * time.Second} {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = operation()
		if err == nil || !retryableVideoStorageError(err) {
			return err
		}
	}
	return err
}

func (s *Store) saveCheckpointedObject(ctx context.Context, video stagedVideo, checkpoint *videoCheckpoint, uploaded *bool, objectPath, localPath string, size int64) error {
	if *uploaded {
		var info storage.ObjectInfo
		err := retryVideoStorage(ctx, func() error {
			var err error
			info, err = s.objects.Stat(ctx, objectPath)
			return err
		})
		if err == nil && !info.IsDir && info.Size == size {
			return nil
		}
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		*uploaded = false
	}
	if err := retryVideoStorage(ctx, func() error { return putLocalFile(ctx, s.objects, objectPath, localPath) }); err != nil {
		return err
	}
	*uploaded = true
	return s.saveVideoCheckpoint(ctx, video.jobID, *checkpoint)
}

func removeStagedVideoFiles(stagingPath string) {
	if stagingPath == "" {
		return
	}
	for _, suffix := range []string{"", ".playback.mp4", ".playback.mp4.encoded.mp4", ".poster.jpg"} {
		_ = os.Remove(stagingPath + suffix)
	}
}

func (s *Store) updateProcessing(ctx context.Context, jobID, phase, step, encoder, code string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE media_processing_jobs SET phase = ?, step = ?, encoder = CASE WHEN ? = '' THEN encoder ELSE ? END, error_code = ?, updated_at = ? WHERE id = ?`, phase, step, encoder, encoder, code, nowText(time.Now().UTC()), jobID)
}

func (s *Store) failProcessing(ctx context.Context, video stagedVideo, code string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	now := nowText(time.Now().UTC())
	_, _ = s.db.ExecContext(cleanupCtx, `UPDATE media_processing_jobs SET phase = 'failed', error_code = ?, updated_at = ? WHERE id = ?`, code, now, video.jobID)
	_, _ = s.db.ExecContext(cleanupCtx, `UPDATE upload_jobs SET state = 'failed', error_code = ?, updated_at = ? WHERE id = ?`, code, now, video.jobID)
	_, _ = s.db.ExecContext(cleanupCtx, `UPDATE media SET status = 'unavailable', updated_at = ? WHERE id = ? AND status = 'uploading'`, now, video.mediaID)
	// Cleanup is terminal only, never between upload retries. Paths are unique to
	// this media ID, including any partial write whose acknowledgement was lost.
	if s.objects != nil {
		createdAt, _ := time.Parse(time.RFC3339Nano, video.createdText)
		prefix := remoteOwnerSegment(video.userID) + "/" + createdAt.UTC().Format("2006/01") + "/" + video.mediaID
		for _, objectPath := range []string{video.objectPath, "/playback/" + prefix + ".mp4", "/previews/" + prefix + ".jpg"} {
			_ = s.objects.Delete(cleanupCtx, objectPath)
		}
	}
	removeStagedVideoFiles(video.stagingPath)
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
		s.scheduleStagedVideo(id, false)
	}
	return nil
}
