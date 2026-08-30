package media

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"dorm-memorial/internal/identity"
	"dorm-memorial/internal/storage"
)

const MaxFileSize int64 = 8 << 30

var (
	ErrNotFound           = errors.New("media not found")
	ErrForbidden          = errors.New("media access forbidden")
	ErrInvalid            = errors.New("invalid media upload")
	ErrConflict           = errors.New("upload request conflict")
	ErrConfirmationNeeded = errors.New("media purge confirmation required")
	ErrQuotaExceeded      = errors.New("media quota exceeded")
	ErrStorageUnavailable = errors.New("media storage unavailable")
	ErrPreviewPending     = errors.New("media preview is being prepared")
	requestIDPattern      = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,96}$`)
)

type Record struct {
	ID               string    `json:"id"`
	OwnerID          string    `json:"owner_id"`
	OriginalFilename string    `json:"original_filename"`
	MediaType        string    `json:"media_type"`
	MimeType         string    `json:"mime_type"`
	SizeBytes        int64     `json:"size_bytes"`
	SHA256           string    `json:"sha256"`
	Status           string    `json:"status"`
	Width            *int      `json:"width"`
	Height           *int      `json:"height"`
	DurationMS       *int64    `json:"duration_ms"`
	HasPreview       bool      `json:"has_preview"`
	CreatedAt        time.Time `json:"created_at"`
}

type AdminRecord struct {
	Record
	OwnerUsername  string `json:"owner_username"`
	OwnerNickname  string `json:"owner_nickname"`
	ReferenceCount int    `json:"reference_count"`
	Withdrawn      bool   `json:"withdrawn"`
}

type UploadInput struct {
	ClientRequestID string
	Filename        string
	MimeType        string
	Size            int64
	Body            io.Reader
	IPAddress       string
	Width           int
	Height          int
	DurationMS      int64
}

type Usage struct {
	UsedBytes     int64 `json:"used_bytes"`
	ReservedBytes int64 `json:"reserved_bytes"`
	QuotaBytes    int64 `json:"quota_bytes"`
}

type Content struct {
	Body          io.ReadCloser
	StatusCode    int
	MimeType      string
	Filename      string
	ContentLength int64
	ContentRange  string
	AcceptRanges  string
}

type ContentDescriptor struct {
	ID           string
	OwnerID      string
	ObjectPath   string
	PreviewPath  string
	Filename     string
	MediaType    string
	MimeType     string
	Status       string
	CreatedText  string
	Size         int64
	DurationMS   sql.NullInt64
	DisplayPath  string
	DisplayMIME  string
	DisplaySize  int64
	PlaybackPath string
	PlaybackMIME string
	PlaybackSize int64
}

type Store struct {
	db              *sql.DB
	objects         storage.ObjectStorage
	ffmpegPath      string
	stagingDir      string
	videoEncoder    string
	verifyDelays    []time.Duration
	jobMu           sync.Mutex
	stageMu         sync.Mutex
	background      map[string]bool
	videoTranscodes chan struct{}
	imageRenders    chan struct{}
	imageDisplays   map[string]*imageDisplayJob
	uploadLockMu    sync.Mutex
	uploadLocks     map[string]*uploadRequestLock
}

func NewStore(db *sql.DB, objects storage.ObjectStorage, ffmpegPaths ...string) *Store {
	ffmpegPath := "ffmpeg"
	if len(ffmpegPaths) > 0 && strings.TrimSpace(ffmpegPaths[0]) != "" {
		ffmpegPath = strings.TrimSpace(ffmpegPaths[0])
	}
	return &Store{db: db, objects: objects, ffmpegPath: ffmpegPath, stagingDir: "data/media-staging", videoEncoder: "auto", verifyDelays: []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second}, background: make(map[string]bool), videoTranscodes: make(chan struct{}, 1), imageRenders: make(chan struct{}, 2), imageDisplays: make(map[string]*imageDisplayJob), uploadLocks: make(map[string]*uploadRequestLock)}
}

func (s *Store) ConfigureVideoProcessing(stagingDir, encoder string) {
	if strings.TrimSpace(stagingDir) != "" {
		s.stagingDir = strings.TrimSpace(stagingDir)
	}
	if strings.TrimSpace(encoder) != "" {
		s.videoEncoder = strings.ToLower(strings.TrimSpace(encoder))
	}
}

// StartMaintenance queues inexpensive missing video posters. Legacy playback
// renditions are generated lazily when a browser actually needs one, so a
// startup backlog cannot make the video a member clicked wait behind every
// historical upload.
func (s *Store) StartMaintenance(ctx context.Context) error {
	if s.objects == nil {
		return nil
	}
	if err := s.recoverSynchronousUploads(ctx); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.owner_id, m.object_path, m.preview_path, m.original_filename, m.mime_type, m.size_bytes, m.created_at, m.duration_ms
		FROM media m WHERE m.media_type = 'video' AND m.status = 'ready'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var descriptor ContentDescriptor
		if err := rows.Scan(&descriptor.ID, &descriptor.OwnerID, &descriptor.ObjectPath, &descriptor.PreviewPath, &descriptor.Filename, &descriptor.MimeType, &descriptor.Size, &descriptor.CreatedText, &descriptor.DurationMS); err != nil {
			return err
		}
		descriptor.MediaType = "video"
		if descriptor.PreviewPath == "" {
			s.scheduleVideoPreview(descriptor)
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return rowErr
	}
	return s.resumeStagedVideos(ctx)
}

func (s *Store) Upload(ctx context.Context, actor identity.User, input UploadInput) (Record, error) {
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	input.Filename = cleanFilename(input.Filename)
	input.MimeType = strings.ToLower(strings.TrimSpace(strings.Split(input.MimeType, ";")[0]))
	mediaType, ext, err := validateUpload(input)
	if err != nil {
		return Record{}, err
	}
	if s.objects == nil {
		return Record{}, ErrStorageUnavailable
	}
	// A request ID is a reservation key. Lock only this key so an unrelated
	// upload can proceed while a large image is being written to remote storage;
	// reserveUpload's conditional state transitions cover other Store instances.
	release, err := s.acquireUploadLock(ctx, actor.ID+"\x00"+input.ClientRequestID)
	if err != nil {
		return Record{}, err
	}
	defer release()

	var stagedCleanup func()
	stagedCleanup = func() {}
	defer func() { stagedCleanup() }()
	requestFingerprint := uploadMetadataFingerprint(input)
	if mediaType == "image" {
		prepared, cleanup, err := prepareImageUpload(ctx, input)
		if err != nil {
			return Record{}, err
		}
		stagedCleanup = cleanup
		input = prepared
		mediaType, ext, err = validateUpload(input)
		if err != nil {
			return Record{}, err
		}
		requestFingerprint, err = imageUploadFingerprint(input)
		if err != nil {
			return Record{}, fmt.Errorf("fingerprint image upload: %w", errors.Join(ErrInvalid, err))
		}
	}

	if existing, ok, err := s.existingRequest(ctx, actor.ID, input.ClientRequestID, requestFingerprint); err != nil {
		return Record{}, err
	} else if ok {
		return existing, nil
	}
	if mediaType == "video" && ext == ".mp4" {
		prepared, cleanup, err := prepareMP4Upload(ctx, s.ffmpegPath, input)
		if err != nil {
			return Record{}, err
		}
		defer cleanup()
		input = prepared
	}

	mediaID := newID()
	createdAt := time.Now().UTC()
	objectPath := fmt.Sprintf("/originals/%s/%s/%s%s", remoteOwnerSegment(actor.ID), createdAt.Format("2006/01"), mediaID, ext)
	jobID := newID()
	effectiveJobID, err := s.reserveUpload(ctx, actor.ID, jobID, input.ClientRequestID, objectPath, input.Size, requestFingerprint)
	if err != nil {
		return Record{}, err
	}
	jobID = effectiveJobID
	s.setJobState(ctx, jobID, "uploading", "")
	previewPath := ""
	cleanupPreviewPath := ""
	if mediaType == "image" {
		cleanupPreviewPath = imagePreviewObjectPath(actor.ID, mediaID, createdAt)
		if err := s.setJobPreviewPath(context.WithoutCancel(ctx), jobID, cleanupPreviewPath); err != nil {
			s.failUploadPaths(ctx, jobID, objectPath, cleanupPreviewPath, "database_failed")
			return Record{}, err
		}
	}

	hasher := sha256.New()
	if err := s.objects.Put(ctx, objectPath, io.TeeReader(input.Body, hasher), input.Size); err != nil {
		s.failUploadPaths(ctx, jobID, objectPath, cleanupPreviewPath, "storage_write_failed")
		return Record{}, fmt.Errorf("upload object: %w", errors.Join(ErrStorageUnavailable, err))
	}
	s.setJobState(context.WithoutCancel(ctx), jobID, "verifying", "")
	if err := s.verifySize(ctx, objectPath, input.Size); err != nil {
		s.failUploadPaths(ctx, jobID, objectPath, cleanupPreviewPath, "storage_verify_failed")
		return Record{}, fmt.Errorf("verify uploaded object: %w", ErrStorageUnavailable)
	}
	if mediaType == "image" {
		if err := verifyImageIntegrity(ctx, s.objects, objectPath, input.Size, hex.EncodeToString(hasher.Sum(nil))); err != nil {
			s.failUploadPaths(ctx, jobID, objectPath, cleanupPreviewPath, "storage_verify_failed")
			return Record{}, fmt.Errorf("verify uploaded image: %w", errors.Join(ErrStorageUnavailable, err))
		}
	}

	now := createdAt
	imageInfo := imageDetails{}
	previewCleanupRequired := false
	if mediaType == "image" {
		previewTracker := &previewPutTracker{ObjectStorage: s.objects, previewPath: cleanupPreviewPath}
		imageInfo = buildImagePreview(ctx, previewTracker, objectPath, actor.ID, mediaID, now)
		if imageInfo.width <= 0 || imageInfo.height <= 0 {
			// The staged original was already decoded and measured. A transient
			// preview read failure must not discard its authoritative dimensions.
			imageInfo.width, imageInfo.height = input.Width, input.Height
		}
		previewCleanupRequired = previewTracker.attempted && previewTracker.err != nil
	}
	previewPath = imageInfo.previewPath
	if mediaType == "video" {
		previewPath = buildVideoPreview(ctx, s.objects, s.ffmpegPath, objectPath, actor.ID, mediaID, now, input.DurationMS)
	}
	record := Record{
		ID: mediaID, OwnerID: actor.ID, OriginalFilename: input.Filename, MediaType: mediaType,
		MimeType: input.MimeType, SizeBytes: input.Size, SHA256: hex.EncodeToString(hasher.Sum(nil)), Status: "ready", CreatedAt: now,
	}
	if mediaType == "video" && input.Width > 0 && input.Width <= 16384 && input.Height > 0 && input.Height <= 16384 {
		record.Width = &input.Width
		record.Height = &input.Height
	}
	if mediaType == "video" && input.DurationMS > 0 && input.DurationMS <= int64((48*time.Hour)/time.Millisecond) {
		record.DurationMS = &input.DurationMS
	}
	if mediaType == "audio" && input.DurationMS > 0 && input.DurationMS <= int64((48*time.Hour)/time.Millisecond) {
		record.DurationMS = &input.DurationMS
	}
	if imageInfo.width > 0 {
		record.Width = &imageInfo.width
		record.Height = &imageInfo.height
	}
	record.HasPreview = previewPath != ""
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.failUploadPaths(ctx, jobID, objectPath, cleanupPreviewPath, "database_failed")
		return Record{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO media(id, owner_id, object_path, preview_path, original_filename, media_type, mime_type, size_bytes, sha256, width, height, duration_ms, status, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'ready', ?, ?)`, mediaID, actor.ID, objectPath, previewPath, input.Filename, mediaType, input.MimeType, input.Size, record.SHA256, nullableInt(record.Width), nullableInt(record.Height), nullableInt64(record.DurationMS), nowText(now), nowText(now)); err != nil {
		_ = tx.Rollback()
		s.failUploadPaths(ctx, jobID, objectPath, cleanupPreviewPath, "database_failed")
		return Record{}, err
	}
	jobErrorCode := ""
	if previewCleanupRequired {
		jobErrorCode = "preview_cleanup_required"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE upload_jobs SET state = 'completed', error_code = ?, updated_at = ? WHERE id = ?`, jobErrorCode, nowText(now), jobID); err != nil {
		_ = tx.Rollback()
		s.failUploadPaths(ctx, jobID, objectPath, cleanupPreviewPath, "database_failed")
		return Record{}, err
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, metadata_json, ip_address, created_at)
		VALUES(?, 'media.upload', 'media', ?, ?, ?, ?)`, actor.ID, mediaID, fmt.Sprintf(`{"size_bytes":%d}`, input.Size), input.IPAddress, nowText(now))
	if err := tx.Commit(); err != nil {
		s.failUploadPaths(ctx, jobID, objectPath, cleanupPreviewPath, "database_failed")
		return Record{}, err
	}
	if previewCleanupRequired {
		// The original and its media row are already durable. Clean only the
		// partially written derived object; failure is retained for maintenance.
		s.cleanupCompletedPreview(ctx, jobID, mediaID, cleanupPreviewPath)
	}
	if mediaType == "video" {
		s.scheduleVideoPlayback(ContentDescriptor{ID: mediaID, OwnerID: actor.ID, ObjectPath: objectPath, Filename: input.Filename, MediaType: mediaType, MimeType: input.MimeType, Size: input.Size, CreatedText: nowText(now), DurationMS: sql.NullInt64{Int64: input.DurationMS, Valid: input.DurationMS > 0}})
	}
	return record, nil
}

// remoteOwnerSegment returns a deterministic, opaque directory name that is
// accepted by storage providers with a 16-character folder-name limit. Media
// rows retain their complete object paths, so existing uploads remain readable.
func remoteOwnerSegment(ownerID string) string {
	sum := sha256.Sum256([]byte(ownerID))
	return hex.EncodeToString(sum[:8])
}

func (s *Store) verifySize(ctx context.Context, objectPath string, expected int64) error {
	var lastErr error
	for _, delay := range s.verifyDelays {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		if refresher, ok := s.objects.(storage.DirectoryRefresher); ok {
			if err := refresher.RefreshDirectory(ctx, pathpkg.Dir(objectPath)); err != nil {
				lastErr = err
				continue
			}
		}
		info, err := s.objects.Stat(ctx, objectPath)
		if err == nil && info.Size == expected {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("size is %d, expected %d", info.Size, expected)
		}
	}
	return lastErr
}

func (s *Store) Usage(ctx context.Context, userID string) (Usage, error) {
	var result Usage
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT SUM(size_bytes) FROM media WHERE owner_id = ? AND status = 'ready'), 0),
		COALESCE((SELECT SUM(expected_size) FROM upload_jobs WHERE user_id = ? AND state IN ('pending','uploading','verifying')), 0),
		media_quota_bytes FROM users WHERE id = ?`, userID, userID, userID).Scan(&result.UsedBytes, &result.ReservedBytes, &result.QuotaBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return Usage{}, ErrNotFound
	}
	return result, err
}

func (s *Store) ListAdmin(ctx context.Context, actor identity.User, search, mediaType, status string, limit int) ([]AdminRecord, error) {
	if actor.Role != "admin" {
		return nil, ErrForbidden
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	const withdrawnExpression = `(m.status = 'deleted' OR (
		EXISTS(SELECT 1 FROM post_media withdrawn_pm JOIN posts withdrawn_post ON withdrawn_post.id = withdrawn_pm.post_id WHERE withdrawn_pm.media_id = m.id AND withdrawn_post.status = 'deleted')
		AND NOT EXISTS(SELECT 1 FROM post_media active_pm JOIN posts active_post ON active_post.id = active_pm.post_id WHERE active_pm.media_id = m.id AND active_post.status <> 'deleted')
		AND NOT EXISTS(SELECT 1 FROM guestbook_media withdrawn_gm WHERE withdrawn_gm.media_id = m.id)
		AND NOT EXISTS(SELECT 1 FROM message_media withdrawn_mm WHERE withdrawn_mm.media_id = m.id)
		AND NOT EXISTS(SELECT 1 FROM profiles withdrawn_profile WHERE withdrawn_profile.avatar_path = m.id)
	))`
	query := `SELECT m.id, m.owner_id, m.original_filename, m.media_type, m.mime_type, m.size_bytes, m.sha256, m.status,
		m.width, m.height, m.duration_ms, m.preview_path <> '', m.created_at, u.username, p.nickname,
		(SELECT COUNT(*) FROM post_media pm WHERE pm.media_id = m.id)
		+ (SELECT COUNT(*) FROM guestbook_media gm WHERE gm.media_id = m.id)
		+ (SELECT COUNT(*) FROM message_media mm WHERE mm.media_id = m.id)
		+ (SELECT COUNT(*) FROM profiles profile WHERE profile.avatar_path = m.id), ` + withdrawnExpression + `
		FROM media m JOIN users u ON u.id = m.owner_id JOIN profiles p ON p.user_id = u.id WHERE 1 = 1`
	args := []any{}
	if search = strings.TrimSpace(search); search != "" {
		needle := "%" + search + "%"
		query += ` AND (m.original_filename LIKE ? COLLATE NOCASE OR u.username LIKE ? COLLATE NOCASE OR p.nickname LIKE ? COLLATE NOCASE)`
		args = append(args, needle, needle, needle)
	}
	if mediaType == "image" || mediaType == "video" || mediaType == "audio" {
		query += ` AND m.media_type = ?`
		args = append(args, mediaType)
	}
	if status == "deleted" {
		query += ` AND ` + withdrawnExpression
	} else if status == "ready" {
		query += ` AND m.status = 'ready' AND NOT ` + withdrawnExpression
	} else if status == "uploading" || status == "unavailable" {
		query += ` AND m.status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY m.created_at DESC, m.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AdminRecord{}
	for rows.Next() {
		var item AdminRecord
		var width, height, duration sql.NullInt64
		var created string
		if err := rows.Scan(&item.ID, &item.OwnerID, &item.OriginalFilename, &item.MediaType, &item.MimeType, &item.SizeBytes, &item.SHA256, &item.Status, &width, &height, &duration, &item.HasPreview, &created, &item.OwnerUsername, &item.OwnerNickname, &item.ReferenceCount, &item.Withdrawn); err != nil {
			return nil, err
		}
		if width.Valid {
			value := int(width.Int64)
			item.Width = &value
		}
		if height.Valid {
			value := int(height.Int64)
			item.Height = &value
		}
		if duration.Valid {
			value := duration.Int64
			item.DurationMS = &value
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Delete(ctx context.Context, actor identity.User, id, ip string) error {
	release, err := s.acquireUploadLock(ctx, "media-lifecycle\x00"+id)
	if err != nil {
		return err
	}
	defer release()
	var ownerID, objectPath, previewPath, status string
	var attached int
	err = s.db.QueryRowContext(ctx, `SELECT m.owner_id, m.object_path, m.preview_path, m.status,
		EXISTS(SELECT 1 FROM post_media pm WHERE pm.media_id = m.id)
		OR EXISTS(SELECT 1 FROM guestbook_media gm WHERE gm.media_id = m.id)
		OR EXISTS(SELECT 1 FROM message_media mm WHERE mm.media_id = m.id)
		OR EXISTS(SELECT 1 FROM profiles p WHERE p.avatar_path = m.id)
		FROM media m WHERE m.id = ?`, id).Scan(&ownerID, &objectPath, &previewPath, &status, &attached)
	if errors.Is(err, sql.ErrNoRows) || status == "deleted" {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if ownerID != actor.ID && actor.Role != "admin" {
		return ErrForbidden
	}
	if attached != 0 {
		return ErrConflict
	}
	if s.objects == nil {
		return ErrStorageUnavailable
	}
	if err := s.objects.Delete(ctx, objectPath); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("delete object: %w", errors.Join(ErrStorageUnavailable, err))
	}
	if previewPath != "" {
		if err := s.objects.Delete(ctx, previewPath); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("delete preview object: %w", errors.Join(ErrStorageUnavailable, err))
		}
	}
	if err := s.deleteVariantObjects(ctx, id); err != nil {
		return err
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE media SET status = 'deleted', deleted_at = ?, updated_at = ? WHERE id = ? AND status <> 'deleted'`, nowText(now), nowText(now), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'media.delete', 'media', ?, ?, ?)`, actor.ID, id, ip, nowText(now))
	return nil
}

// Purge permanently removes an administrator-selected media record and all of
// its references. Referenced media that is still active requires an explicit
// force flag so an accidental request cannot silently remove displayed files.
func (s *Store) Purge(ctx context.Context, actor identity.User, id string, force bool, ip string) error {
	release, err := s.acquireUploadLock(ctx, "media-lifecycle\x00"+id)
	if err != nil {
		return err
	}
	defer release()
	if actor.Role != "admin" {
		return ErrForbidden
	}
	var objectPath, previewPath, status string
	var attached int
	err = s.db.QueryRowContext(ctx, `SELECT m.object_path, m.preview_path, m.status,
		EXISTS(SELECT 1 FROM post_media pm WHERE pm.media_id = m.id)
		OR EXISTS(SELECT 1 FROM guestbook_media gm WHERE gm.media_id = m.id)
		OR EXISTS(SELECT 1 FROM message_media mm WHERE mm.media_id = m.id)
		OR EXISTS(SELECT 1 FROM profiles p WHERE p.avatar_path = m.id)
		FROM media m WHERE m.id = ?`, id).Scan(&objectPath, &previewPath, &status, &attached)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "deleted" && attached != 0 && !force {
		return ErrConfirmationNeeded
	}
	if status != "deleted" {
		if s.objects == nil {
			return ErrStorageUnavailable
		}
		if err := s.objects.Delete(ctx, objectPath); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("purge object: %w", errors.Join(ErrStorageUnavailable, err))
		}
		if previewPath != "" {
			if err := s.objects.Delete(ctx, previewPath); err != nil && !errors.Is(err, storage.ErrNotFound) {
				return fmt.Errorf("purge preview object: %w", errors.Join(ErrStorageUnavailable, err))
			}
		}
		if err := s.deleteVariantObjects(ctx, id); err != nil {
			return err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`DELETE FROM post_media WHERE media_id = ?`,
		`DELETE FROM guestbook_media WHERE media_id = ?`,
		`DELETE FROM message_media WHERE media_id = ?`,
		`UPDATE profiles SET avatar_path = '' WHERE avatar_path = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, id); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM media WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'media.purge', 'media', ?, ?, ?)`, actor.ID, id, ip, nowText(time.Now().UTC())); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) deleteVariantObjects(ctx context.Context, mediaID string) error {
	// A compatible original may also be the playback rendition. The caller
	// already deletes the original and poster, so delete each distinct extra
	// object only once.
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT v.object_path FROM media_variants v JOIN media m ON m.id = v.media_id
		WHERE v.media_id = ? AND v.object_path <> m.object_path AND v.object_path <> m.preview_path`, mediaID)
	if err != nil {
		return err
	}
	var paths []string
	for rows.Next() {
		var objectPath string
		if err := rows.Scan(&objectPath); err != nil {
			rows.Close()
			return err
		}
		paths = append(paths, objectPath)
	}
	err = rows.Close()
	if err != nil {
		return err
	}
	for _, objectPath := range paths {
		if err := s.objects.Delete(ctx, objectPath); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("delete media variant: %w", errors.Join(ErrStorageUnavailable, err))
		}
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM media_variants WHERE media_id = ?`, mediaID)
	return err
}

func (s *Store) InspectContent(ctx context.Context, actor identity.User, id string) (ContentDescriptor, error) {
	defer storage.Time(ctx, "media_db")()
	var ownerID, objectPath, previewPath, filename, mediaType, mimeType, status, createdText string
	var size, displaySize, playbackSize int64
	var displayPath, displayMIME, playbackPath, playbackMIME string
	var durationMS sql.NullInt64
	var generallyReadable, messageAttached, messageReadable int
	err := s.db.QueryRowContext(ctx, `SELECT m.owner_id, m.object_path, m.preview_path, m.original_filename, m.media_type, m.mime_type, m.size_bytes, m.status, m.created_at, m.duration_ms,
		COALESCE((SELECT object_path FROM media_variants WHERE media_id = m.id AND kind = 'display'), ''),
		COALESCE((SELECT mime_type FROM media_variants WHERE media_id = m.id AND kind = 'display'), ''),
		COALESCE((SELECT size_bytes FROM media_variants WHERE media_id = m.id AND kind = 'display'), 0),
		COALESCE((SELECT object_path FROM media_variants WHERE media_id = m.id AND kind = 'playback'), ''),
		COALESCE((SELECT mime_type FROM media_variants WHERE media_id = m.id AND kind = 'playback'), ''),
		COALESCE((SELECT size_bytes FROM media_variants WHERE media_id = m.id AND kind = 'playback'), 0),
		EXISTS(SELECT 1 FROM post_media pm JOIN posts p ON p.id = pm.post_id WHERE pm.media_id = m.id AND p.status = 'published' AND p.visibility = 'members')
		OR EXISTS(SELECT 1 FROM guestbook_media gm JOIN guestbook_entries g ON g.id = gm.entry_id WHERE gm.media_id = m.id AND g.status = 'visible')
		OR EXISTS(SELECT 1 FROM guestbook_media gm JOIN guestbook_entries g ON g.id = gm.entry_id WHERE gm.media_id = m.id AND g.status = 'hidden' AND g.recipient_id = ?)
		OR EXISTS(SELECT 1 FROM profiles profile WHERE profile.avatar_path = m.id),
		EXISTS(SELECT 1 FROM message_media mm WHERE mm.media_id = m.id),
		EXISTS(SELECT 1 FROM message_media mm JOIN messages msg ON msg.id = mm.message_id
			JOIN conversation_members cm ON cm.conversation_id = msg.conversation_id
			WHERE mm.media_id = m.id AND cm.user_id = ?)
		FROM media m WHERE m.id = ?`, actor.ID, actor.ID, id).Scan(&ownerID, &objectPath, &previewPath, &filename, &mediaType, &mimeType, &size, &status, &createdText, &durationMS,
		&displayPath, &displayMIME, &displaySize, &playbackPath, &playbackMIME, &playbackSize, &generallyReadable, &messageAttached, &messageReadable)
	if errors.Is(err, sql.ErrNoRows) || status == "deleted" {
		return ContentDescriptor{}, ErrNotFound
	}
	if err != nil {
		return ContentDescriptor{}, err
	}
	if messageAttached != 0 && generallyReadable == 0 && messageReadable == 0 {
		return ContentDescriptor{}, ErrForbidden
	}
	if actor.Role != "admin" && actor.ID != ownerID && generallyReadable == 0 && messageReadable == 0 {
		return ContentDescriptor{}, ErrForbidden
	}
	if status != "ready" || s.objects == nil {
		return ContentDescriptor{}, ErrStorageUnavailable
	}
	return ContentDescriptor{ID: id, OwnerID: ownerID, ObjectPath: objectPath, PreviewPath: previewPath, Filename: filename, MediaType: mediaType, MimeType: mimeType, Status: status, CreatedText: createdText, Size: size, DurationMS: durationMS,
		DisplayPath: displayPath, DisplayMIME: displayMIME, DisplaySize: displaySize, PlaybackPath: playbackPath, PlaybackMIME: playbackMIME, PlaybackSize: playbackSize}, nil
}

func (s *Store) OpenDescriptor(ctx context.Context, descriptor ContentDescriptor, byteRange, variant string) (Content, error) {
	defer storage.Time(ctx, "media_open")()
	objectPath, previewPath := descriptor.ObjectPath, descriptor.PreviewPath
	filename, mediaType, mimeType := descriptor.Filename, descriptor.MediaType, descriptor.MimeType
	size := descriptor.Size
	if variant == "preview" && previewPath == "" && mediaType == "video" {
		s.scheduleVideoPreview(descriptor)
		return Content{}, ErrPreviewPending
	}
	if variant == "preview" && previewPath != "" {
		body, err := s.objects.Open(ctx, previewPath)
		if err == nil {
			return Content{Body: body, StatusCode: http.StatusOK, MimeType: "image/jpeg", Filename: filename, ContentLength: -1}, nil
		}
		// AList-backed drivers can acknowledge a newly written preview before its
		// raw URL becomes readable. The original remains a valid display source,
		// so a transient preview failure must not leave avatars or cards broken.
	}
	if variant == "preview" && mediaType == "video" {
		return Content{}, ErrStorageUnavailable
	}
	if variant == "playback" && mediaType == "video" {
		if descriptor.PlaybackPath == "" {
			s.scheduleVideoPlayback(descriptor)
			return Content{}, ErrPreviewPending
		}
		objectPath, mimeType, size = descriptor.PlaybackPath, descriptor.PlaybackMIME, descriptor.PlaybackSize
	}
	if variant == "display" && mediaType == "image" {
		return s.openDeliveredImageDisplay(ctx, descriptor, byteRange)
	}
	if variant == "playback" && mediaType != "video" || variant == "display" && mediaType != "image" {
		return Content{}, ErrInvalid
	}
	if byteRange != "" {
		if ranged, ok := s.objects.(storage.RangeStorage); ok {
			response, err := ranged.OpenRange(ctx, objectPath, byteRange)
			if err != nil {
				return Content{}, fmt.Errorf("open ranged object: %w", errors.Join(ErrStorageUnavailable, err))
			}
			return Content{Body: response.Body, StatusCode: response.StatusCode, MimeType: mimeType, Filename: filename, ContentLength: response.ContentLength, ContentRange: response.Header.Get("Content-Range"), AcceptRanges: response.Header.Get("Accept-Ranges")}, nil
		}
	}
	body, err := s.objects.Open(ctx, objectPath)
	if err != nil {
		return Content{}, fmt.Errorf("open object: %w", errors.Join(ErrStorageUnavailable, err))
	}
	return Content{Body: body, StatusCode: http.StatusOK, MimeType: mimeType, Filename: filename, ContentLength: size, AcceptRanges: "bytes"}, nil
}

func (s *Store) scheduleVideoPreview(descriptor ContentDescriptor) {
	s.runBackground("preview:"+descriptor.ID, func(ctx context.Context) {
		createdAt, _ := time.Parse(time.RFC3339Nano, descriptor.CreatedText)
		previewPath := buildVideoPreview(ctx, s.objects, s.ffmpegPath, descriptor.ObjectPath, descriptor.OwnerID, descriptor.ID, createdAt, descriptor.DurationMS.Int64)
		if previewPath != "" {
			_, _ = s.db.ExecContext(ctx, `UPDATE media SET preview_path = ?, updated_at = ? WHERE id = ? AND preview_path = ''`, previewPath, nowText(time.Now().UTC()), descriptor.ID)
		}
	})
}

func (s *Store) scheduleVideoPlayback(descriptor ContentDescriptor) {
	s.runBackground("playback:"+descriptor.ID, func(ctx context.Context) {
		select {
		case s.videoTranscodes <- struct{}{}:
			defer func() { <-s.videoTranscodes }()
		case <-ctx.Done():
			return
		}
		path, mimeType, size := buildVideoPlayback(ctx, s.objects, s.ffmpegPath, descriptor.ObjectPath, descriptor.OwnerID, descriptor.ID, descriptor.CreatedText, s.videoEncoder)
		if path == "" || size <= 0 {
			return
		}
		now := nowText(time.Now().UTC())
		_, _ = s.db.ExecContext(ctx, `INSERT INTO media_variants(media_id, kind, object_path, mime_type, size_bytes, created_at, updated_at)
			VALUES(?, 'playback', ?, ?, ?, ?, ?)
			ON CONFLICT(media_id, kind) DO UPDATE SET object_path = excluded.object_path, mime_type = excluded.mime_type, size_bytes = excluded.size_bytes, updated_at = excluded.updated_at`,
			descriptor.ID, path, mimeType, size, now, now)
	})
}

func (s *Store) runBackground(key string, job func(context.Context)) {
	s.jobMu.Lock()
	if s.background[key] {
		s.jobMu.Unlock()
		return
	}
	s.background[key] = true
	s.jobMu.Unlock()
	go func() {
		defer func() {
			s.jobMu.Lock()
			delete(s.background, key)
			s.jobMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		job(ctx)
	}()
}

func (s *Store) OpenContent(ctx context.Context, actor identity.User, id, byteRange string, preview bool) (Content, error) {
	descriptor, err := s.InspectContent(ctx, actor, id)
	if err != nil {
		return Content{}, err
	}
	variant := "original"
	if preview {
		variant = "preview"
	}
	return s.OpenDescriptor(ctx, descriptor, byteRange, variant)
}

func (s *Store) existingRequest(ctx context.Context, userID, requestID string, fingerprints ...string) (Record, bool, error) {
	fingerprint := ""
	if len(fingerprints) > 0 {
		fingerprint = fingerprints[0]
	}
	var jobID, state, objectPath, previewPath, storedFingerprint string
	var hasProcessing int
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.state, u.object_path, u.preview_path, u.request_fingerprint,
		EXISTS (SELECT 1 FROM media_processing_jobs p WHERE p.id = u.id)
		FROM upload_jobs u WHERE u.user_id = ? AND u.client_request_id = ?`, userID, requestID).Scan(&jobID, &state, &objectPath, &previewPath, &storedFingerprint, &hasProcessing)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	if hasProcessing != 0 {
		return Record{}, false, ErrConflict
	}
	if storedFingerprint != "" && fingerprint != "" && storedFingerprint != fingerprint {
		return Record{}, false, ErrConflict
	}
	switch state {
	case "completed":
		record, err := s.recordByObjectPath(ctx, objectPath)
		if err != nil {
			return Record{}, false, err
		}
		if record.Status != "ready" {
			return Record{}, false, ErrConflict
		}
		return record, true, nil
	case "pending", "uploading", "verifying":
		return Record{}, false, ErrConflict
	case "cleanup_required":
		// A retry must finish cleanup before it can claim this request again.
		if record, recovered, err := s.cleanupUploadJob(ctx, jobID, objectPath, previewPath, "cleanup_required"); err != nil {
			return Record{}, false, err
		} else if recovered {
			return record, true, nil
		}
	case "failed", "cleaned":
		return Record{}, false, nil
	default:
		return Record{}, false, ErrConflict
	}
	return Record{}, false, nil
}

func (s *Store) recordByObjectPath(ctx context.Context, objectPath string) (Record, error) {
	var record Record
	var created string
	var width, height, duration sql.NullInt64
	var previewPath string
	err := s.db.QueryRowContext(ctx, `SELECT id, owner_id, original_filename, media_type, mime_type, size_bytes, sha256, status, created_at, width, height, duration_ms, preview_path FROM media WHERE object_path = ?`, objectPath).
		Scan(&record.ID, &record.OwnerID, &record.OriginalFilename, &record.MediaType, &record.MimeType, &record.SizeBytes, &record.SHA256, &record.Status, &created, &width, &height, &duration, &previewPath)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if width.Valid {
		value := int(width.Int64)
		record.Width = &value
	}
	if height.Valid {
		value := int(height.Int64)
		record.Height = &value
	}
	if duration.Valid {
		value := duration.Int64
		record.DurationMS = &value
	}
	record.HasPreview = previewPath != ""
	return record, nil
}

func (s *Store) reserve(ctx context.Context, userID, jobID, requestID, objectPath string, size int64) error {
	_, err := s.reserveUpload(ctx, userID, jobID, requestID, objectPath, size, "")
	return err
}

// reserveUpload either creates a reservation or atomically reclaims a
// terminal failed/cleaned reservation for a retry. Active requests remain
// locked behind ErrConflict, preventing two clients from writing the same
// request ID concurrently.
func (s *Store) reserveUpload(ctx context.Context, userID, jobID, requestID, objectPath string, size int64, fingerprint string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var existingID, state, storedFingerprint string
	var hasProcessing int
	err = tx.QueryRowContext(ctx, `SELECT u.id, u.state, u.request_fingerprint,
		EXISTS (SELECT 1 FROM media_processing_jobs p WHERE p.id = u.id)
		FROM upload_jobs u WHERE u.user_id = ? AND u.client_request_id = ?`, userID, requestID).Scan(&existingID, &state, &storedFingerprint, &hasProcessing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil {
		if hasProcessing != 0 {
			// Staged videos own their upload_jobs row and checkpoint lifecycle;
			// synchronous image retries must never reuse that primary key.
			return "", ErrConflict
		}
		if storedFingerprint != "" && fingerprint != "" && storedFingerprint != fingerprint {
			return "", ErrConflict
		}
		switch state {
		case "failed", "cleaned":
			var used, reserved, quota int64
			if err := tx.QueryRowContext(ctx, `SELECT
				COALESCE((SELECT SUM(size_bytes) FROM media WHERE owner_id = ? AND status = 'ready'), 0),
				COALESCE((SELECT SUM(expected_size) FROM upload_jobs WHERE user_id = ? AND state IN ('pending','uploading','verifying')), 0),
				media_quota_bytes FROM users WHERE id = ?`, userID, userID, userID).Scan(&used, &reserved, &quota); err != nil {
				return "", err
			}
			if size > quota || used > quota-size || reserved > quota-size-used {
				return "", ErrQuotaExceeded
			}
			if _, err := tx.ExecContext(ctx, `UPDATE upload_jobs SET object_path = ?, preview_path = '', state = 'pending', expected_size = ?, error_code = '', request_fingerprint = ?, updated_at = ? WHERE id = ? AND state IN ('failed','cleaned')`, objectPath, size, fingerprint, nowText(time.Now().UTC()), existingID); err != nil {
				return "", err
			}
			if err := tx.Commit(); err != nil {
				return "", err
			}
			return existingID, nil
		case "completed", "pending", "uploading", "verifying", "cleanup_required":
			return "", ErrConflict
		default:
			return "", ErrConflict
		}
	}
	var used, reserved, quota int64
	err = tx.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT SUM(size_bytes) FROM media WHERE owner_id = ? AND status = 'ready'), 0),
		COALESCE((SELECT SUM(expected_size) FROM upload_jobs WHERE user_id = ? AND state IN ('pending','uploading','verifying')), 0),
		media_quota_bytes FROM users WHERE id = ?`, userID, userID, userID).Scan(&used, &reserved, &quota)
	if err != nil {
		return "", err
	}
	if size > quota || used > quota-size || reserved > quota-size-used {
		return "", ErrQuotaExceeded
	}
	now := nowText(time.Now().UTC())
	_, err = tx.ExecContext(ctx, `INSERT INTO upload_jobs(id, user_id, client_request_id, object_path, state, expected_size, request_fingerprint, created_at, updated_at)
		VALUES(?, ?, ?, ?, 'pending', ?, ?, ?, ?)`, jobID, userID, requestID, objectPath, size, fingerprint, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return "", ErrConflict
		}
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return jobID, nil
}

func (s *Store) setJobState(ctx context.Context, jobID, state, code string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE upload_jobs SET state = ?, error_code = ?, updated_at = ? WHERE id = ?`, state, code, nowText(time.Now().UTC()), jobID)
}

func (s *Store) failUpload(ctx context.Context, jobID, objectPath, code string) {
	s.failUploadPaths(ctx, jobID, objectPath, "", code)
}

func validateUpload(input UploadInput) (string, string, error) {
	if !requestIDPattern.MatchString(input.ClientRequestID) || input.Filename == "" || input.Body == nil || input.Size <= 0 || input.Size > MaxFileSize {
		return "", "", ErrInvalid
	}
	mediaType := ""
	switch {
	case strings.HasPrefix(input.MimeType, "image/"):
		if input.MimeType == "image/svg+xml" {
			return "", "", ErrInvalid
		}
		mediaType = "image"
	case strings.HasPrefix(input.MimeType, "video/"):
		mediaType = "video"
	case strings.HasPrefix(input.MimeType, "audio/"):
		mediaType = "audio"
	default:
		return "", "", ErrInvalid
	}
	ext := strings.ToLower(filepath.Ext(input.Filename))
	if ext == "" || len(ext) > 12 {
		exts, _ := mime.ExtensionsByType(input.MimeType)
		if len(exts) > 0 {
			ext = exts[0]
		}
	}
	if ext == "" || !regexp.MustCompile(`^\.[a-z0-9]+$`).MatchString(ext) {
		ext = ".bin"
	}
	return mediaType, ext, nil
}

func cleanFilename(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = filepath.Base(value)
	if len([]rune(value)) > 240 {
		value = string([]rune(value)[:240])
	}
	return value
}

func newID() string {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

func nowText(value time.Time) string { return value.Format(time.RFC3339Nano) }

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
