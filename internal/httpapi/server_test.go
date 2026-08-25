package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestInviteRegistrationSessionAndPermissions(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := identity.NewStore(db)
	created, err := store.BootstrapAdmin(ctx, "admin", "admin@example.com", "correct-horse-battery", "管理员")
	if err != nil || !created {
		t.Fatalf("bootstrap created=%v err=%v", created, err)
	}
	cfg := config.Config{Environment: "test", CookieSecure: false, SessionTTL: 24 * time.Hour, FrontendDir: filepath.Join(t.TempDir(), "missing")}
	server := httptest.NewServer(New(cfg, db, store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer server.Close()

	adminCookie := loginTestUser(t, server.URL, "admin", "correct-horse-battery")
	inviteResponse := doJSON(t, server.URL+"/api/admin/invites", http.MethodPost, map[string]any{"max_uses": 1, "expires_in_hours": 24, "count": 3}, adminCookie)
	if inviteResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create invite status=%d body=%s", inviteResponse.StatusCode, readBody(inviteResponse))
	}
	var inviteBody struct {
		Invites []struct {
			Code string `json:"code"`
		} `json:"invites"`
	}
	decodeResponse(t, inviteResponse, &inviteBody)
	if len(inviteBody.Invites) != 3 {
		t.Fatalf("invite count=%d", len(inviteBody.Invites))
	}

	registerResponse := doJSON(t, server.URL+"/api/auth/register", http.MethodPost, map[string]any{
		"invite_code": inviteBody.Invites[0].Code, "username": "roommate", "email": "roommate@example.com", "password": "a-secure-password", "nickname": "室友",
	}, nil)
	if registerResponse.StatusCode != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", registerResponse.StatusCode, readBody(registerResponse))
	}
	memberCookie := findSessionCookie(registerResponse)
	registerResponse.Body.Close()
	if memberCookie == nil {
		t.Fatal("register response did not set a session cookie")
	}

	meResponse := doJSON(t, server.URL+"/api/auth/me", http.MethodGet, nil, memberCookie)
	if meResponse.StatusCode != http.StatusOK {
		t.Fatalf("me status=%d body=%s", meResponse.StatusCode, readBody(meResponse))
	}
	meResponse.Body.Close()

	profileResponse := doJSON(t, server.URL+"/api/profile", http.MethodPatch, map[string]any{
		"nickname": "新昵称", "bio": "个人简介", "bed_no": "2", "memorial_note": "纪念寄语",
	}, memberCookie)
	if profileResponse.StatusCode != http.StatusOK {
		t.Fatalf("update profile status=%d body=%s", profileResponse.StatusCode, readBody(profileResponse))
	}
	var profileBody struct {
		User identity.User `json:"user"`
	}
	decodeResponse(t, profileResponse, &profileBody)
	if profileBody.User.BedNo != "2" || profileBody.User.MemorialNote != "纪念寄语" {
		t.Fatalf("updated profile=%+v", profileBody.User)
	}

	forbidden := doJSON(t, server.URL+"/api/admin/invites", http.MethodPost, map[string]any{"max_uses": 1, "expires_in_hours": 24}, memberCookie)
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("member create invite status=%d", forbidden.StatusCode)
	}
	forbidden.Body.Close()

	reuse := doJSON(t, server.URL+"/api/auth/register", http.MethodPost, map[string]any{
		"invite_code": inviteBody.Invites[0].Code, "username": "another", "email": "another@example.com", "password": "a-secure-password", "nickname": "另一位",
	}, nil)
	if reuse.StatusCode != http.StatusBadRequest {
		t.Fatalf("reused invite status=%d", reuse.StatusCode)
	}
	reuse.Body.Close()

	// A new handler backed by the same database must accept the existing cookie.
	restarted := httptest.NewServer(New(cfg, db, identity.NewStore(db), slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer restarted.Close()
	afterRestart := doJSON(t, restarted.URL+"/api/auth/me", http.MethodGet, nil, memberCookie)
	if afterRestart.StatusCode != http.StatusOK {
		t.Fatalf("session after restart status=%d body=%s", afterRestart.StatusCode, readBody(afterRestart))
	}
	afterRestart.Body.Close()
}

func loginTestUser(t *testing.T, baseURL, identifier, password string) *http.Cookie {
	t.Helper()
	response := doJSON(t, baseURL+"/api/auth/login", http.MethodPost, map[string]string{"identifier": identifier, "password": password}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d body=%s", response.StatusCode, readBody(response))
	}
	cookie := findSessionCookie(response)
	response.Body.Close()
	if cookie == nil {
		t.Fatal("login did not set session cookie")
	}
	return cookie
}

func doJSON(t *testing.T, url, method string, body any, cookie *http.Cookie) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func findSessionCookie(response *http.Response) *http.Cookie {
	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionCookie {
			return cookie
		}
	}
	return nil
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func readBody(response *http.Response) string {
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return string(body)
}
