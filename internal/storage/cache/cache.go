package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dorm-memorial/internal/storage"
)

type flight struct {
	done       chan struct{}
	generation uint64
}

// Storage keeps fully written or fully consumed objects on local disk. Partial
// reads never become cache entries, so an interrupted transfer cannot poison
// later requests.
type Storage struct {
	upstream storage.ObjectStorage
	dir      string
	maxBytes int64
	mu       sync.Mutex
	flights  map[string]*flight
	// generations changes whenever a caller mutates an object. A completed
	// read from an older generation must never overwrite the cache after Put,
	// Delete, or Move has started.
	generations map[string]uint64
}

func New(upstream storage.ObjectStorage, dir string, maxBytes int64) (*Storage, error) {
	if upstream == nil {
		return nil, fmt.Errorf("cache upstream is required")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("cache size must be positive")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve cache directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("create cache directory: %w", err)
	}
	c := &Storage{upstream: upstream, dir: abs, maxBytes: maxBytes, flights: make(map[string]*flight), generations: make(map[string]uint64)}
	c.removeOrphanTemps()
	c.prune()
	return c, nil
}

func (c *Storage) Put(ctx context.Context, objectPath string, body io.Reader, size int64) error {
	c.invalidate(objectPath)
	if size < 0 || size > c.maxBytes {
		err := c.upstream.Put(ctx, objectPath, body, size)
		c.invalidate(objectPath)
		return err
	}
	temp, err := os.CreateTemp(c.dir, c.key(objectPath)+"-*.tmp")
	if err != nil {
		err = c.upstream.Put(ctx, objectPath, body, size)
		c.invalidate(objectPath)
		return err
	}
	tempName := temp.Name()
	written := int64(0)
	cacheFailed := false
	tee := io.TeeReader(body, writerFunc(func(p []byte) (int, error) {
		if cacheFailed {
			return len(p), nil
		}
		n, writeErr := temp.Write(p)
		written += int64(n)
		if writeErr != nil || n != len(p) {
			cacheFailed = true
		}
		// Local cache failures must not turn a valid remote upload into a
		// failed application upload.
		return len(p), nil
	}))
	upstreamErr := c.upstream.Put(ctx, objectPath, tee, size)
	closeErr := temp.Close()
	if upstreamErr != nil || cacheFailed || closeErr != nil || written != size {
		_ = os.Remove(tempName)
		c.invalidate(objectPath)
		return upstreamErr
	}
	// Invalidate again after the remote write. Reads that started while Put was
	// in flight belong to the previous generation and must not win the cache
	// race after this upload completes.
	generation := c.invalidate(objectPath)
	if err := c.commit(objectPath, generation, tempName, c.path(objectPath)); err != nil {
		_ = os.Remove(tempName)
	}
	return nil
}

func (c *Storage) Open(ctx context.Context, objectPath string) (io.ReadCloser, error) {
	for {
		if file, ok := c.openCached(objectPath); ok {
			storage.Describe(ctx, "cache", "hit")
			return file, nil
		}
		storage.Describe(ctx, "cache", "miss")
		owner, current := c.beginFlight(objectPath)
		if owner {
			body, err := c.upstream.Open(ctx, objectPath)
			if err != nil {
				c.finishFlight(objectPath, current)
				return nil, err
			}
			temp, err := os.CreateTemp(c.dir, c.key(objectPath)+"-*.tmp")
			if err != nil {
				c.finishFlight(objectPath, current)
				return body, nil
			}
			return &fillReader{source: body, temp: temp, cache: c, objectPath: objectPath, current: current, generation: current.generation}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-current.done:
		}
	}
}

func (c *Storage) IsCached(objectPath string) bool {
	file, ok := c.openCached(objectPath)
	if ok {
		file.Close()
	}
	return ok
}

func (c *Storage) Warm(ctx context.Context, objectPath string) error {
	if c.IsCached(objectPath) {
		return nil
	}
	body, err := c.Open(ctx, objectPath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(io.Discard, body)
	closeErr := body.Close()
	return errors.Join(copyErr, closeErr)
}

func (c *Storage) Stat(ctx context.Context, objectPath string) (storage.ObjectInfo, error) {
	return c.upstream.Stat(ctx, objectPath)
}

func (c *Storage) OpenUncached(ctx context.Context, objectPath string) (io.ReadCloser, error) {
	if upstream, ok := c.upstream.(storage.UncachedStorage); ok {
		return upstream.OpenUncached(ctx, objectPath)
	}
	return c.upstream.Open(ctx, objectPath)
}

func (c *Storage) Delete(ctx context.Context, objectPath string) error {
	c.invalidate(objectPath)
	err := c.upstream.Delete(ctx, objectPath)
	// A failed or ambiguous delete can still have reached the provider. Keep
	// any read that overlapped the operation from publishing stale bytes.
	c.invalidate(objectPath)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	return err
}

func (c *Storage) Move(ctx context.Context, from, to string) error {
	c.invalidate(from)
	c.invalidate(to)
	err := c.upstream.Move(ctx, from, to)
	c.invalidate(from)
	c.invalidate(to)
	if err != nil {
		return err
	}
	return nil
}

func (c *Storage) ResolveURL(ctx context.Context, objectPath string) (string, error) {
	return c.upstream.ResolveURL(ctx, objectPath)
}

func (c *Storage) RefreshDirectory(ctx context.Context, objectPath string) error {
	if refresher, ok := c.upstream.(storage.DirectoryRefresher); ok {
		return refresher.RefreshDirectory(ctx, objectPath)
	}
	return nil
}

func (c *Storage) OpenRange(ctx context.Context, objectPath, byteRange string) (*http.Response, error) {
	if file, ok := c.openCached(objectPath); ok {
		storage.Describe(ctx, "cache", "hit")
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, err
		}
		start, length, status, contentRange, err := parseRange(byteRange, info.Size())
		if err != nil {
			file.Close()
			return nil, err
		}
		body := io.NopCloser(io.NewSectionReader(file, start, length))
		body = &sectionBody{Reader: body, file: file}
		header := make(http.Header)
		header.Set("Accept-Ranges", "bytes")
		if contentRange != "" {
			header.Set("Content-Range", contentRange)
		}
		return &http.Response{StatusCode: status, Header: header, Body: body, ContentLength: length}, nil
	}
	storage.Describe(ctx, "cache", "miss")
	if ranged, ok := c.upstream.(storage.RangeStorage); ok {
		response, err := ranged.OpenRange(ctx, objectPath, byteRange)
		if err != nil {
			return nil, err
		}
		if total, ok := completeRangeSize(response); ok && total <= c.maxBytes {
			owner, current := c.beginFlight(objectPath)
			if owner {
				temp, tempErr := os.CreateTemp(c.dir, c.key(objectPath)+"-*.tmp")
				if tempErr == nil {
					response.Body = &fillReader{source: response.Body, temp: temp, cache: c, objectPath: objectPath, current: current, generation: current.generation}
				} else {
					c.finishFlight(objectPath, current)
				}
			}
		}
		return response, nil
	}
	body, err := c.Open(ctx, objectPath)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Accept-Ranges": []string{"bytes"}}, Body: body, ContentLength: -1}, nil
}

type fillReader struct {
	source     io.ReadCloser
	temp       *os.File
	cache      *Storage
	objectPath string
	current    *flight
	generation uint64
	written    int64
	done       bool
}

func (r *fillReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if n > 0 && r.temp != nil {
		written, writeErr := r.temp.Write(p[:n])
		r.written += int64(written)
		if writeErr != nil || written != n || r.written > r.cache.maxBytes {
			r.abortCache()
		}
	}
	if err == io.EOF {
		r.completeCache()
	}
	return n, err
}

func (r *fillReader) Close() error {
	if !r.done {
		r.abortCache()
	}
	return r.source.Close()
}

func (r *fillReader) completeCache() {
	if r.done {
		return
	}
	r.done = true
	if r.temp != nil {
		name := r.temp.Name()
		if err := r.temp.Close(); err == nil {
			if info, statErr := os.Stat(name); statErr == nil && info.Size() <= r.cache.maxBytes {
				if r.cache.commit(r.objectPath, r.generation, name, r.cache.path(r.objectPath)) == nil {
					r.temp = nil
				}
			}
		}
		if r.temp != nil {
			_ = os.Remove(name)
		}
	}
	r.cache.finishFlight(r.objectPath, r.current)
}

func (r *fillReader) abortCache() {
	if r.done {
		return
	}
	r.done = true
	if r.temp != nil {
		name := r.temp.Name()
		_ = r.temp.Close()
		_ = os.Remove(name)
		r.temp = nil
	}
	r.cache.finishFlight(r.objectPath, r.current)
}

func (c *Storage) openCached(objectPath string) (*os.File, bool) {
	path := c.path(objectPath)
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return file, true
}

func (c *Storage) beginFlight(objectPath string) (bool, *flight) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if current := c.flights[objectPath]; current != nil {
		return false, current
	}
	current := &flight{done: make(chan struct{}), generation: c.generations[objectPath]}
	c.flights[objectPath] = current
	return true, current
}

func (c *Storage) finishFlight(objectPath string, current *flight) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flights[objectPath] == current {
		delete(c.flights, objectPath)
		close(current.done)
	}
}

var errStaleCache = errors.New("stale cache generation")

func (c *Storage) commit(objectPath string, generation uint64, tempName, finalName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generations[objectPath] != generation {
		return errStaleCache
	}
	_ = os.Remove(finalName)
	if err := os.Rename(tempName, finalName); err != nil {
		return err
	}
	now := time.Now()
	_ = os.Chtimes(finalName, now, now)
	c.pruneLocked()
	return nil
}

func (c *Storage) invalidate(objectPath string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = os.Remove(c.path(objectPath))
	c.generations[objectPath]++
	return c.generations[objectPath]
}

// A process can terminate between CreateTemp and commit. Remove only the
// cache's own temporary files at startup; active transfers are not present yet.
func (c *Storage) removeOrphanTemps() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !isCacheTempName(entry.Name()) {
			continue
		}
		_ = os.Remove(filepath.Join(c.dir, entry.Name()))
	}
}

func isCacheTempName(name string) bool {
	if len(name) <= 65 || name[64] != '-' || !strings.HasSuffix(name, ".tmp") {
		return false
	}
	for _, value := range name[:64] {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return false
		}
	}
	return true
}

func (c *Storage) prune() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
}

func (c *Storage) pruneLocked() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	type item struct {
		path string
		size int64
		used time.Time
	}
	items := make([]item, 0, len(entries))
	total := int64(0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".data") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		total += info.Size()
		items = append(items, item{path: filepath.Join(c.dir, entry.Name()), size: info.Size(), used: info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].used.Before(items[j].used) })
	for _, cached := range items {
		if total <= c.maxBytes {
			break
		}
		if os.Remove(cached.path) == nil {
			total -= cached.size
		}
	}
}

func (c *Storage) key(objectPath string) string {
	sum := sha256.Sum256([]byte(objectPath))
	return hex.EncodeToString(sum[:])
}

func (c *Storage) path(objectPath string) string {
	return filepath.Join(c.dir, c.key(objectPath)+".data")
}

func parseRange(value string, size int64) (start, length int64, status int, contentRange string, err error) {
	if value == "" {
		return 0, size, http.StatusOK, "", nil
	}
	prefix, spec, ok := strings.Cut(value, "=")
	if !ok || prefix != "bytes" || strings.Contains(spec, ",") {
		return 0, 0, 0, "", fmt.Errorf("unsupported byte range")
	}
	left, right, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, 0, "", fmt.Errorf("invalid byte range")
	}
	if left == "" {
		suffix, parseErr := strconv.ParseInt(right, 10, 64)
		if parseErr != nil || suffix <= 0 {
			return 0, 0, 0, "", fmt.Errorf("invalid byte range")
		}
		length = min(suffix, size)
		start = size - length
	} else {
		start, err = strconv.ParseInt(left, 10, 64)
		if err != nil || start < 0 || start >= size {
			return 0, 0, 0, "", fmt.Errorf("invalid byte range")
		}
		end := size - 1
		if right != "" {
			end, err = strconv.ParseInt(right, 10, 64)
			if err != nil || end < start {
				return 0, 0, 0, "", fmt.Errorf("invalid byte range")
			}
			end = min(end, size-1)
		}
		length = end - start + 1
	}
	return start, length, http.StatusPartialContent, fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, size), nil
}

func completeRangeSize(response *http.Response) (int64, bool) {
	value := response.Header.Get("Content-Range")
	unit, spec, ok := strings.Cut(value, " ")
	if !ok || unit != "bytes" {
		return 0, false
	}
	span, totalText, ok := strings.Cut(spec, "/")
	if !ok {
		return 0, false
	}
	startText, endText, ok := strings.Cut(span, "-")
	if !ok || startText != "0" {
		return 0, false
	}
	end, endErr := strconv.ParseInt(endText, 10, 64)
	total, totalErr := strconv.ParseInt(totalText, 10, 64)
	if endErr != nil || totalErr != nil || total <= 0 || end != total-1 || response.ContentLength != total {
		return 0, false
	}
	return total, true
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

type sectionBody struct {
	io.Reader
	file *os.File
}

func (b *sectionBody) Close() error { return b.file.Close() }
