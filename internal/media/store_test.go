package media

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"path"
	"sync"
	"testing"
	"time"

	"dorm-memorial/internal/database"
	"dorm-memorial/internal/identity"
	"dorm-memorial/internal/storage"
)

type memoryObjects struct {
	mu       sync.Mutex
	objects  map[string][]byte
	putCount int
	badStat  bool
}

func newMemoryObjects() *memoryObjects { return &memoryObjects{objects: make(map[string][]byte)} }

func (m *memoryObjects) Put(_ context.Context, objectPath string, body io.Reader, size int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return errors.New("size mismatch")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.putCount++
	m.objects[objectPath] = data
	return nil
}

func (m *memoryObjects) Open(_ context.Context, objectPath string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[objectPath]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memoryObjects) Stat(_ context.Context, objectPath string) (storage.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[objectPath]
	if !ok {
		return storage.ObjectInfo{}, storage.ErrNotFound
	}
	size := int64(len(data))
	if m.badStat {
		size++
	}
	return storage.ObjectInfo{Path: objectPath, Name: path.Base(objectPath), Size: size}, nil
}

func (m *memoryObjects) Delete(_ context.Context, objectPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[objectPath]; !ok {
		return storage.ErrNotFound
	}
	delete(m.objects, objectPath)
	return nil
}

func (m *memoryObjects) Move(context.Context, string, string) error { return nil }
func (m *memoryObjects) ResolveURL(context.Context, string) (string, error) {
	return "", errors.New("not implemented")
}

func TestUploadReservesQuotaVerifiesAndIsIdempotent(t *testing.T) {
	db, user := mediaTestUser(t)
	objects := newMemoryObjects()
	store := NewStore(db, objects)
	store.verifyDelays = []time.Duration{0}
	var imageBytes bytes.Buffer
	source := image.NewRGBA(image.Rect(0, 0, 4, 3))
	source.Set(1, 1, color.RGBA{R: 180, G: 60, B: 40, A: 255})
	if err := png.Encode(&imageBytes, source); err != nil {
		t.Fatal(err)
	}
	payload := imageBytes.Bytes()
	input := UploadInput{ClientRequestID: "request-0001", Filename: "room.png", MimeType: "image/png", Size: int64(len(payload)), Body: bytes.NewReader(payload)}

	record, err := store.Upload(context.Background(), user, input)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "ready" || record.SHA256 == "" || record.SizeBytes != int64(len(payload)) || !record.HasPreview || record.Width == nil || *record.Width != 4 || record.Height == nil || *record.Height != 3 {
		t.Fatalf("unexpected record: %+v", record)
	}
	usage, err := store.Usage(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.UsedBytes != int64(len(payload)) || usage.ReservedBytes != 0 {
		t.Fatalf("unexpected usage: %+v", usage)
	}

	input.Body = bytes.NewReader(payload)
	again, err := store.Upload(context.Background(), user, input)
	if err != nil {
		t.Fatal(err)
	}
	// One write stores the original and one stores its generated preview.
	if again.ID != record.ID || objects.putCount != 2 {
		t.Fatalf("idempotency failed: ids %q/%q putCount=%d", record.ID, again.ID, objects.putCount)
	}
}

func TestPreviewFailureFallsBackToOriginal(t *testing.T) {
	db, user := mediaTestUser(t)
	objects := newMemoryObjects()
	store := NewStore(db, objects)
	store.verifyDelays = []time.Duration{0}
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	payload := imageBytes.Bytes()
	record, err := store.Upload(context.Background(), user, UploadInput{ClientRequestID: "preview-fallback", Filename: "avatar.png", MimeType: "image/png", Size: int64(len(payload)), Body: bytes.NewReader(payload)})
	if err != nil {
		t.Fatal(err)
	}
	var previewPath string
	if err := db.QueryRow("SELECT preview_path FROM media WHERE id = ?", record.ID).Scan(&previewPath); err != nil {
		t.Fatal(err)
	}
	objects.mu.Lock()
	delete(objects.objects, previewPath)
	objects.mu.Unlock()
	content, err := store.OpenContent(context.Background(), user, record.ID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer content.Body.Close()
	got, _ := io.ReadAll(content.Body)
	if content.MimeType != "image/png" || !bytes.Equal(got, payload) {
		t.Fatalf("fallback mime=%q bytes=%d", content.MimeType, len(got))
	}
}

func TestHiddenGuestbookMediaIsReadableByRecipient(t *testing.T) {
	db, owner := mediaTestUser(t)
	objects := newMemoryObjects()
	store := NewStore(db, objects)
	store.verifyDelays = []time.Duration{0}
	payload := []byte("hidden guestbook video")
	record, err := store.Upload(context.Background(), owner, UploadInput{ClientRequestID: "hidden-guestbook-media", Filename: "memory.mp4", MimeType: "video/mp4", Size: int64(len(payload)), Body: bytes.NewReader(payload)})
	if err != nil {
		t.Fatal(err)
	}
	identities := identity.NewStore(db)
	code, _, err := identities.CreateInvite(context.Background(), owner, 1, time.Hour, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := identities.Register(context.Background(), identity.RegisterInput{InviteCode: code, Username: "recipient", Email: "recipient@example.test", Password: "recipient-password", Nickname: "接收者"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	entryID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := db.Exec(`INSERT INTO guestbook_entries(id, author_id, recipient_id, body, status, created_at, updated_at) VALUES(?, ?, ?, '隐藏留言', 'hidden', ?, ?)`, entryID, owner.ID, recipient.ID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO guestbook_media(entry_id, media_id, position) VALUES(?, ?, 0)`, entryID, record.ID); err != nil {
		t.Fatal(err)
	}
	content, err := store.OpenContent(context.Background(), recipient, record.ID, "", false)
	if err != nil {
		t.Fatalf("recipient read hidden guestbook media: %v", err)
	}
	defer content.Body.Close()
	got, _ := io.ReadAll(content.Body)
	if !bytes.Equal(got, payload) {
		t.Fatalf("hidden guestbook media bytes=%q", got)
	}
}

func TestUploadRejectsQuotaBeforeWriting(t *testing.T) {
	db, user := mediaTestUser(t)
	if _, err := db.Exec("UPDATE users SET media_quota_bytes = 3 WHERE id = ?", user.ID); err != nil {
		t.Fatal(err)
	}
	objects := newMemoryObjects()
	store := NewStore(db, objects)
	err := func() error {
		_, err := store.Upload(context.Background(), user, UploadInput{ClientRequestID: "request-0002", Filename: "large.mp4", MimeType: "video/mp4", Size: 4, Body: bytes.NewReader([]byte("1234"))})
		return err
	}()
	if !errors.Is(err, ErrQuotaExceeded) || objects.putCount != 0 {
		t.Fatalf("expected quota error before write, got %v putCount=%d", err, objects.putCount)
	}
}

func TestUploadSizeVerificationCleansRemoteObject(t *testing.T) {
	db, user := mediaTestUser(t)
	objects := newMemoryObjects()
	objects.badStat = true
	store := NewStore(db, objects)
	store.verifyDelays = []time.Duration{0}
	payload := []byte("video")
	_, err := store.Upload(context.Background(), user, UploadInput{ClientRequestID: "request-0003", Filename: "clip.mp4", MimeType: "video/mp4", Size: int64(len(payload)), Body: bytes.NewReader(payload)})
	if !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("expected storage verification error, got %v", err)
	}
	objects.mu.Lock()
	remaining := len(objects.objects)
	objects.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected remote cleanup, %d objects remain", remaining)
	}
	var state string
	if err := db.QueryRow("SELECT state FROM upload_jobs WHERE client_request_id = 'request-0003'").Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "failed" {
		t.Fatalf("state=%q", state)
	}
}

func mediaTestUser(t *testing.T) (*sql.DB, identity.User) {
	t.Helper()
	db, err := database.Open(context.Background(), path.Join(t.TempDir(), "media.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	identities := identity.NewStore(db)
	if _, err := identities.BootstrapAdmin(context.Background(), "media-admin", "media@example.test", "correct-horse-battery", "媒体管理员"); err != nil {
		t.Fatal(err)
	}
	user, err := identities.Authenticate(context.Background(), "media-admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	return db, user
}

var _ storage.ObjectStorage = (*memoryObjects)(nil)
