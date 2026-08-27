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
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dorm-memorial/internal/database"
	"dorm-memorial/internal/identity"
	"dorm-memorial/internal/messaging"
	"dorm-memorial/internal/storage"
)

func TestRemoteOwnerSegmentFitsRestrictedStorage(t *testing.T) {
	ownerID := "5ebba3bdb607f775f2f4687596c2ad81"
	segment := remoteOwnerSegment(ownerID)
	if len(segment) != 16 {
		t.Fatalf("segment length = %d, want 16: %q", len(segment), segment)
	}
	if segment != remoteOwnerSegment(ownerID) {
		t.Fatal("owner segment is not deterministic")
	}
	if segment == remoteOwnerSegment(ownerID+"-different") {
		t.Fatal("different owners produced the same test segment")
	}
	if strings.ContainsAny(segment, "/\\") {
		t.Fatalf("segment contains a path separator: %q", segment)
	}
}

func TestVideoPreviewSeekIsStableAndInsideVideo(t *testing.T) {
	seek := videoPreviewSeek("0123456789abcdef0123456789abcdef", 10_000)
	if seek < 1.5 || seek > 7.5 {
		t.Fatalf("seek=%v, want a frame between 15%% and 75%%", seek)
	}
	if again := videoPreviewSeek("0123456789abcdef0123456789abcdef", 10_000); again != seek {
		t.Fatalf("seek changed between calls: %v != %v", again, seek)
	}
	if fallback := videoPreviewSeek("video-without-duration", 0); fallback != 1 {
		t.Fatalf("fallback seek=%v, want 1", fallback)
	}
}

func TestBuildVideoPreviewWithFFmpeg(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	sourcePath := filepath.Join(t.TempDir(), "preview-source.mp4")
	command := exec.Command(ffmpegPath, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=12", "-t", "2", "-pix_fmt", "yuv420p", "-y", sourcePath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create test video: %v: %s", err, output)
	}
	payload, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	objects := newMemoryObjects()
	objects.objects["/source.mp4"] = payload
	previewPath := buildVideoPreview(context.Background(), objects, ffmpegPath, "/source.mp4", "owner", "0123456789abcdef0123456789abcdef", time.Now(), 2_000)
	preview := objects.objects[previewPath]
	if previewPath == "" || len(preview) < 2 || preview[0] != 0xff || preview[1] != 0xd8 {
		t.Fatalf("preview was not stored as JPEG: path=%q bytes=%d", previewPath, len(preview))
	}
}

func TestPrepareMP4UploadMovesMetadataBeforeMedia(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	sourcePath := filepath.Join(t.TempDir(), "source.mp4")
	command := exec.Command(ffmpegPath, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=12", "-t", "2", "-pix_fmt", "yuv420p", "-y", sourcePath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create test video: %v: %s", err, output)
	}
	fastStart, err := MP4FastStart(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if fastStart {
		t.Fatal("test source unexpectedly already uses fast start")
	}
	payload, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := prepareMP4Upload(context.Background(), ffmpegPath, UploadInput{Body: bytes.NewReader(payload), Size: int64(len(payload))})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	file, ok := prepared.Body.(*os.File)
	if !ok {
		t.Fatalf("prepared body type = %T, want *os.File", prepared.Body)
	}
	fastStart, err = MP4FastStart(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !fastStart {
		t.Fatal("prepared video is not fast start")
	}
	if prepared.Size <= 0 {
		t.Fatalf("prepared size = %d", prepared.Size)
	}
}

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
	var objectPath, previewPath string
	if err := db.QueryRow("SELECT object_path, preview_path FROM media WHERE id = ?", record.ID).Scan(&objectPath, &previewPath); err != nil {
		t.Fatal(err)
	}
	wantOwnerSegment := "/" + remoteOwnerSegment(user.ID) + "/"
	if !strings.Contains(objectPath, wantOwnerSegment) || !strings.Contains(previewPath, wantOwnerSegment) {
		t.Fatalf("restricted-storage paths do not use compact owner segment: object=%q preview=%q", objectPath, previewPath)
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

func TestMessageAudioIsReadableOnlyByConversationMembers(t *testing.T) {
	db, owner := mediaTestUser(t)
	objects := newMemoryObjects()
	mediaStore := NewStore(db, objects)
	mediaStore.verifyDelays = []time.Duration{0}
	payload := []byte("voice note")
	record, err := mediaStore.Upload(context.Background(), owner, UploadInput{ClientRequestID: "message-audio-file", Filename: "晚安.m4a", MimeType: "audio/mp4", Size: int64(len(payload)), Body: bytes.NewReader(payload), DurationMS: 2400})
	if err != nil || record.MediaType != "audio" || record.DurationMS == nil || *record.DurationMS != 2400 {
		t.Fatalf("audio upload=%+v err=%v", record, err)
	}
	identities := identity.NewStore(db)
	recipient := registerMediaUser(t, identities, owner, "listener", "listener@example.test")
	outsider := registerMediaUser(t, identities, owner, "outsider", "outsider@example.test")
	outsider.Role = "admin"
	messageStore := messaging.NewStore(db)
	conversation, err := messageStore.StartDirect(context.Background(), owner, recipient.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messageStore.SendMessage(context.Background(), owner, conversation.ID, "", []string{record.ID}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	content, err := mediaStore.OpenContent(context.Background(), recipient, record.ID, "", false)
	if err != nil {
		t.Fatalf("recipient open audio: %v", err)
	}
	content.Body.Close()
	if _, err := mediaStore.OpenContent(context.Background(), outsider, record.ID, "", false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider admin attachment access err=%v", err)
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

func TestAdminMediaListReportsReferences(t *testing.T) {
	db, admin := mediaTestUser(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	mediaID := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if _, err := db.ExecContext(ctx, `INSERT INTO media(id, owner_id, object_path, original_filename, media_type, mime_type, size_bytes, sha256, status, created_at, updated_at)
		VALUES(?, ?, '/test/admin.jpg', '纪念照.jpg', 'image', 'image/jpeg', 256, ?, 'ready', ?, ?)`, mediaID, admin.ID, mediaID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE profiles SET avatar_path = ? WHERE user_id = ?`, mediaID, admin.ID); err != nil {
		t.Fatal(err)
	}
	items, err := NewStore(db, nil).ListAdmin(ctx, admin, "纪念", "image", "ready", 20)
	if err != nil || len(items) != 1 || items[0].ReferenceCount != 1 || items[0].OwnerNickname != admin.Nickname {
		t.Fatalf("admin media=%+v err=%v", items, err)
	}
}

func registerMediaUser(t *testing.T, identities *identity.Store, admin identity.User, username, email string) identity.User {
	t.Helper()
	code, _, err := identities.CreateInvite(context.Background(), admin, 1, time.Hour, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	user, err := identities.Register(context.Background(), identity.RegisterInput{InviteCode: code, Username: username, Email: email, Password: "member-password", Nickname: username}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	return user
}

var _ storage.ObjectStorage = (*memoryObjects)(nil)
