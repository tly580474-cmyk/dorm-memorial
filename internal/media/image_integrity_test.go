package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	storagecache "dorm-memorial/internal/storage/cache"
)

func TestImageIntegrityChecksRemoteInsteadOfUploadCache(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "same-size-corruption"}[corrupt], func(t *testing.T) {
			objects := newMemoryObjects()
			cached, err := storagecache.New(objects, t.TempDir(), 1024)
			if err != nil {
				t.Fatal(err)
			}
			payload := []byte("original image bytes")
			if err := cached.Put(context.Background(), "/image.png", bytes.NewReader(payload), int64(len(payload))); err != nil {
				t.Fatal(err)
			}
			if corrupt {
				objects.mu.Lock()
				objects.objects["/image.png"][0] ^= 0xff
				objects.mu.Unlock()
			}
			sum := sha256.Sum256(payload)
			err = verifyImageIntegrity(context.Background(), cached, "/image.png", int64(len(payload)), hex.EncodeToString(sum[:]))
			if (err != nil) != corrupt {
				t.Fatalf("corrupt=%v verification=%v", corrupt, err)
			}
		})
	}
}

type corruptImageStorage struct{ *memoryObjects }

func (s *corruptImageStorage) Put(ctx context.Context, path string, body io.Reader, size int64) error {
	if err := s.memoryObjects.Put(ctx, path, body, size); err != nil {
		return err
	}
	if strings.HasPrefix(path, "/originals/") {
		s.mu.Lock()
		s.objects[path][0] ^= 0xff
		s.mu.Unlock()
	}
	return nil
}

func TestCorruptedRemoteImageNeverBecomesReadyAndReleasesQuota(t *testing.T) {
	db, owner := mediaTestUser(t)
	remote := &corruptImageStorage{newMemoryObjects()}
	cached, err := storagecache.New(remote, t.TempDir(), 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, cached)
	store.verifyDelays = []time.Duration{0}
	var payload bytes.Buffer
	if err := png.Encode(&payload, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	_, err = store.Upload(context.Background(), owner, UploadInput{ClientRequestID: "corrupt-remote-image", Filename: "test.png", MimeType: "image/png", Body: bytes.NewReader(payload.Bytes()), Size: int64(payload.Len())})
	if err == nil {
		t.Fatal("corrupted remote upload was accepted")
	}
	usage, err := store.Usage(context.Background(), owner.ID)
	if err != nil || usage.UsedBytes != 0 || usage.ReservedBytes != 0 {
		t.Fatalf("quota leaked: %+v %v", usage, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM media WHERE status='ready'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("corrupt media persisted: %d %v", count, err)
	}
	remote.mu.Lock()
	defer remote.mu.Unlock()
	if len(remote.objects) != 0 {
		t.Fatalf("corrupt remote object was not cleaned: %d", len(remote.objects))
	}
}
