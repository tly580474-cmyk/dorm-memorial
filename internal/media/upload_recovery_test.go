package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"dorm-memorial/internal/storage"
)

type uploadRecoveryObjects struct {
	*memoryObjects
	failPuts        int
	failPreviewPuts int
	failDeletes     bool
	blockDeletes    bool
}

func (o *uploadRecoveryObjects) Put(ctx context.Context, path string, body io.Reader, size int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if o.failPuts > 0 || (strings.HasPrefix(path, "/previews/") && o.failPreviewPuts > 0) {
		if strings.HasPrefix(path, "/previews/") {
			o.failPreviewPuts--
		} else {
			o.failPuts--
		}
		// Simulate a provider that wrote the object before losing its response.
		o.mu.Lock()
		o.objects[path] = data
		o.mu.Unlock()
		return errors.New("injected put failure")
	}
	return o.memoryObjects.Put(ctx, path, bytes.NewReader(data), size)
}

func TestPreviewPutFailureRetainsReadyOriginalAndMaintenanceCleansOnlyPreview(t *testing.T) {
	db, user := mediaTestUser(t)
	objects := &uploadRecoveryObjects{memoryObjects: newMemoryObjects(), failPreviewPuts: 1, failDeletes: true}
	store := NewStore(db, objects)
	store.verifyDelays = []time.Duration{0}
	payload := recoveryImagePayload(t)
	input := UploadInput{ClientRequestID: "preview-cleanup-0001", Filename: "photo.png", MimeType: "image/png", Size: int64(len(payload)), Body: bytes.NewReader(payload)}
	record, err := store.Upload(context.Background(), user, input)
	if err != nil || record.Status != "ready" || record.HasPreview {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	var jobState, errorCode, previewPath, objectPath string
	if err := db.QueryRow(`SELECT state, error_code, preview_path FROM upload_jobs WHERE client_request_id = ?`, input.ClientRequestID).Scan(&jobState, &errorCode, &previewPath); err != nil {
		t.Fatal(err)
	}
	if jobState != "completed" || errorCode != "preview_cleanup_required" || previewPath == "" {
		t.Fatalf("preview cleanup marker state=%q code=%q path=%q", jobState, errorCode, previewPath)
	}
	if err := db.QueryRow(`SELECT object_path FROM media WHERE id = ?`, record.ID).Scan(&objectPath); err != nil {
		t.Fatal(err)
	}
	oldPreviewPath := previewPath
	objects.mu.Lock()
	_, originalExists := objects.objects[objectPath]
	_, partialPreviewExists := objects.objects[oldPreviewPath]
	objects.mu.Unlock()
	if !originalExists || !partialPreviewExists {
		t.Fatal("preview failure did not leave expected original/partial preview for recovery")
	}
	objects.failDeletes = false
	if err := store.StartMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT state, error_code, preview_path FROM upload_jobs WHERE client_request_id = ?`, input.ClientRequestID).Scan(&jobState, &errorCode, &previewPath); err != nil {
		t.Fatal(err)
	}
	if jobState != "completed" || errorCode != "" || previewPath != "" {
		t.Fatalf("preview cleanup marker remains state=%q code=%q path=%q", jobState, errorCode, previewPath)
	}
	objects.mu.Lock()
	_, originalExists = objects.objects[objectPath]
	_, partialPreviewExists = objects.objects[oldPreviewPath]
	objects.mu.Unlock()
	if !originalExists || partialPreviewExists {
		t.Fatal("maintenance touched the ready original or left cleanup state")
	}
}

func (o *uploadRecoveryObjects) Delete(ctx context.Context, path string) error {
	if o.blockDeletes {
		<-ctx.Done()
		return ctx.Err()
	}
	if o.failDeletes {
		return storage.ErrUnavailable
	}
	return o.memoryObjects.Delete(ctx, path)
}

func TestCanceledUploadLockWaiterDoesNotReleaseOwner(t *testing.T) {
	db, _ := mediaTestUser(t)
	store := NewStore(db, newMemoryObjects())
	ownerRelease, err := store.acquireUploadLock(context.Background(), "lock-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.acquireUploadLock(ctx, "lock-test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter err=%v", err)
	}
	acquired := make(chan func(), 1)
	go func() {
		release, err := store.acquireUploadLock(context.Background(), "lock-test")
		if err == nil {
			acquired <- release
		}
	}()
	select {
	case release := <-acquired:
		release()
		t.Fatal("new waiter acquired the owner lock before owner release")
	case <-time.After(20 * time.Millisecond):
	}
	ownerRelease()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("waiter did not acquire lock after owner release")
	}
}

func TestImageRetryRechecksQuotaAfterFailure(t *testing.T) {
	db, user := mediaTestUser(t)
	objects := &uploadRecoveryObjects{memoryObjects: newMemoryObjects(), failPuts: 1}
	store := NewStore(db, objects)
	store.verifyDelays = []time.Duration{0}
	payload := recoveryImagePayload(t)
	input := UploadInput{ClientRequestID: "image-quota-retry", Filename: "quota.png", MimeType: "image/png", Size: int64(len(payload)), Body: bytes.NewReader(payload)}
	if _, err := store.Upload(context.Background(), user, input); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("first upload err=%v", err)
	}
	if _, err := db.Exec(`UPDATE users SET media_quota_bytes = 1 WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	input.Body = bytes.NewReader(payload)
	if _, err := store.Upload(context.Background(), user, input); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("retry quota err=%v", err)
	}
}

func TestUploadCleanupDeadlinePersistsCleanupRequiredAndReleasesQuota(t *testing.T) {
	db, user := mediaTestUser(t)
	objects := &uploadRecoveryObjects{memoryObjects: newMemoryObjects(), failPuts: 1, blockDeletes: true}
	store := NewStore(db, objects)
	store.verifyDelays = []time.Duration{0}
	oldTimeout := uploadCleanupTimeout
	uploadCleanupTimeout = 10 * time.Millisecond
	defer func() { uploadCleanupTimeout = oldTimeout }()
	payload := recoveryImagePayload(t)
	input := UploadInput{ClientRequestID: "image-cleanup-timeout", Filename: "timeout.png", MimeType: "image/png", Size: int64(len(payload)), Body: bytes.NewReader(payload)}
	if _, err := store.Upload(context.Background(), user, input); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("upload err=%v", err)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM upload_jobs WHERE client_request_id = ?`, input.ClientRequestID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "cleanup_required" {
		t.Fatalf("state=%q, want cleanup_required", state)
	}
	usage, err := store.Usage(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.ReservedBytes != 0 {
		t.Fatalf("cleanup-required upload still reserves %d bytes", usage.ReservedBytes)
	}
}

func recoveryImagePayload(t *testing.T) []byte {
	t.Helper()
	var payload bytes.Buffer
	if err := png.Encode(&payload, image.NewRGBA(image.Rect(0, 0, 8, 6))); err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}

func TestImageUploadRetryReclaimsFailedRequest(t *testing.T) {
	db, user := mediaTestUser(t)
	objects := &uploadRecoveryObjects{memoryObjects: newMemoryObjects(), failPuts: 1}
	store := NewStore(db, objects)
	store.verifyDelays = []time.Duration{0}
	payload := recoveryImagePayload(t)
	input := UploadInput{ClientRequestID: "image-retry-0001", Filename: "room.png", MimeType: "image/png", Size: int64(len(payload)), Body: bytes.NewReader(payload)}
	if _, err := store.Upload(context.Background(), user, input); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("first upload err=%v", err)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM upload_jobs WHERE client_request_id = ?`, input.ClientRequestID).Scan(&state); err != nil || state != "failed" {
		t.Fatalf("failed job state=%q err=%v", state, err)
	}

	input.Body = bytes.NewReader(payload)
	record, err := store.Upload(context.Background(), user, input)
	if err != nil || record.Status != "ready" {
		t.Fatalf("retry record=%+v err=%v", record, err)
	}
	var jobs, mediaCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM upload_jobs WHERE client_request_id = ?`, input.ClientRequestID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM media WHERE id = ?`, record.ID).Scan(&mediaCount); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || mediaCount != 1 {
		t.Fatalf("retry duplicated rows: jobs=%d media=%d", jobs, mediaCount)
	}
	objects.mu.Lock()
	defer objects.mu.Unlock()
	if len(objects.objects) != 2 {
		t.Fatalf("remote objects=%d, want original and preview", len(objects.objects))
	}
}

func TestCompletedUploadDoesNotReuseDeletedMedia(t *testing.T) {
	db, user := mediaTestUser(t)
	objects := newMemoryObjects()
	store := NewStore(db, objects)
	store.verifyDelays = []time.Duration{0}
	payload := recoveryImagePayload(t)
	input := UploadInput{ClientRequestID: "image-deleted-retry", Filename: "deleted.png", MimeType: "image/png", Size: int64(len(payload)), Body: bytes.NewReader(payload)}
	record, err := store.Upload(context.Background(), user, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE media SET status = 'deleted' WHERE id = ?`, record.ID); err != nil {
		t.Fatal(err)
	}
	input.Body = bytes.NewReader(payload)
	if _, err := store.Upload(context.Background(), user, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted media retry err=%v, want conflict", err)
	}
}

func TestCleanupRequiredRetryCleansBeforeWriting(t *testing.T) {
	db, user := mediaTestUser(t)
	objects := &uploadRecoveryObjects{memoryObjects: newMemoryObjects()}
	store := NewStore(db, objects)
	store.verifyDelays = []time.Duration{0}
	now := nowText(time.Now().UTC())
	oldObject, oldPreview := "/orphan/original.jpg", "/orphan/preview.jpg"
	objects.objects[oldObject] = []byte("orphan")
	objects.objects[oldPreview] = []byte("preview")
	if _, err := db.Exec(`INSERT INTO upload_jobs(id, user_id, client_request_id, object_path, preview_path, state, expected_size, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, 'cleanup_required', 6, ?, ?)`, newID(), user.ID, "image-clean-0001", oldObject, oldPreview, now, now); err != nil {
		t.Fatal(err)
	}
	objects.failDeletes = true
	payload := recoveryImagePayload(t)
	input := UploadInput{ClientRequestID: "image-clean-0001", Filename: "clean.png", MimeType: "image/png", Size: int64(len(payload)), Body: bytes.NewReader(payload)}
	if _, err := store.Upload(context.Background(), user, input); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("cleanup failure err=%v", err)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM upload_jobs WHERE client_request_id = ?`, input.ClientRequestID).Scan(&state); err != nil || state != "cleanup_required" {
		t.Fatalf("cleanup state=%q err=%v", state, err)
	}
	objects.failDeletes = false
	input.Body = bytes.NewReader(payload)
	if _, err := store.Upload(context.Background(), user, input); err != nil {
		t.Fatalf("cleanup retry err=%v", err)
	}
	objects.mu.Lock()
	_, originalRemains := objects.objects[oldObject]
	_, previewRemains := objects.objects[oldPreview]
	objects.mu.Unlock()
	if originalRemains || previewRemains {
		t.Fatal("old cleanup-required objects were not removed before retry")
	}
}

func TestStartMaintenanceCleansOrphanImageUploadsAndPreservesReadyMedia(t *testing.T) {
	db, user := mediaTestUser(t)
	objects := newMemoryObjects()
	store := NewStore(db, objects)
	now := nowText(time.Now().UTC())
	orphanObject, orphanPreview := "/orphan/pending.jpg", "/orphan/pending-preview.jpg"
	objects.objects[orphanObject] = []byte("orphan")
	objects.objects[orphanPreview] = []byte("preview")
	if _, err := db.Exec(`INSERT INTO upload_jobs(id, user_id, client_request_id, object_path, preview_path, state, expected_size, created_at, updated_at)
		VALUES(?, ?, 'maintenance-orphan', ?, ?, 'uploading', 6, ?, ?)`, newID(), user.ID, orphanObject, orphanPreview, now, now); err != nil {
		t.Fatal(err)
	}
	readyID := newID()
	readyObject := "/ready/photo.jpg"
	objects.objects[readyObject] = []byte("ready")
	if _, err := db.Exec(`INSERT INTO media(id, owner_id, object_path, original_filename, media_type, mime_type, size_bytes, sha256, status, created_at, updated_at)
		VALUES(?, ?, ?, 'photo.jpg', 'image', 'image/jpeg', 5, 'hash', 'ready', ?, ?)`, readyID, user.ID, readyObject, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO upload_jobs(id, user_id, client_request_id, object_path, state, expected_size, created_at, updated_at)
		VALUES(?, ?, 'maintenance-ready', ?, 'verifying', 5, ?, ?)`, newID(), user.ID, readyObject, now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.StartMaintenance(context.Background()); err != nil {
		t.Fatal(err)
	}
	var orphanState, readyState string
	if err := db.QueryRow(`SELECT state FROM upload_jobs WHERE client_request_id = 'maintenance-orphan'`).Scan(&orphanState); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT state FROM upload_jobs WHERE client_request_id = 'maintenance-ready'`).Scan(&readyState); err != nil {
		t.Fatal(err)
	}
	if orphanState != "cleaned" || readyState != "completed" {
		t.Fatalf("maintenance states orphan=%q ready=%q", orphanState, readyState)
	}
	objects.mu.Lock()
	_, orphanOriginalRemains := objects.objects[orphanObject]
	_, orphanPreviewRemains := objects.objects[orphanPreview]
	_, readyRemains := objects.objects[readyObject]
	objects.mu.Unlock()
	if orphanOriginalRemains || orphanPreviewRemains || !readyRemains {
		t.Fatal("maintenance removed the wrong remote object")
	}
	usage, err := store.Usage(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.ReservedBytes != 0 {
		t.Fatalf("orphan reservation remains: %d", usage.ReservedBytes)
	}
}
