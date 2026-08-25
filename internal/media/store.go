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
	ErrQuotaExceeded      = errors.New("media quota exceeded")
	ErrStorageUnavailable = errors.New("media storage unavailable")
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
	CreatedAt        time.Time `json:"created_at"`
}

type UploadInput struct {
	ClientRequestID string
	Filename        string
	MimeType        string
	Size            int64
	Body            io.Reader
	IPAddress       string
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

type Store struct {
	db           *sql.DB
	objects      storage.ObjectStorage
	verifyDelays []time.Duration
}

func NewStore(db *sql.DB, objects storage.ObjectStorage) *Store {
	return &Store{db: db, objects: objects, verifyDelays: []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second}}
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

	if existing, ok, err := s.existingRequest(ctx, actor.ID, input.ClientRequestID); err != nil {
		return Record{}, err
	} else if ok {
		return existing, nil
	}

	mediaID := newID()
	objectPath := fmt.Sprintf("/originals/%s/%s/%s%s", actor.ID, time.Now().UTC().Format("2006/01"), mediaID, ext)
	jobID := newID()
	if err := s.reserve(ctx, actor.ID, jobID, input.ClientRequestID, objectPath, input.Size); err != nil {
		return Record{}, err
	}
	s.setJobState(ctx, jobID, "uploading", "")

	hasher := sha256.New()
	if err := s.objects.Put(ctx, objectPath, io.TeeReader(input.Body, hasher), input.Size); err != nil {
		s.failUpload(context.WithoutCancel(ctx), jobID, objectPath, "storage_write_failed")
		return Record{}, fmt.Errorf("upload object: %w", errors.Join(ErrStorageUnavailable, err))
	}
	s.setJobState(context.WithoutCancel(ctx), jobID, "verifying", "")
	if err := s.verifySize(ctx, objectPath, input.Size); err != nil {
		s.failUpload(context.WithoutCancel(ctx), jobID, objectPath, "storage_verify_failed")
		return Record{}, fmt.Errorf("verify uploaded object: %w", ErrStorageUnavailable)
	}

	now := time.Now().UTC()
	record := Record{
		ID: mediaID, OwnerID: actor.ID, OriginalFilename: input.Filename, MediaType: mediaType,
		MimeType: input.MimeType, SizeBytes: input.Size, SHA256: hex.EncodeToString(hasher.Sum(nil)), Status: "ready", CreatedAt: now,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.failUpload(context.WithoutCancel(ctx), jobID, objectPath, "database_failed")
		return Record{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO media(id, owner_id, object_path, original_filename, media_type, mime_type, size_bytes, sha256, status, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, 'ready', ?, ?)`, mediaID, actor.ID, objectPath, input.Filename, mediaType, input.MimeType, input.Size, record.SHA256, nowText(now), nowText(now)); err != nil {
		_ = tx.Rollback()
		s.failUpload(context.WithoutCancel(ctx), jobID, objectPath, "database_failed")
		return Record{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE upload_jobs SET state = 'completed', updated_at = ? WHERE id = ?`, nowText(now), jobID); err != nil {
		_ = tx.Rollback()
		s.failUpload(context.WithoutCancel(ctx), jobID, objectPath, "database_failed")
		return Record{}, err
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, metadata_json, ip_address, created_at)
		VALUES(?, 'media.upload', 'media', ?, ?, ?, ?)`, actor.ID, mediaID, fmt.Sprintf(`{"size_bytes":%d}`, input.Size), input.IPAddress, nowText(now))
	if err := tx.Commit(); err != nil {
		s.failUpload(context.WithoutCancel(ctx), jobID, objectPath, "database_failed")
		return Record{}, err
	}
	return record, nil
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

func (s *Store) Delete(ctx context.Context, actor identity.User, id, ip string) error {
	var ownerID, objectPath, status string
	var attached int
	err := s.db.QueryRowContext(ctx, `SELECT m.owner_id, m.object_path, m.status, EXISTS(SELECT 1 FROM post_media pm WHERE pm.media_id = m.id)
		FROM media m WHERE m.id = ?`, id).Scan(&ownerID, &objectPath, &status, &attached)
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

func (s *Store) OpenContent(ctx context.Context, actor identity.User, id, byteRange string) (Content, error) {
	var ownerID, objectPath, filename, mimeType, status string
	var size int64
	var publiclyReadable int
	err := s.db.QueryRowContext(ctx, `SELECT m.owner_id, m.object_path, m.original_filename, m.mime_type, m.size_bytes, m.status,
		EXISTS(SELECT 1 FROM post_media pm JOIN posts p ON p.id = pm.post_id WHERE pm.media_id = m.id AND p.status = 'published' AND p.visibility = 'members')
		FROM media m WHERE m.id = ?`, id).Scan(&ownerID, &objectPath, &filename, &mimeType, &size, &status, &publiclyReadable)
	if errors.Is(err, sql.ErrNoRows) || status == "deleted" {
		return Content{}, ErrNotFound
	}
	if err != nil {
		return Content{}, err
	}
	if actor.Role != "admin" && actor.ID != ownerID && publiclyReadable == 0 {
		return Content{}, ErrForbidden
	}
	if status != "ready" || s.objects == nil {
		return Content{}, ErrStorageUnavailable
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

func (s *Store) existingRequest(ctx context.Context, userID, requestID string) (Record, bool, error) {
	var state, objectPath string
	err := s.db.QueryRowContext(ctx, `SELECT state, object_path FROM upload_jobs WHERE user_id = ? AND client_request_id = ?`, userID, requestID).Scan(&state, &objectPath)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	if state != "completed" {
		return Record{}, false, ErrConflict
	}
	record, err := s.recordByObjectPath(ctx, objectPath)
	return record, err == nil, err
}

func (s *Store) recordByObjectPath(ctx context.Context, objectPath string) (Record, error) {
	var record Record
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id, owner_id, original_filename, media_type, mime_type, size_bytes, sha256, status, created_at FROM media WHERE object_path = ?`, objectPath).
		Scan(&record.ID, &record.OwnerID, &record.OriginalFilename, &record.MediaType, &record.MimeType, &record.SizeBytes, &record.SHA256, &record.Status, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return record, nil
}

func (s *Store) reserve(ctx context.Context, userID, jobID, requestID, objectPath string, size int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var used, reserved, quota int64
	err = tx.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT SUM(size_bytes) FROM media WHERE owner_id = ? AND status = 'ready'), 0),
		COALESCE((SELECT SUM(expected_size) FROM upload_jobs WHERE user_id = ? AND state IN ('pending','uploading','verifying')), 0),
		media_quota_bytes FROM users WHERE id = ?`, userID, userID, userID).Scan(&used, &reserved, &quota)
	if err != nil {
		return err
	}
	if size > quota || used > quota-size || reserved > quota-size-used {
		return ErrQuotaExceeded
	}
	now := nowText(time.Now().UTC())
	_, err = tx.ExecContext(ctx, `INSERT INTO upload_jobs(id, user_id, client_request_id, object_path, state, expected_size, created_at, updated_at)
		VALUES(?, ?, ?, ?, 'pending', ?, ?, ?)`, jobID, userID, requestID, objectPath, size, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrConflict
		}
		return err
	}
	return tx.Commit()
}

func (s *Store) setJobState(ctx context.Context, jobID, state, code string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE upload_jobs SET state = ?, error_code = ?, updated_at = ? WHERE id = ?`, state, code, nowText(time.Now().UTC()), jobID)
}

func (s *Store) failUpload(ctx context.Context, jobID, objectPath, code string) {
	state := "failed"
	if err := s.objects.Delete(ctx, objectPath); err != nil && !errors.Is(err, storage.ErrNotFound) {
		state = "cleanup_required"
	}
	s.setJobState(ctx, jobID, state, code)
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
