package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type delayedImageArchive struct {
	*memoryObjects
	started chan struct{}
	release chan struct{}
	once    sync.Once
	opens   atomic.Int32
	fail    bool
}

func (o *delayedImageArchive) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	o.opens.Add(1)
	return o.memoryObjects.Open(ctx, path)
}

func (o *delayedImageArchive) Put(ctx context.Context, path string, body io.Reader, size int64) error {
	if strings.HasPrefix(path, "/display/") {
		o.once.Do(func() { close(o.started) })
		select {
		case <-o.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if o.fail {
			return errors.New("archive offline")
		}
	}
	return o.memoryObjects.Put(ctx, path, body, size)
}

func imageDisplayFixture(t *testing.T, fail bool) (*Store, *delayedImageArchive, ContentDescriptor) {
	t.Helper()
	db, owner := mediaTestUser(t)
	objects := &delayedImageArchive{memoryObjects: newMemoryObjects(), started: make(chan struct{}), release: make(chan struct{}), fail: fail}
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 80, 40))); err != nil {
		t.Fatal(err)
	}
	objects.objects["/image.png"] = source.Bytes()
	now := nowText(time.Now().UTC())
	if _, err := db.Exec(`INSERT INTO media(id,owner_id,object_path,original_filename,media_type,mime_type,size_bytes,sha256,status,created_at,updated_at)
		VALUES('display-test',?,'/image.png','image.png','image','image/png',?,'test','ready',?,?)`, owner.ID, source.Len(), now, now); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, objects)
	descriptor, err := store.InspectContent(context.Background(), owner, "display-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-objects.release:
		default:
			close(objects.release)
		}
		waitForImageArchive(t, store)
	})
	return store, objects, descriptor
}

func waitForImageArchive(t *testing.T, store *Store) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		store.jobMu.Lock()
		pending := len(store.imageDisplays)
		store.jobMu.Unlock()
		if pending == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("image archive did not finish")
}

func TestImageDisplayServesBeforeSlowArchiveAndSharesRenderedBytes(t *testing.T) {
	store, objects, descriptor := imageDisplayFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first, err := store.OpenDescriptor(ctx, descriptor, "", "display")
	if err != nil {
		t.Fatalf("display must not wait for remote Put: %v", err)
	}
	body, err := io.ReadAll(first.Body)
	first.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || format != "jpeg" || config.Width != 80 || first.ContentLength != int64(len(body)) {
		t.Fatalf("invalid display response: %v, %s, %+v", err, format, first)
	}
	select {
	case <-objects.started:
	case <-ctx.Done():
		t.Fatal("archive did not start")
	}
	second, err := store.OpenDescriptor(ctx, descriptor, "", "display")
	if err != nil {
		t.Fatal(err)
	}
	secondBody, err := io.ReadAll(second.Body)
	second.Body.Close()
	if err != nil || !bytes.Equal(body, secondBody) || objects.opens.Load() != 1 {
		t.Fatalf("duplicate render or inconsistent response: opens=%d err=%v", objects.opens.Load(), err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM media_variants WHERE media_id='display-test'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("archive was recorded before it succeeded: count=%d err=%v", count, err)
	}
	close(objects.release)
	waitForImageArchive(t, store)
	if err := store.db.QueryRow(`SELECT count(*) FROM media_variants WHERE media_id='display-test' AND kind='display'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("archive not registered: count=%d err=%v", count, err)
	}
}

func TestImageDisplayArchiveFailureDoesNotFailTheDisplayedPhoto(t *testing.T) {
	store, objects, descriptor := imageDisplayFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	content, err := store.OpenDescriptor(ctx, descriptor, "", "display")
	if err != nil {
		t.Fatal(err)
	}
	close(objects.release)
	waitForImageArchive(t, store)
	body, err := io.ReadAll(content.Body)
	content.Body.Close()
	if err != nil || len(body) == 0 {
		t.Fatalf("display lost after archival failed: %v", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM media_variants WHERE media_id='display-test'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed archive registered: count=%d err=%v", count, err)
	}
}

func TestImageDisplayDoesNotRestoreADeletedPhoto(t *testing.T) {
	store, objects, descriptor := imageDisplayFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	content, err := store.OpenDescriptor(ctx, descriptor, "", "display")
	if err != nil {
		t.Fatal(err)
	}
	content.Body.Close()
	if _, err := store.db.Exec(`UPDATE media SET status='deleted' WHERE id='display-test'`); err != nil {
		t.Fatal(err)
	}
	close(objects.release)
	waitForImageArchive(t, store)
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM media_variants WHERE media_id='display-test'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted photo received variant: count=%d err=%v", count, err)
	}
	objects.mu.Lock()
	_, exists := objects.objects[imageDisplayPath(descriptor.OwnerID, descriptor.ID, descriptor.CreatedText)]
	objects.mu.Unlock()
	if exists {
		t.Fatal("deleted photo retained orphaned display")
	}
}
