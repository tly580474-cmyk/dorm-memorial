package media

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"dorm-memorial/internal/identity"
)

func TestImageDisplayArchiveDBFailureCleansUnregisteredObject(t *testing.T) {
	store, objects, descriptor := imageDisplayFixture(t, false)
	_, err := store.db.Exec(`CREATE TRIGGER fail_display_variant_insert
		BEFORE INSERT ON media_variants
		WHEN NEW.media_id = 'display-test' AND NEW.kind = 'display'
		BEGIN
			SELECT RAISE(ABORT, 'forced display variant insert failure');
		END`)
	if err != nil {
		t.Fatal(err)
	}

	content, err := store.OpenDescriptor(context.Background(), descriptor, "", "display")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(content.Body)
	content.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, format, err := image.Decode(bytes.NewReader(body)); err != nil || format != "jpeg" {
		t.Fatalf("returned display is invalid: format=%q err=%v", format, err)
	}

	close(objects.release)
	waitForImageArchive(t, store)

	displayPath := imageDisplayPath(descriptor.OwnerID, descriptor.ID, descriptor.CreatedText)
	objects.mu.Lock()
	_, originalExists := objects.objects[descriptor.ObjectPath]
	_, displayExists := objects.objects[displayPath]
	objects.mu.Unlock()
	if !originalExists {
		t.Fatal("database registration failure removed the original object")
	}
	if displayExists {
		t.Fatal("unregistered display object was not cleaned up")
	}
	var variants int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM media_variants WHERE media_id = 'display-test' AND kind = 'display'`).Scan(&variants); err != nil {
		t.Fatal(err)
	}
	if variants != 0 {
		t.Fatalf("failed display registration left %d variants", variants)
	}
}

type blockingLifecycleObjects struct {
	*memoryObjects
	deleteStarted  chan struct{}
	releaseDelete  chan struct{}
	displayPutDone chan struct{}
	deleteOnce     sync.Once
	putOnce        sync.Once
	releaseOnce    sync.Once
}

func newBlockingLifecycleObjects() *blockingLifecycleObjects {
	return &blockingLifecycleObjects{
		memoryObjects:  newMemoryObjects(),
		deleteStarted:  make(chan struct{}),
		releaseDelete:  make(chan struct{}),
		displayPutDone: make(chan struct{}),
	}
}

func (o *blockingLifecycleObjects) Put(ctx context.Context, objectPath string, body io.Reader, size int64) error {
	err := o.memoryObjects.Put(ctx, objectPath, body, size)
	if strings.HasPrefix(objectPath, "/display/") {
		o.putOnce.Do(func() { close(o.displayPutDone) })
	}
	return err
}

func (o *blockingLifecycleObjects) Delete(ctx context.Context, objectPath string) error {
	if objectPath == "/image.png" {
		o.deleteOnce.Do(func() { close(o.deleteStarted) })
		select {
		case <-o.releaseDelete:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return o.memoryObjects.Delete(ctx, objectPath)
}

func (o *blockingLifecycleObjects) release() {
	o.releaseOnce.Do(func() { close(o.releaseDelete) })
}

func blockingLifecycleImageFixture(t *testing.T) (*Store, *blockingLifecycleObjects, ContentDescriptor, identity.User) {
	t.Helper()
	db, owner := mediaTestUser(t)
	objects := newBlockingLifecycleObjects()
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
		objects.release()
		waitForImageArchive(t, store)
	})
	return store, objects, descriptor, owner
}

func waitForLifecycleLockWaiter(t *testing.T, store *Store, mediaID string) {
	t.Helper()
	key := "media-lifecycle\x00" + mediaID
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		store.uploadLockMu.Lock()
		lock := store.uploadLocks[key]
		refs := 0
		if lock != nil {
			refs = lock.refs
		}
		store.uploadLockMu.Unlock()
		if refs >= 2 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("display archive did not wait for the media lifecycle lock")
}

func TestImageDisplayArchiveDoesNotRaceMediaDelete(t *testing.T) {
	store, objects, descriptor, owner := blockingLifecycleImageFixture(t)
	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- store.Delete(context.Background(), owner, descriptor.ID, "")
	}()
	select {
	case <-objects.deleteStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("media delete did not reach remote storage")
	}

	content, err := store.OpenDescriptor(context.Background(), descriptor, "", "display")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(content.Body)
	content.Body.Close()
	if err != nil || len(body) == 0 {
		t.Fatalf("display response lost during delete: %v", err)
	}
	select {
	case <-objects.displayPutDone:
	case <-time.After(2 * time.Second):
		t.Fatal("display archive did not write its remote object")
	}
	waitForLifecycleLockWaiter(t, store, descriptor.ID)

	var variants int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM media_variants WHERE media_id = ?`, descriptor.ID).Scan(&variants); err != nil {
		t.Fatal(err)
	}
	if variants != 0 {
		t.Fatalf("display archive registered a variant while delete was in progress: %d", variants)
	}
	objects.release()
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	waitForImageArchive(t, store)

	var status string
	if err := store.db.QueryRow(`SELECT status FROM media WHERE id = ?`, descriptor.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "deleted" {
		t.Fatalf("media status=%q", status)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM media_variants WHERE media_id = ?`, descriptor.ID).Scan(&variants); err != nil {
		t.Fatal(err)
	}
	if variants != 0 {
		t.Fatalf("deleted media retained %d variants", variants)
	}
	displayPath := imageDisplayPath(descriptor.OwnerID, descriptor.ID, descriptor.CreatedText)
	objects.mu.Lock()
	_, originalExists := objects.objects[descriptor.ObjectPath]
	_, displayExists := objects.objects[displayPath]
	objects.mu.Unlock()
	if originalExists || displayExists {
		t.Fatalf("remote cleanup incomplete: original=%v display=%v", originalExists, displayExists)
	}
}
