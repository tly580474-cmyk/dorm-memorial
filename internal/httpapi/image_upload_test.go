package httpapi

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"dorm-memorial/internal/config"
	"dorm-memorial/internal/database"
	"dorm-memorial/internal/identity"
)

func TestImageUploadLimitsAndActualContentValidation(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "image-upload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	identities := identity.NewStore(db)
	if _, err := identities.BootstrapAdmin(ctx, "imageadmin", "imageadmin@example.com", "correct-horse-battery", "图片管理员"); err != nil {
		t.Fatal(err)
	}
	objects := &httpTestObjects{values: make(map[string][]byte)}
	cfg := config.Config{Environment: "test", SessionTTL: time.Hour, MaxImageUploadBytes: 1024, MaxVideoUploadBytes: 300 << 20, FrontendDir: filepath.Join(t.TempDir(), "missing")}
	server := httptest.NewServer(New(cfg, db, identities, slog.New(slog.NewTextHandler(io.Discard, nil)), objects).Handler())
	defer server.Close()
	unauthorized := doJSON(t, server.URL+"/api/media/limits", http.MethodGet, nil, nil)
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("limits without login=%d", unauthorized.StatusCode)
	}
	unauthorized.Body.Close()
	cookie := loginTestUser(t, server.URL, "imageadmin", "correct-horse-battery")
	var limits struct {
		MaxImageBytes  int64    `json:"max_image_upload_bytes"`
		MaxImagePixels int64    `json:"max_image_pixels"`
		MIMETypes      []string `json:"supported_image_mime_types"`
	}
	response := doJSON(t, server.URL+"/api/media/limits", http.MethodGet, nil, cookie)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("limits status=%d", response.StatusCode)
	}
	decodeResponse(t, response, &limits)
	if limits.MaxImageBytes != cfg.MaxImageUploadBytes || limits.MaxImagePixels != 24_000_000 || len(limits.MIMETypes) != 4 {
		t.Fatalf("limits=%+v", limits)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, mime string
		payload    []byte
		status     int
	}{
		{"forged", "image/png", []byte("not an image"), http.StatusBadRequest},
		{"truncated", "image/png", encoded.Bytes()[:encoded.Len()/2], http.StatusBadRequest},
		{"mime-mismatch", "image/jpeg", encoded.Bytes(), http.StatusBadRequest},
		{"svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), http.StatusBadRequest},
		{"too-large", "image/png", make([]byte, 1025), http.StatusRequestEntityTooLarge},
		{"valid", "image/png", encoded.Bytes(), http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodPost, server.URL+"/api/media/uploads", bytes.NewReader(test.payload))
			if err != nil {
				t.Fatal(err)
			}
			r.AddCookie(cookie)
			r.Header.Set("Content-Type", test.mime)
			r.Header.Set("X-File-Name", "picture.png")
			r.Header.Set("X-Upload-ID", "image-test-"+test.name)
			response, err := http.DefaultClient.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.StatusCode, test.status, readBody(response))
			}
		})
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM media WHERE status='ready'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ready images=%d err=%v", count, err)
	}
}
