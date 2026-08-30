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
		return // The displayed bytes remain valid even if archival fails.
	}
	now := nowText(time.Now().UTC())
	result, err := s.db.ExecContext(ctx, `INSERT INTO media_variants(media_id, kind, object_path, mime_type, size_bytes, created_at, updated_at)
		SELECT id, 'display', ?, 'image/jpeg', ?, ?, ? FROM media WHERE id = ? AND status = 'ready'
		ON CONFLICT(media_id, kind) DO UPDATE SET object_path = excluded.object_path, mime_type = excluded.mime_type, size_bytes = excluded.size_bytes, updated_at = excluded.updated_at`,
		objectPath, int64(len(job.data)), now, now, descriptor.ID)
	if err == nil {
		if count, _ := result.RowsAffected(); count == 0 {
			// The photo was deleted while archival was in flight. Do not resurrect it.
			_ = s.objects.Delete(ctx, objectPath)
		}
	}
}
