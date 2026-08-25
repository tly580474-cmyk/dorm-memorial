package cache

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"dorm-memorial/internal/storage"
)

type memoryStorage struct {
	mu         sync.Mutex
	values     map[string][]byte
	openCount  int
	rangeCount int
}

func newMemoryStorage() *memoryStorage { return &memoryStorage{values: make(map[string][]byte)} }

func (m *memoryStorage) Put(_ context.Context, path string, body io.Reader, _ int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.values[path] = data
	m.mu.Unlock()
	return nil
}

func (m *memoryStorage) Open(_ context.Context, path string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openCount++
	data, ok := m.values[path]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memoryStorage) OpenRange(_ context.Context, path, value string) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rangeCount++
	data, ok := m.values[path]
	if !ok {
		return nil, storage.ErrNotFound
	}
	if value != "bytes=0-" {
		return nil, errors.New("test upstream only supports a complete range")
	}
	header := make(http.Header)
	header.Set("Accept-Ranges", "bytes")
	header.Set("Content-Range", "bytes 0-"+strconv.Itoa(len(data)-1)+"/"+strconv.Itoa(len(data)))
	return &http.Response{StatusCode: http.StatusPartialContent, Header: header, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
}

func (m *memoryStorage) Stat(_ context.Context, path string) (storage.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.values[path]
	if !ok {
		return storage.ObjectInfo{}, storage.ErrNotFound
	}
	return storage.ObjectInfo{Path: path, Size: int64(len(data))}, nil
}

func (m *memoryStorage) Delete(_ context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.values[path]; !ok {
		return storage.ErrNotFound
	}
	delete(m.values, path)
	return nil
}

func (m *memoryStorage) Move(_ context.Context, from, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[to] = m.values[from]
	delete(m.values, from)
	return nil
}

func (m *memoryStorage) ResolveURL(context.Context, string) (string, error) {
	return "", errors.New("not implemented")
}

func TestPutSeedsCacheAndRangeReadsStayLocal(t *testing.T) {
	upstream := newMemoryStorage()
	cached, err := New(upstream, t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("0123456789")
	if err := cached.Put(context.Background(), "/avatar.jpg", bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	body, err := cached.Open(context.Background(), "/avatar.jpg")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(body)
	body.Close()
	if !bytes.Equal(got, payload) || upstream.openCount != 0 {
		t.Fatalf("cached read=%q upstream opens=%d", got, upstream.openCount)
	}
	response, err := cached.OpenRange(context.Background(), "/avatar.jpg", "bytes=2-5")
	if err != nil {
		t.Fatal(err)
	}
	ranged, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusPartialContent || response.Header.Get("Content-Range") != "bytes 2-5/10" || string(ranged) != "2345" || upstream.openCount != 0 {
		t.Fatalf("range status=%d header=%q body=%q upstream opens=%d", response.StatusCode, response.Header.Get("Content-Range"), ranged, upstream.openCount)
	}
}

func TestCompletedMissIsCachedButInterruptedReadIsNot(t *testing.T) {
	upstream := newMemoryStorage()
	upstream.values["/photo.jpg"] = []byte("complete photo")
	upstream.values["/video.mp4"] = []byte("long video")
	cached, err := New(upstream, t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	body, err := cached.Open(context.Background(), "/photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(body)
	body.Close()
	body, err = cached.Open(context.Background(), "/photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(body)
	body.Close()
	if upstream.openCount != 1 {
		t.Fatalf("completed object upstream opens=%d", upstream.openCount)
	}
	body, err = cached.Open(context.Background(), "/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 2)
	_, _ = body.Read(buffer)
	body.Close()
	body, err = cached.Open(context.Background(), "/video.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(body)
	body.Close()
	if upstream.openCount != 3 {
		t.Fatalf("interrupted object should reopen upstream, opens=%d", upstream.openCount)
	}
}

func TestLRUEvictsOldestObjectAtLimit(t *testing.T) {
	upstream := newMemoryStorage()
	cached, err := New(upstream, t.TempDir(), 6)
	if err != nil {
		t.Fatal(err)
	}
	if err := cached.Put(context.Background(), "/old", bytes.NewReader([]byte("aaaa")), 4); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := cached.Put(context.Background(), "/new", bytes.NewReader([]byte("bbbb")), 4); err != nil {
		t.Fatal(err)
	}
	body, err := cached.Open(context.Background(), "/new")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(body)
	body.Close()
	if upstream.openCount != 0 {
		t.Fatalf("newest object should remain cached, opens=%d", upstream.openCount)
	}
	body, err = cached.Open(context.Background(), "/old")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(body)
	body.Close()
	if upstream.openCount != 1 {
		t.Fatalf("oldest object should be evicted, opens=%d", upstream.openCount)
	}
}

func TestRemoteNotFoundDeleteStillInvalidatesCache(t *testing.T) {
	upstream := newMemoryStorage()
	cached, err := New(upstream, t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := cached.Put(context.Background(), "/gone", bytes.NewReader([]byte("cached")), 6); err != nil {
		t.Fatal(err)
	}
	upstream.mu.Lock()
	delete(upstream.values, "/gone")
	upstream.mu.Unlock()
	if err := cached.Delete(context.Background(), "/gone"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("delete error=%v", err)
	}
	if _, err := cached.Open(context.Background(), "/gone"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("stale cache remained readable: %v", err)
	}
}

func TestCompleteRangeMissWarmsCache(t *testing.T) {
	upstream := newMemoryStorage()
	upstream.values["/history.mp4"] = []byte("complete video")
	cached, err := New(upstream, t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	response, err := cached.OpenRange(context.Background(), "/history.mp4", "bytes=0-")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(response.Body)
	response.Body.Close()
	response, err = cached.OpenRange(context.Background(), "/history.mp4", "bytes=2-4")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(got) != "mpl" || upstream.rangeCount != 1 {
		t.Fatalf("cached range=%q upstream range opens=%d", got, upstream.rangeCount)
	}
}

var _ storage.ObjectStorage = (*memoryStorage)(nil)
var _ storage.RangeStorage = (*memoryStorage)(nil)
