package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

var (
	ErrInvalidPath  = errors.New("invalid object path")
	ErrUnauthorized = errors.New("storage authorization failed")
	ErrNotFound     = errors.New("object not found")
	ErrUnavailable  = errors.New("storage unavailable")
)

type ObjectInfo struct {
	Path     string
	Name     string
	Size     int64
	IsDir    bool
	Modified time.Time
	Hash     string
}

type ObjectStorage interface {
	Put(ctx context.Context, objectPath string, body io.Reader, size int64) error
	Open(ctx context.Context, objectPath string) (io.ReadCloser, error)
	Stat(ctx context.Context, objectPath string) (ObjectInfo, error)
	Delete(ctx context.Context, objectPath string) error
	Move(ctx context.Context, from, to string) error
	ResolveURL(ctx context.Context, objectPath string) (string, error)
}

// RangeStorage is implemented by providers that can preserve upstream byte-range
// responses. Video playback uses it without forcing every storage adapter to
// depend on HTTP semantics.
type RangeStorage interface {
	OpenRange(ctx context.Context, objectPath, byteRange string) (*http.Response, error)
}

type DirectoryRefresher interface {
	RefreshDirectory(ctx context.Context, objectPath string) error
}

// UncachedStorage reads the persisted object rather than the local copy of an
// upload. Integrity checks must not accidentally verify their own input bytes.
type UncachedStorage interface {
	OpenUncached(ctx context.Context, objectPath string) (io.ReadCloser, error)
}

// CacheWarmer is implemented by storage decorators that can populate a local
// cache without tying completion to a browser's short-lived Range request.
type CacheWarmer interface {
	IsCached(objectPath string) bool
	Warm(ctx context.Context, objectPath string) error
}
