package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

type imageDisplayJob struct {
	ready chan struct{}
	data  []byte
	err   error
}

func (s *Store) openDeliveredImageDisplay(ctx context.Context, descriptor ContentDescriptor, byteRange string) (Content, error) {
	// Existing GIF display variants may be static JPEGs from an older release.
	// Always keep animation when opening a GIF in the viewer.
	if descriptor.MimeType == "image/gif" {
		return s.OpenDescriptor(ctx, descriptor, byteRange, "original")
	}
	if descriptor.DisplayPath == "" {
		return s.openImageDisplay(ctx, descriptor)
	}
	display := descriptor
	display.ObjectPath, display.MimeType, display.Size = descriptor.DisplayPath, descriptor.DisplayMIME, descriptor.DisplaySize
	content, err := s.OpenDescriptor(ctx, display, byteRange, "original")
	if err == nil || ctx.Err() != nil {
		return content, err
	}
	// A missing or temporarily unavailable derivative must not hide an original
	// that is still readable. Keep the same permission-checked descriptor.
	return s.OpenDescriptor(ctx, descriptor, byteRange, "original")
}

// Concurrent viewers share the rendered bytes while the remote archive is
// pending. Bound both retained images and render concurrency, including on slow
// storage; the browser never needs that archive to finish before seeing a photo.
func (s *Store) openImageDisplay(ctx context.Context, descriptor ContentDescriptor) (Content, error) {
	if err := ctx.Err(); err != nil {
		return Content{}, err
	}
	s.jobMu.Lock()
	job := s.imageDisplays[descriptor.ID]
	if job == nil {
		if len(s.imageDisplays) >= 4 {
			s.jobMu.Unlock()
			return Content{}, ErrPreviewPending
		}
		job = &imageDisplayJob{ready: make(chan struct{})}
		s.imageDisplays[descriptor.ID] = job
		go s.prepareImageDisplay(descriptor, job)
	}
	s.jobMu.Unlock()
	select {
	case <-ctx.Done():
		return Content{}, ctx.Err()
	case <-job.ready:
	}
	if job.err != nil {
		if errors.Is(job.err, ErrImageOriginalPreferred) {
			return s.OpenDescriptor(ctx, descriptor, "", "original")
		}
		return Content{}, errors.Join(ErrStorageUnavailable, job.err)
	}
	return Content{Body: io.NopCloser(bytes.NewReader(job.data)), StatusCode: http.StatusOK,
		MimeType: "image/jpeg", Filename: descriptor.Filename, ContentLength: int64(len(job.data))}, nil
}

func (s *Store) prepareImageDisplay(descriptor ContentDescriptor, job *imageDisplayJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	defer func() {
		s.jobMu.Lock()
		delete(s.imageDisplays, descriptor.ID)
		s.jobMu.Unlock()
	}()
	select {
	case s.imageRenders <- struct{}{}:
		job.data, job.err = renderImageDisplay(ctx, s.objects, descriptor.ObjectPath)
		<-s.imageRenders
	case <-ctx.Done():
		job.err = ctx.Err()
	}
	close(job.ready)
	if job.err != nil {
		return
	}
	objectPath := imageDisplayPath(descriptor.OwnerID, descriptor.ID, descriptor.CreatedText)
	if err := s.objects.Put(ctx, objectPath, bytes.NewReader(job.data), int64(len(job.data))); err != nil {
		s.cleanupImageDisplayArchive(descriptor, objectPath)
		return // The displayed bytes remain valid even if archival fails.
	}
	// Serialize final registration with Delete/Purge for this photo. Otherwise
	// a new variant could appear after deletion enumerated the existing ones.
	release, err := s.acquireUploadLock(ctx, "media-lifecycle\x00"+descriptor.ID)
	if err != nil {
		s.cleanupImageDisplayArchive(descriptor, objectPath)
		return
	}
	defer release()
	now := nowText(time.Now().UTC())
	result, err := s.db.ExecContext(ctx, `INSERT INTO media_variants(media_id, kind, object_path, mime_type, size_bytes, created_at, updated_at)
		SELECT id, 'display', ?, 'image/jpeg', ?, ?, ? FROM media WHERE id = ? AND status = 'ready'
		ON CONFLICT(media_id, kind) DO UPDATE SET object_path = excluded.object_path, mime_type = excluded.mime_type, size_bytes = excluded.size_bytes, updated_at = excluded.updated_at`,
		objectPath, int64(len(job.data)), now, now, descriptor.ID)
	if err != nil {
		s.cleanupImageDisplayArchive(descriptor, objectPath)
		return
	}
	if count, _ := result.RowsAffected(); count == 0 {
		// The photo was deleted while archival was in flight. Do not resurrect it.
		s.cleanupImageDisplayArchive(descriptor, objectPath)
	}
}

func (s *Store) cleanupImageDisplayArchive(descriptor ContentDescriptor, objectPath string) {
	if objectPath == descriptor.ObjectPath || objectPath == descriptor.PreviewPath {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var registered int
	// A concurrent successful registration (or ambiguous database response)
	// owns this path and normal media deletion can clean it later.
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_variants v JOIN media m ON m.id = v.media_id
		WHERE v.object_path = ? AND m.status = 'ready'`, objectPath).Scan(&registered); err == nil && registered > 0 {
		return
	}
	// This is only the deterministic derived path, never the user's original.
	// It is safe to discard it even if the database is currently unavailable.
	_ = s.objects.Delete(ctx, objectPath)
}
