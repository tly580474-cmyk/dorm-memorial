package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"dorm-memorial/internal/storage"
)

// A successful Put/Stat does not prove the remote bytes match the uploaded
// image. Read through the storage adapter, bypassing the upload's local cache.
func verifyImageIntegrity(ctx context.Context, objects storage.ObjectStorage, objectPath string, expectedSize int64, expectedSHA256 string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var lastErr error
	for _, delay := range []time.Duration{0, time.Second, 2 * time.Second} {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		open := objects.Open
		if uncached, ok := objects.(storage.UncachedStorage); ok {
			open = uncached.OpenUncached
		}
		body, err := open(ctx, objectPath)
		if err != nil {
			lastErr = err
			continue
		}
		hasher := sha256.New()
		n, err := io.Copy(hasher, io.LimitReader(body, expectedSize+1))
		closeErr := body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if closeErr != nil {
			lastErr = closeErr
			continue
		}
		if n != expectedSize || hex.EncodeToString(hasher.Sum(nil)) != expectedSHA256 {
			return fmt.Errorf("remote image content does not match upload: %w", ErrStorageUnavailable)
		}
		return nil
	}
	return fmt.Errorf("read remote image for integrity verification: %w", lastErr)
}
