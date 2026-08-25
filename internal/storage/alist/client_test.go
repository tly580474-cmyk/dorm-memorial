package alist

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dorm-memorial/internal/storage"
)

func TestPutStreamsBodyWithContentLength(t *testing.T) {
	t.Parallel()
	payload := "streamed without buffering"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/fs/put" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.ContentLength != int64(len(payload)) {
			t.Fatalf("content length = %d", r.ContentLength)
		}
		if r.Header.Get("Authorization") != "test-token" {
			t.Fatal("authorization header missing")
		}
		filePath, err := url.PathUnescape(r.Header.Get("File-Path"))
		if err != nil || filePath != "/dorm-memorial/originals/u1/a.txt" {
			t.Fatalf("file path = %q, err = %v", filePath, err)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != payload {
			t.Fatalf("body = %q", body)
		}
		writeOK(w, nil)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	reader := &closeTrackingReader{Reader: strings.NewReader(payload)}
	if err := client.Put(context.Background(), "/originals/u1/a.txt", reader, int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	if reader.closed {
		t.Fatal("Put closed a reader owned by the caller")
	}
}

func TestRejectsTraversalBeforeRequest(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, "http://127.0.0.1:1")
	for _, objectPath := range []string{"/../secret", "/a/../../secret", `/a\secret`} {
		err := client.Put(context.Background(), objectPath, strings.NewReader("x"), 1)
		if !errors.Is(err, storage.ErrInvalidPath) {
			t.Fatalf("path %q: expected ErrInvalidPath, got %v", objectPath, err)
		}
	}
}

func TestStatAndRangeDownload(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(nil)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/fs/get":
			writeOK(w, map[string]any{
				"name": "clip.mp4", "size": 6, "is_dir": false,
				"modified":  "2026-08-25T10:00:00Z",
				"raw_url":   server.URL + "/raw/clip.mp4",
				"hash_info": map[string]string{"sha256": "abc123"},
			})
		case "/raw/clip.mp4":
			if r.Header.Get("Range") != "bytes=1-3" {
				t.Fatalf("range = %q", r.Header.Get("Range"))
			}
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "bcd")
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()

	client := newTestClient(t, server.URL)
	info, err := client.Stat(context.Background(), "/originals/u1/clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "clip.mp4" || info.Size != 6 || info.Hash != "abc123" {
		t.Fatalf("unexpected info: %+v", info)
	}
	resp, err := client.OpenRange(context.Background(), "/originals/u1/clip.mp4", "bytes=1-3")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusPartialContent || string(body) != "bcd" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
}

func TestMoveAndDeletePayloads(t *testing.T) {
	t.Parallel()
	var endpoints []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoints = append(endpoints, r.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		writeOK(w, nil)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	if err := client.Move(context.Background(), "/originals/a.txt", "/previews/b.txt"); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(context.Background(), "/previews/b.txt"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/api/fs/move", "/api/fs/rename", "/api/fs/remove"}
	if strings.Join(endpoints, ",") != strings.Join(want, ",") {
		t.Fatalf("endpoints = %v", endpoints)
	}
}

func TestMapsAuthorizationFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL)
	err := client.Put(context.Background(), "/a.txt", strings.NewReader("x"), 1)
	if !errors.Is(err, storage.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestAuthenticateReplacesStaleToken(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			if r.Header.Get("Authorization") != "" {
				t.Fatal("login request must not send a stale token")
			}
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["username"] != "dorm-memorial" || payload["password"] != "secret" {
				t.Fatalf("unexpected login payload: %#v", payload)
			}
			writeOK(w, map[string]string{"token": "fresh-token"})
		case "/api/fs/put":
			if r.Header.Get("Authorization") != "fresh-token" {
				t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
			}
			_, _ = io.Copy(io.Discard, r.Body)
			writeOK(w, nil)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(Config{
		BaseURL:  server.URL,
		Token:    "stale-token",
		Username: "dorm-memorial",
		Password: "secret",
		Root:     "/dorm-memorial",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.Put(context.Background(), "/probe.txt", strings.NewReader("x"), 1); err != nil {
		t.Fatal(err)
	}
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := New(Config{BaseURL: baseURL, Token: "test-token", Root: "/dorm-memorial"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
}

type closeTrackingReader struct {
	*strings.Reader
	closed bool
}

func (r *closeTrackingReader) Close() error {
	r.closed = true
	return nil
}
