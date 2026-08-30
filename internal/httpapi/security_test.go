package httpapi

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mediastore "dorm-memorial/internal/media"
	"dorm-memorial/internal/validation"
)

func TestOversizedAudioIsRejectedBeforeReadingBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/media/uploads", strings.NewReader(""))
	req.Header.Set("Content-Type", "audio/webm")
	req.ContentLength = mediastore.MaxAudioUploadBytes + 1
	response := httptest.NewRecorder()
	(&Server{}).uploadMedia(response, req)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized audio accepted: %d", response.Code)
	}
}

func TestInternalErrorsAreNotDisclosed(t *testing.T) {
	for name, write := range map[string]func(http.ResponseWriter, error){"identity": writeIdentityError, "content": writeContentError, "messaging": writeMessagingError} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			write(response, errors.New("database /private/app.db: SELECT password_hash FROM users"))
			if response.Code != 500 || strings.Contains(response.Body.String(), "password_hash") || strings.Contains(response.Body.String(), "app.db") {
				t.Fatalf("internal error leaked: %d %s", response.Code, response.Body.String())
			}
			response = httptest.NewRecorder()
			write(response, fmt.Errorf("private wrapper: %w", validation.New("invalid input")))
			if response.Code != 400 || !strings.Contains(response.Body.String(), "invalid input") || strings.Contains(response.Body.String(), "private wrapper") {
				t.Fatalf("validation mapping failed: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestCrossOriginWritesAreRejectedWithOriginFallback(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := s.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, test := range []struct {
		name, origin, site string
		want               int
	}{
		{"same origin", "https://community.test", "same-origin", 204},
		{"cross site", "https://evil.test", "cross-site", 403},
		{"sibling origin", "https://evil.community.test", "same-site", 403},
		{"old browser cross origin", "https://evil.test", "", 403},
		{"old browser same origin", "https://community.test", "", 204},
		{"non browser", "", "", 204},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://community.test/api/auth/login", nil)
			req.Header.Set("Origin", test.origin)
			req.Header.Set("Sec-Fetch-Site", test.site)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != test.want {
				t.Fatalf("status=%d, want=%d", recorder.Code, test.want)
			}
		})
	}
}

func TestCrossOriginMediaReadsCannotTriggerPreparation(t *testing.T) {
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := s.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, site := range []string{"cross-site", "same-site"} {
			req := httptest.NewRequest(method, "https://community.test/api/media/id/content?variant=playback", nil)
			req.Header.Set("Sec-Fetch-Site", site)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusForbidden {
				t.Fatalf("%s %s triggered media work: %d", method, site, response.Code)
			}
		}
	}
	for _, origin := range []string{"https://evil.test", "null"} {
		req := httptest.NewRequest(http.MethodGet, "https://community.test/api/media/id/content", nil)
		req.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Fatalf("legacy cross-origin read accepted: %s", origin)
		}
	}
}

func TestAuthLimiterCannotBeBypassedWithForwardedHeaders(t *testing.T) {
	limiter := &authLimiter{slots: make(chan struct{}, 2)}
	called := 0
	handler := limiter.protect(func(w http.ResponseWriter, _ *http.Request) { called++; w.WriteHeader(http.StatusNoContent) })
	for attempt := 0; attempt <= authAttemptLimit; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("X-Forwarded-For", strings.Repeat("1", attempt)+".test")
		response := httptest.NewRecorder()
		handler(response, req)
		if attempt == authAttemptLimit && (response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "") {
			t.Fatalf("attempt limit not enforced: %d %v", response.Code, response.Header())
		}
	}
	if called != authAttemptLimit {
		t.Fatalf("admitted %d attempts", called)
	}
	if !limiter.allow("192.0.2.2", time.Now()) {
		t.Fatal("another peer was blocked")
	}
	if !limiter.allow("192.0.2.1", time.Now().Add(authAttemptWindow+time.Second)) {
		t.Fatal("expired limit did not recover")
	}
}

func TestJSONContentTypeIsParsedExactly(t *testing.T) {
	for _, contentType := range []string{"application/jsonp", "application/json-evil", "text/plain"} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", contentType)
		response := httptest.NewRecorder()
		var body struct{}
		if decodeJSON(response, req, &body) || response.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("accepted %q", contentType)
		}
	}
}

func TestPrivateMediaRevalidatesAndUnauthorizedResponsesAreNotCached(t *testing.T) {
	_, _, server, cookie := videoDeliveryServer(t)
	url := server.URL + "/api/media/video-delivery/content"
	response := doJSON(t, url, http.MethodGet, nil, cookie)
	etag := response.Header.Get("ETag")
	if response.Header.Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("private media cache=%q", response.Header.Get("Cache-Control"))
	}
	response.Body.Close()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("If-None-Match", etag)
	unauthorized, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized || unauthorized.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthorized cache response: %d %v", unauthorized.StatusCode, unauthorized.Header)
	}
}
