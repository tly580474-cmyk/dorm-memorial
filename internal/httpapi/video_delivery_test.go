package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dorm-memorial/internal/config"
	"dorm-memorial/internal/database"
	"dorm-memorial/internal/identity"
	mediastore "dorm-memorial/internal/media"
	storagecache "dorm-memorial/internal/storage/cache"
)

func videoDeliveryServer(t *testing.T) (*sql.DB, identity.User, *httptest.Server, *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "delivery.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	identities := identity.NewStore(db)
	if _, err := identities.BootstrapAdmin(ctx, "deliveryadmin", "delivery@example.test", "correct-horse-battery", "媒体管理员"); err != nil {
		t.Fatal(err)
	}
	owner, err := identities.Authenticate(ctx, "deliveryadmin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	objects, err := storagecache.New(&httpTestObjects{values: make(map[string][]byte)}, t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	for objectPath, body := range map[string]string{"/original.mp4": "original-content", "/playback.mp4": "web-content"} {
		if err := objects.Put(ctx, objectPath, strings.NewReader(body), int64(len(body))); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO media(id,owner_id,object_path,original_filename,media_type,mime_type,size_bytes,sha256,status,created_at,updated_at)
		VALUES('video-delivery',?,'/original.mp4','memory.mp4','video','video/mp4',16,'source-hash','ready',?,?)`, owner.ID, now, now); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Environment: "test", SessionTTL: time.Hour, FrontendDir: filepath.Join(t.TempDir(), "missing")}
	server := httptest.NewServer(New(cfg, db, identities, slog.New(slog.NewTextHandler(io.Discard, nil)), objects).Handler())
	t.Cleanup(server.Close)
	return db, owner, server, loginTestUser(t, server.URL, "deliveryadmin", "correct-horse-battery")
}

func TestWatchSelectsPreparedVideoAndRevalidatesLegacyFallback(t *testing.T) {
	db, _, server, cookie := videoDeliveryServer(t)
	watchURL := server.URL + "/api/media/video-delivery/content?variant=watch"
	response := doJSON(t, watchURL, http.MethodGet, nil, cookie)
	originalETag := response.Header.Get("ETag")
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Media-Variant") != "original" || response.Header.Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("legacy response: %d %v", response.StatusCode, response.Header)
	}
	if body := readBody(response); body != "original-content" {
		t.Fatalf("legacy body=%q", body)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO media_variants(media_id,kind,object_path,mime_type,size_bytes,created_at,updated_at)
		VALUES('video-delivery','playback','/playback.mp4','video/mp4',11,?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, watchURL, nil)
	req.AddCookie(cookie)
	req.Header.Set("If-None-Match", originalETag)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Media-Variant") != "playback" || response.Header.Get("ETag") == originalETag {
		t.Fatalf("new rendition response: %d %v", response.StatusCode, response.Header)
	}
	if body := readBody(response); body != "web-content" {
		t.Fatalf("prepared body=%q", body)
	}
	for _, item := range []struct{ query, want string }{{"variant=watch", "web"}, {"variant=original", "ori"}} {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/media/video-delivery/content?"+item.query, nil)
		req.AddCookie(cookie)
		req.Header.Set("Range", "bytes=0-2")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusPartialContent || response.Header.Get("Accept-Ranges") != "bytes" {
			t.Fatalf("range response: %d %v", response.StatusCode, response.Header)
		}
		if body := readBody(response); body != item.want {
			t.Fatalf("range body=%q, want %q", body, item.want)
		}
	}
	unauthenticated := doJSON(t, watchURL, http.MethodGet, nil, nil)
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", unauthenticated.StatusCode)
	}
	unauthenticated.Body.Close()
	if _, err := db.Exec(`UPDATE media SET status='deleted' WHERE id='video-delivery'`); err != nil {
		t.Fatal(err)
	}
	deleted := doJSON(t, watchURL, http.MethodGet, nil, cookie)
	if deleted.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted=%d", deleted.StatusCode)
	}
	deleted.Body.Close()
}

func TestVideoStatusCanOmitUsageAndReturnsItOnDemand(t *testing.T) {
	db, owner, server, cookie := videoDeliveryServer(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO upload_jobs(id,user_id,client_request_id,object_path,state,expected_size,created_at,updated_at)
		VALUES('delivery-job',?,'delivery-request','/original.mp4','completed',16,?,?)`, owner.ID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO media_processing_jobs(id,media_id,user_id,staging_path,phase,created_at,updated_at)
		VALUES('delivery-job','video-delivery',?,'','completed',?,?)`, owner.ID, now, now); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"?include_usage=0", ""} {
		response := doJSON(t, server.URL+"/api/media-upload-jobs/delivery-job"+query, http.MethodGet, nil, cookie)
		if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d headers=%v", response.StatusCode, response.Header)
		}
		var body map[string]json.RawMessage
		decodeResponse(t, response, &body)
		_, hasUsage := body["usage"]
		if hasUsage != (query == "") || body["job"] == nil {
			t.Fatalf("response fields=%v", body)
		}
	}
}

func TestOnlyPendingVideoErrorsAdvertisePreparation(t *testing.T) {
	for _, pending := range []bool{true, false} {
		recorder := httptest.NewRecorder()
		err := mediastore.ErrStorageUnavailable
		if pending {
			err = mediastore.ErrPreviewPending
		}
		writeMediaError(recorder, err)
		if recorder.Code != http.StatusServiceUnavailable || (recorder.Header().Get("X-Media-State") == "preparing") != pending {
			t.Fatalf("pending=%t status=%d headers=%v", pending, recorder.Code, recorder.Header())
		}
	}
}
