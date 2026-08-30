package media

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"testing"
	"time"

	"dorm-memorial/internal/storage"
)

type unavailablePreviewSource struct {
	*memoryObjects
	opens int
}

func (o *unavailablePreviewSource) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	o.opens++
	if o.opens > 1 {
		return nil, storage.ErrUnavailable
	}
	return o.memoryObjects.Open(ctx, path)
}

func TestImageDimensionsSurvivePreviewSourceFailure(t *testing.T) {
	db, owner := mediaTestUser(t)
	objects := &unavailablePreviewSource{memoryObjects: newMemoryObjects()}
	store := NewStore(db, objects)
	store.verifyDelays = []time.Duration{0}
	var payload bytes.Buffer
	if err := png.Encode(&payload, image.NewRGBA(image.Rect(0, 0, 7, 3))); err != nil {
		t.Fatal(err)
	}
	record, err := store.Upload(context.Background(), owner, UploadInput{ClientRequestID: "preview-metadata-failure", Filename: "test.png", MimeType: "image/png", Body: bytes.NewReader(payload.Bytes()), Size: int64(payload.Len()), Width: 999, Height: 999})
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "ready" || record.HasPreview || record.Width == nil || record.Height == nil || *record.Width != 7 || *record.Height != 3 {
		t.Fatalf("validated dimensions lost or client metadata trusted: %+v", record)
	}
}
