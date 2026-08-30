package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"dorm-memorial/internal/storage"
)

var uploadCleanupTimeout = 30 * time.Second

const uploadStateTimeout = 5 * time.Second

type uploadRequestLock struct {
	ch   chan struct{}
	refs int
}

// previewPutTracker observes only the derived preview write. It lets the
// synchronous upload distinguish an image that was too large/unsupported
// from a provider Put that may have left a partial preview behind, without
// changing the image renderer's public result type.
type previewPutTracker struct {
	storage.ObjectStorage
	previewPath string
	attempted   bool
	err         error
}

func (t *previewPutTracker) Put(ctx context.Context, objectPath string, body io.Reader, size int64) error {
	if objectPath == t.previewPath {
		t.attempted = true
		err := t.ObjectStorage.Put(ctx, objectPath, body, size)
		if err != nil {
			t.err = err
		}
		return err
	}
	return t.ObjectStorage.Put(ctx, objectPath, body, size)
}

func (s *Store) acquireUploadLock(ctx context.Context, requestID string) (func(), error) {
	s.uploadLockMu.Lock()
	lock := s.uploadLocks[requestID]
	if lock == nil {
		lock = &uploadRequestLock{ch: make(chan struct{}, 1)}
		s.uploadLocks[requestID] = lock
	}
	lock.refs++
	s.uploadLockMu.Unlock()

	select {
	case lock.ch <- struct{}{}:
		return func() { s.releaseUploadLock(requestID, lock) }, nil
	case <-ctx.Done():
		s.dropUploadLockRef(requestID, lock)
		return nil, ctx.Err()
	}
}

func (s *Store) releaseUploadLock(requestID string, lock *uploadRequestLock) {
	<-lock.ch
	s.dropUploadLockRef(requestID, lock)
}

func (s *Store) dropUploadLockRef(requestID string, lock *uploadRequestLock) {
	s.uploadLockMu.Lock()
	lock.refs--
	if lock.refs <= 0 {
		delete(s.uploadLocks, requestID)
	}
	s.uploadLockMu.Unlock()
}

func imagePreviewObjectPath(ownerID, mediaID string, createdAt time.Time) string {
	return fmt.Sprintf("/previews/%s/%s/%s.jpg", remoteOwnerSegment(ownerID), createdAt.UTC().Format("2006/01"), mediaID)
}

func (s *Store) setJobPreviewPath(ctx context.Context, jobID, previewPath string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE upload_jobs SET preview_path = ?, updated_at = ? WHERE id = ?`, previewPath, nowText(time.Now().UTC()), jobID)
	return err
}

func (s *Store) cleanupCompletedPreview(ctx context.Context, jobID, mediaID, previewPath string) {
	if previewPath == "" || s.objects == nil {
		return
	}
	stateCtx, stateCancel := context.WithTimeout(context.Background(), uploadStateTimeout)
	defer stateCancel()
	var ready int
	if err := s.db.QueryRowContext(stateCtx, `SELECT COUNT(*) FROM media m
		WHERE m.id = ? AND m.status = 'ready' AND m.object_path <> ? AND m.preview_path <> ?
		  AND NOT EXISTS (SELECT 1 FROM media_variants v WHERE v.object_path = ?)`, mediaID, previewPath, previewPath, previewPath).Scan(&ready); err != nil || ready != 1 {
		return
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), uploadCleanupTimeout)
	defer cleanupCancel()
	if err := s.objects.Delete(cleanupCtx, previewPath); err != nil && !errors.Is(err, storage.ErrNotFound) {
		// Keep the marker and path for the next startup maintenance run. The
		// ready original remains untouched in all cases.
		return
	}
	stateCtx, cancel := context.WithTimeout(context.Background(), uploadStateTimeout)
	defer cancel()
	_, _ = s.db.ExecContext(stateCtx, `UPDATE upload_jobs SET preview_path = '', error_code = '', updated_at = ? WHERE id = ? AND object_path <> ? AND state = 'completed' AND error_code = 'preview_cleanup_required' AND preview_path = ?`, nowText(time.Now().UTC()), jobID, previewPath, previewPath)
}

// failUploadPaths is deliberately best effort: the caller is already
// returning an upload error, so the durable job state records whether a later
// maintenance run must retry remote deletion. A ready media row wins over a
// stale failure, since its object is user-visible and must never be deleted.
func (s *Store) failUploadPaths(ctx context.Context, jobID, objectPath, previewPath, code string) {
	stateCtx, stateCancel := context.WithTimeout(context.Background(), uploadStateTimeout)
	defer stateCancel()
	ready, readyErr := s.readyMediaForObject(stateCtx, objectPath)
	if readyErr != nil {
		// A database error makes the commit state unknown. Never delete a remote
		// object while that state cannot be established.
		_ = s.setUploadJobStateForPath(stateCtx, jobID, objectPath, "cleanup_required", code, "pending", "uploading", "verifying", "cleanup_required")
		return
	}
	if ready {
		var mediaID, jobError, recordedPreview string
		if err := s.db.QueryRowContext(stateCtx, `SELECT m.id, u.error_code, u.preview_path
			FROM media m JOIN upload_jobs u ON u.id = ?
			WHERE m.object_path = ? AND m.status = 'ready'`, jobID, objectPath).Scan(&mediaID, &jobError, &recordedPreview); err == nil && jobError == "preview_cleanup_required" {
			if previewPath == "" {
				previewPath = recordedPreview
			}
			s.cleanupCompletedPreview(ctx, jobID, mediaID, previewPath)
			return
		}
		_ = s.setUploadJobStateForPath(stateCtx, jobID, objectPath, "completed", "", "pending", "uploading", "verifying", "cleanup_required")
		return
	}
	// Persist the cleanup obligation before touching remote storage. If the
	// process exits while Delete is blocked, startup can still reclaim quota.
	result, err := s.db.ExecContext(stateCtx, `UPDATE upload_jobs SET state = 'cleanup_required', error_code = ?, updated_at = ? WHERE id = ? AND object_path = ? AND state IN ('pending','uploading','verifying','cleanup_required')`, code, nowText(time.Now().UTC()), jobID, objectPath)
	if err != nil {
		return
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return
	}
	if previewPath == "" {
		_ = s.db.QueryRowContext(stateCtx, `SELECT preview_path FROM upload_jobs WHERE id = ?`, jobID).Scan(&previewPath)
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), uploadCleanupTimeout)
	defer cleanupCancel()
	if err := s.deleteUploadObjects(cleanupCtx, objectPath, previewPath); err != nil {
		stateCtx, cancel := context.WithTimeout(context.Background(), uploadStateTimeout)
		_ = s.setUploadJobStateForPath(stateCtx, jobID, objectPath, "cleanup_required", code, "cleanup_required")
		cancel()
		return
	}
	stateCtx, cancel := context.WithTimeout(context.Background(), uploadStateTimeout)
	_ = s.setUploadJobStateForPath(stateCtx, jobID, objectPath, "failed", code, "cleanup_required")
	cancel()
}

func (s *Store) readyMediaForObject(ctx context.Context, objectPath string) (bool, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM media WHERE object_path = ?`, objectPath).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return status == "ready", err
}

func (s *Store) setUploadJobStateForPath(ctx context.Context, jobID, objectPath, state, code string, from ...string) error {
	if len(from) == 0 {
		from = []string{"pending", "uploading", "verifying", "cleanup_required"}
	}
	placeholders := make([]byte, 0, len(from)*2)
	args := make([]any, 0, len(from)+5)
	for i, value := range from {
		if i > 0 {
			placeholders = append(placeholders, ',', ' ')
		}
		placeholders = append(placeholders, '?')
		args = append(args, value)
	}
	args = append([]any{state, code, nowText(time.Now().UTC()), jobID, objectPath}, args...)
	query := fmt.Sprintf(`UPDATE upload_jobs SET state = ?, error_code = ?, updated_at = ? WHERE id = ? AND object_path = ? AND state IN (%s)`, string(placeholders))
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *Store) cleanupUploadJob(ctx context.Context, jobID, objectPath, previewPath, expectedState string) (Record, bool, error) {
	stateCtx, stateCancel := context.WithTimeout(context.Background(), uploadStateTimeout)
	defer stateCancel()
	if expectedState != "" {
		// Claim active orphan states before deleting anything. This makes startup
		// cleanup safe against a retry which begins between the recovery query and
		// the remote delete.
		result, err := s.db.ExecContext(stateCtx, `UPDATE upload_jobs SET state = 'cleanup_required', updated_at = ? WHERE id = ? AND object_path = ? AND state = ?`, nowText(time.Now().UTC()), jobID, objectPath, expectedState)
		if err != nil {
			return Record{}, false, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return Record{}, false, nil
		}
	}
	if ready, err := s.readyMediaForObject(stateCtx, objectPath); err != nil {
		return Record{}, false, err
	} else if ready {
		record, err := s.recordByObjectPath(stateCtx, objectPath)
		if err != nil {
			return Record{}, false, err
		}
		_ = s.setUploadJobStateForPath(stateCtx, jobID, objectPath, "completed", "", "cleanup_required")
		return record, true, nil
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), uploadCleanupTimeout)
	defer cleanupCancel()
	if err := s.deleteUploadObjects(cleanupCtx, objectPath, previewPath); err != nil {
		stateCtx, cancel := context.WithTimeout(context.Background(), uploadStateTimeout)
		_ = s.setUploadJobStateForPath(stateCtx, jobID, objectPath, "cleanup_required", "cleanup_failed", "cleanup_required")
		cancel()
		return Record{}, false, fmt.Errorf("cleanup upload: %w", errors.Join(ErrStorageUnavailable, err))
	}
	stateCtx, cancel := context.WithTimeout(context.Background(), uploadStateTimeout)
	defer cancel()
	if _, err := s.db.ExecContext(stateCtx, `UPDATE upload_jobs SET state = 'cleaned', error_code = '', updated_at = ? WHERE id = ? AND object_path = ? AND state = 'cleanup_required'`, nowText(time.Now().UTC()), jobID, objectPath); err != nil {
		return Record{}, false, err
	}
	return Record{}, false, nil
}

func (s *Store) deleteUploadObjects(ctx context.Context, objectPath, previewPath string) error {
	if s.objects == nil {
		return ErrStorageUnavailable
	}
	var cleanupErr error
	for _, path := range []string{objectPath, previewPath} {
		if path == "" {
			continue
		}
		if err := s.objects.Delete(ctx, path); err != nil && !errors.Is(err, storage.ErrNotFound) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

// recoverSynchronousUploads handles only the small synchronous upload state
// machine. Video jobs are identified by media_processing_jobs and are left to
// resumeStagedVideos, which owns their staging checkpoints and cleanup policy.
func (s *Store) recoverSynchronousUploads(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE upload_jobs
		SET state = 'completed', error_code = '', updated_at = ?
		WHERE state IN ('pending','uploading','verifying','cleanup_required')
		  AND EXISTS (SELECT 1 FROM media m WHERE m.object_path = upload_jobs.object_path AND m.status = 'ready')
		  AND NOT EXISTS (SELECT 1 FROM media_processing_jobs p WHERE p.id = upload_jobs.id)`, nowText(time.Now().UTC()))
	if err != nil {
		return err
	}
	if err := s.recoverCompletedPreviewCleanups(ctx); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.state, u.object_path, u.preview_path
		FROM upload_jobs u
		LEFT JOIN media m ON m.object_path = u.object_path
		WHERE u.state IN ('pending','uploading','verifying','cleanup_required')
		  AND COALESCE(m.media_type, '') <> 'video'
		  AND NOT EXISTS (SELECT 1 FROM media_processing_jobs p WHERE p.id = u.id)
		  AND NOT EXISTS (SELECT 1 FROM media m_ready WHERE m_ready.object_path = u.object_path AND m_ready.status = 'ready')`)
	if err != nil {
		return err
	}
	type orphanUpload struct {
		jobID, state, objectPath, previewPath string
	}
	var orphans []orphanUpload
	for rows.Next() {
		var jobID, state, objectPath, previewPath string
		if err := rows.Scan(&jobID, &state, &objectPath, &previewPath); err != nil {
			rows.Close()
			return err
		}
		orphans = append(orphans, orphanUpload{jobID: jobID, state: state, objectPath: objectPath, previewPath: previewPath})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, orphan := range orphans {
		// Ignore individual storage failures so another orphan does not prevent
		// the rest of startup recovery. cleanupUploadJob persists the retryable
		// cleanup_required state in that case.
		_, _, _ = s.cleanupUploadJob(ctx, orphan.jobID, orphan.objectPath, orphan.previewPath, orphan.state)
	}
	return nil
}

func (s *Store) recoverCompletedPreviewCleanups(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, m.id, u.preview_path
		FROM upload_jobs u
		JOIN media m ON m.object_path = u.object_path AND m.status = 'ready'
		WHERE u.state = 'completed' AND u.error_code = 'preview_cleanup_required' AND u.preview_path <> ''
		  AND NOT EXISTS (SELECT 1 FROM media_processing_jobs p WHERE p.id = u.id)`)
	if err != nil {
		return err
	}
	type previewCleanup struct {
		jobID, mediaID, previewPath string
	}
	var cleanups []previewCleanup
	for rows.Next() {
		var item previewCleanup
		if err := rows.Scan(&item.jobID, &item.mediaID, &item.previewPath); err != nil {
			rows.Close()
			return err
		}
		cleanups = append(cleanups, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range cleanups {
		s.cleanupCompletedPreview(ctx, item.jobID, item.mediaID, item.previewPath)
	}
	return nil
}
