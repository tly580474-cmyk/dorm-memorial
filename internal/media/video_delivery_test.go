package media

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type observedVideoObjects struct {
	*memoryObjects
	warms   atomic.Int32
	deletes atomic.Int32
}

func (m *observedVideoObjects) IsCached(string) bool { return false }
func (m *observedVideoObjects) Warm(context.Context, string) error {
	m.warms.Add(1)
	return nil
}
func (m *observedVideoObjects) Delete(ctx context.Context, objectPath string) error {
	m.deletes.Add(1)
	return m.memoryObjects.Delete(ctx, objectPath)
}

func TestVideoReadDoesNotScheduleWholeFileWarm(t *testing.T) {
	objects := &observedVideoObjects{memoryObjects: newMemoryObjects()}
	objects.objects["/original.mp4"] = []byte("source")
	store := NewStore(nil, objects)
	descriptor := ContentDescriptor{ID: "video", ObjectPath: "/original.mp4", MediaType: "video", MimeType: "video/mp4", Size: 6}
	content, err := store.OpenDescriptor(context.Background(), descriptor, "bytes=0-1", "original")
	if err != nil {
		t.Fatal(err)
	}
	content.Body.Close()
	// Scheduling is registered synchronously before the reader returns. An
	// already-finished background warm is detected by its call counter.
	store.jobMu.Lock()
	scheduled := len(store.background)
	store.jobMu.Unlock()
	if scheduled != 0 || objects.warms.Load() != 0 {
		t.Fatal("a video read triggered an extra full download")
	}
}

func TestDeletePlaybackAliasRemovesOriginalOnlyOnce(t *testing.T) {
	db, owner := mediaTestUser(t)
	objects := &observedVideoObjects{memoryObjects: newMemoryObjects()}
	objects.objects["/source.mp4"] = []byte("source")
	store := NewStore(db, objects)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO media(id,owner_id,object_path,original_filename,media_type,mime_type,size_bytes,sha256,status,created_at,updated_at)
		VALUES('alias-video',?,'/source.mp4','source.mp4','video','video/mp4',6,'hash','ready',?,?)`, owner.ID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO media_variants(media_id,kind,object_path,mime_type,size_bytes,created_at,updated_at)
		VALUES('alias-video','playback','/source.mp4','video/mp4',6,?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	descriptor, err := store.InspectContent(context.Background(), owner, "alias-video")
	if err != nil {
		t.Fatal(err)
	}
	content, err := store.OpenDescriptor(context.Background(), descriptor, "", "playback")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(content.Body)
	content.Body.Close()
	if err != nil || string(body) != "source" {
		t.Fatalf("alias read=%q err=%v", body, err)
	}
	if err := store.Delete(context.Background(), owner, "alias-video", ""); err != nil {
		t.Fatal(err)
	}
	if objects.deletes.Load() != 1 {
		t.Fatalf("remote deletes=%d, want 1", objects.deletes.Load())
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM media WHERE id='alias-video'`).Scan(&status); err != nil || status != "deleted" {
		t.Fatalf("status=%q err=%v", status, err)
	}
}
