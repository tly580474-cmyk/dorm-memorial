package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"testing"
	"time"

	"dorm-memorial/internal/config"
	"dorm-memorial/internal/database"
	"dorm-memorial/internal/identity"
	"dorm-memorial/internal/storage"
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

	createPost := doJSON(t, server.URL+"/api/posts", http.MethodPost, map[string]any{
		"body": "一起搬进宿舍的第一天", "content_date": "2024-09-01", "visibility": "members", "tags": []string{"开学"}, "submit": false,
	}, memberCookie)
	if createPost.StatusCode != http.StatusCreated {
		t.Fatalf("create post status=%d body=%s", createPost.StatusCode, readBody(createPost))
	}
	var createdPost struct {
		Post struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"post"`
	}
	decodeResponse(t, createPost, &createdPost)
	if createdPost.Post.Status != "draft" {
		t.Fatalf("created post status=%q", createdPost.Post.Status)
	}
	submitPost := doJSON(t, server.URL+"/api/posts/"+createdPost.Post.ID+"/submit", http.MethodPost, map[string]any{}, memberCookie)
	if submitPost.StatusCode != http.StatusOK {
		t.Fatalf("submit post status=%d body=%s", submitPost.StatusCode, readBody(submitPost))
	}
	submitPost.Body.Close()
	memberPending := doJSON(t, server.URL+"/api/posts?scope=pending", http.MethodGet, nil, memberCookie)
	if memberPending.StatusCode != http.StatusForbidden {
		t.Fatalf("member pending status=%d", memberPending.StatusCode)
	}
	memberPending.Body.Close()
	moderatePost := doJSON(t, server.URL+"/api/admin/posts/"+createdPost.Post.ID+"/moderate", http.MethodPost, map[string]any{"action": "approve", "note": ""}, adminCookie)
	if moderatePost.StatusCode != http.StatusOK {
		t.Fatalf("moderate post status=%d body=%s", moderatePost.StatusCode, readBody(moderatePost))
	}
	moderatePost.Body.Close()
	likeResponse := doJSON(t, server.URL+"/api/posts/"+createdPost.Post.ID+"/like", http.MethodPost, map[string]any{}, memberCookie)
	if likeResponse.StatusCode != http.StatusOK {
		t.Fatalf("like status=%d body=%s", likeResponse.StatusCode, readBody(likeResponse))
	}
	var likeBody struct {
		Liked     bool `json:"liked"`
		LikeCount int  `json:"like_count"`
	}
	decodeResponse(t, likeResponse, &likeBody)
	if !likeBody.Liked || likeBody.LikeCount != 1 {
		t.Fatalf("like body=%+v", likeBody)
	}
	commentResponse := doJSON(t, server.URL+"/api/posts/"+createdPost.Post.ID+"/comments", http.MethodPost, map[string]any{"body": "接口评论"}, memberCookie)
	if commentResponse.StatusCode != http.StatusCreated {
		t.Fatalf("comment status=%d body=%s", commentResponse.StatusCode, readBody(commentResponse))
	}
	commentResponse.Body.Close()
	commentsResponse := doJSON(t, server.URL+"/api/posts/"+createdPost.Post.ID+"/comments", http.MethodGet, nil, memberCookie)
	if commentsResponse.StatusCode != http.StatusOK {
		t.Fatalf("comments status=%d", commentsResponse.StatusCode)
	}
	var commentsBody struct {
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
	}
	decodeResponse(t, commentsResponse, &commentsBody)
	if len(commentsBody.Comments) != 1 || commentsBody.Comments[0].Body != "接口评论" {
		t.Fatalf("comments=%+v", commentsBody.Comments)
	}
	feedResponse := doJSON(t, server.URL+"/api/posts?scope=feed", http.MethodGet, nil, memberCookie)
	if feedResponse.StatusCode != http.StatusOK {
		t.Fatalf("feed status=%d body=%s", feedResponse.StatusCode, readBody(feedResponse))
	}
	var feedBody struct {
		Posts []struct {
			ID string `json:"id"`
		} `json:"posts"`
	}
	decodeResponse(t, feedResponse, &feedBody)
	if len(feedBody.Posts) != 1 || feedBody.Posts[0].ID != createdPost.Post.ID {
		t.Fatalf("feed=%+v", feedBody.Posts)
	}
	membersResponse := doJSON(t, server.URL+"/api/members", http.MethodGet, nil, memberCookie)
	if membersResponse.StatusCode != http.StatusOK {
		t.Fatalf("members status=%d body=%s", membersResponse.StatusCode, readBody(membersResponse))
	}
	var membersBody struct {
		Members []identity.Member `json:"members"`
	}
	decodeResponse(t, membersResponse, &membersBody)
	if len(membersBody.Members) != 2 {
		t.Fatalf("members=%+v", membersBody.Members)
	}
	guestbookResponse := doJSON(t, server.URL+"/api/guestbook", http.MethodPost, map[string]any{"body": "接口留言", "media_ids": []string{}}, memberCookie)
	if guestbookResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create guestbook status=%d body=%s", guestbookResponse.StatusCode, readBody(guestbookResponse))
	}
	var guestbookBody struct {
		Entry struct {
			ID string `json:"id"`
		} `json:"entry"`
	}
	decodeResponse(t, guestbookResponse, &guestbookBody)
	guestbookList := doJSON(t, server.URL+"/api/guestbook", http.MethodGet, nil, memberCookie)
	if guestbookList.StatusCode != http.StatusOK {
		t.Fatalf("guestbook list status=%d body=%s", guestbookList.StatusCode, readBody(guestbookList))
	}
	var guestbookListBody struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}
	decodeResponse(t, guestbookList, &guestbookListBody)
	if len(guestbookListBody.Entries) != 1 || guestbookListBody.Entries[0].ID != guestbookBody.Entry.ID {
		t.Fatalf("guestbook entries=%+v", guestbookListBody.Entries)
	}
	memberHide := doJSON(t, server.URL+"/api/guestbook/"+guestbookBody.Entry.ID+"/hide", http.MethodPost, map[string]any{}, memberCookie)
	if memberHide.StatusCode != http.StatusForbidden {
		t.Fatalf("member hide dorm guestbook status=%d body=%s", memberHide.StatusCode, readBody(memberHide))
	}
	memberHide.Body.Close()
	adminHide := doJSON(t, server.URL+"/api/guestbook/"+guestbookBody.Entry.ID+"/hide", http.MethodPost, map[string]any{}, adminCookie)
	if adminHide.StatusCode != http.StatusNoContent {
		t.Fatalf("admin hide guestbook status=%d body=%s", adminHide.StatusCode, readBody(adminHide))
	}
	adminHide.Body.Close()
	hiddenGuestbookList := doJSON(t, server.URL+"/api/guestbook?status=hidden", http.MethodGet, nil, adminCookie)
	if hiddenGuestbookList.StatusCode != http.StatusOK {
		t.Fatalf("hidden guestbook list status=%d body=%s", hiddenGuestbookList.StatusCode, readBody(hiddenGuestbookList))
	}
	var hiddenGuestbookBody struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}
	decodeResponse(t, hiddenGuestbookList, &hiddenGuestbookBody)
	if len(hiddenGuestbookBody.Entries) != 1 || hiddenGuestbookBody.Entries[0].ID != guestbookBody.Entry.ID {
		t.Fatalf("hidden guestbook entries=%+v", hiddenGuestbookBody.Entries)
	}
	restoreGuestbook := doJSON(t, server.URL+"/api/guestbook/"+guestbookBody.Entry.ID+"/restore", http.MethodPost, map[string]any{}, adminCookie)
	if restoreGuestbook.StatusCode != http.StatusNoContent {
		t.Fatalf("restore guestbook status=%d body=%s", restoreGuestbook.StatusCode, readBody(restoreGuestbook))
	}
	restoreGuestbook.Body.Close()

	forbidden := doJSON(t, server.URL+"/api/admin/invites", http.MethodPost, map[string]any{"max_uses": 1, "expires_in_hours": 24}, memberCookie)
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("member create invite status=%d", forbidden.StatusCode)
	}
	forbidden.Body.Close()
	secondRegister := doJSON(t, server.URL+"/api/auth/register", http.MethodPost, map[string]any{
		"invite_code": inviteBody.Invites[1].Code, "username": "observer", "email": "observer@example.com", "password": "a-secure-password", "nickname": "旁观室友",
	}, nil)
	if secondRegister.StatusCode != http.StatusCreated {
		t.Fatalf("second register status=%d body=%s", secondRegister.StatusCode, readBody(secondRegister))
	}
	observerCookie := findSessionCookie(secondRegister)
	var secondRegisterBody struct {
		User identity.User `json:"user"`
	}
	decodeResponse(t, secondRegister, &secondRegisterBody)
	startDirect := doJSON(t, server.URL+"/api/messages/conversations/direct", http.MethodPost, map[string]any{"recipient_id": secondRegisterBody.User.ID}, memberCookie)
	if startDirect.StatusCode != http.StatusOK {
		t.Fatalf("start direct status=%d body=%s", startDirect.StatusCode, readBody(startDirect))
	}
	var directBody struct {
		Conversation struct {
			ID string `json:"id"`
		} `json:"conversation"`
	}
	decodeResponse(t, startDirect, &directBody)
	sendDirect := doJSON(t, server.URL+"/api/messages/conversations/"+directBody.Conversation.ID, http.MethodPost, map[string]any{"body": "接口私信"}, memberCookie)
	if sendDirect.StatusCode != http.StatusCreated {
		t.Fatalf("send direct status=%d body=%s", sendDirect.StatusCode, readBody(sendDirect))
	}
	sendDirect.Body.Close()
	observerMessages := doJSON(t, server.URL+"/api/messages/conversations/"+directBody.Conversation.ID, http.MethodGet, nil, observerCookie)
	if observerMessages.StatusCode != http.StatusOK {
		t.Fatalf("observer messages status=%d body=%s", observerMessages.StatusCode, readBody(observerMessages))
	}
	var observerMessagesBody struct {
		Messages []struct {
			Body string `json:"body"`
		} `json:"messages"`
	}
	decodeResponse(t, observerMessages, &observerMessagesBody)
	if len(observerMessagesBody.Messages) != 1 || observerMessagesBody.Messages[0].Body != "接口私信" {
		t.Fatalf("observer messages=%+v", observerMessagesBody.Messages)
	}
	adminDirect := doJSON(t, server.URL+"/api/messages/conversations/"+directBody.Conversation.ID, http.MethodGet, nil, adminCookie)
	if adminDirect.StatusCode != http.StatusForbidden {
		t.Fatalf("admin direct privacy status=%d body=%s", adminDirect.StatusCode, readBody(adminDirect))
	}
	adminDirect.Body.Close()
	observerNotifications := doJSON(t, server.URL+"/api/notifications", http.MethodGet, nil, observerCookie)
	if observerNotifications.StatusCode != http.StatusOK {
		t.Fatalf("observer notifications status=%d body=%s", observerNotifications.StatusCode, readBody(observerNotifications))
	}
	var observerNotificationsBody struct {
		UnreadCount int `json:"unread_count"`
	}
	decodeResponse(t, observerNotifications, &observerNotificationsBody)
	if observerNotificationsBody.UnreadCount == 0 {
		t.Fatal("observer direct notification was not unread")
	}
	readNotifications := doJSON(t, server.URL+"/api/notifications/read-all", http.MethodPost, map[string]any{}, observerCookie)
	if readNotifications.StatusCode != http.StatusNoContent {
		t.Fatalf("read notifications status=%d body=%s", readNotifications.StatusCode, readBody(readNotifications))
	}
	readNotifications.Body.Close()
	observerDelete := doJSON(t, server.URL+"/api/posts/"+createdPost.Post.ID, http.MethodDelete, nil, observerCookie)
	if observerDelete.StatusCode != http.StatusNotFound {
		t.Fatalf("non-author delete published post status=%d body=%s", observerDelete.StatusCode, readBody(observerDelete))
	}
	observerDelete.Body.Close()
	adminDeletePost := doJSON(t, server.URL+"/api/posts/"+createdPost.Post.ID, http.MethodDelete, nil, adminCookie)
	if adminDeletePost.StatusCode != http.StatusNoContent {
		t.Fatalf("admin delete published post status=%d body=%s", adminDeletePost.StatusCode, readBody(adminDeletePost))
	}
	adminDeletePost.Body.Close()

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

func TestRawMediaUploadCanBeAttachedToPost(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "media-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	identities := identity.NewStore(db)
	if _, err := identities.BootstrapAdmin(ctx, "mediaadmin", "mediaadmin@example.com", "correct-horse-battery", "媒体管理员"); err != nil {
		t.Fatal(err)
	}
	objects := &httpTestObjects{values: make(map[string][]byte)}
	cfg := config.Config{Environment: "test", SessionTTL: time.Hour, FrontendDir: filepath.Join(t.TempDir(), "missing")}
	server := httptest.NewServer(New(cfg, db, identities, slog.New(slog.NewTextHandler(io.Discard, nil)), objects).Handler())
	defer server.Close()
	cookie := loginTestUser(t, server.URL, "mediaadmin", "correct-horse-battery")

	payload := []byte("image payload")
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/media/uploads", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(cookie)
	request.Header.Set("Content-Type", "image/png")
	request.Header.Set("X-File-Name", "room.png")
	request.Header.Set("X-Upload-ID", "http-upload-0001")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", response.StatusCode, readBody(response))
	}
	var uploaded struct {
		Media struct {
			ID string `json:"id"`
		} `json:"media"`
	}
	decodeResponse(t, response, &uploaded)
	avatarResponse := doJSON(t, server.URL+"/api/profile/avatar", http.MethodPost, map[string]any{"media_id": uploaded.Media.ID}, cookie)
	if avatarResponse.StatusCode != http.StatusOK {
		t.Fatalf("avatar status=%d body=%s", avatarResponse.StatusCode, readBody(avatarResponse))
	}
	var avatarBody struct {
		User identity.User `json:"user"`
	}
	decodeResponse(t, avatarResponse, &avatarBody)
	if avatarBody.User.AvatarPath != uploaded.Media.ID {
		t.Fatalf("avatar path=%q", avatarBody.User.AvatarPath)
	}
	contentRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/media/"+uploaded.Media.ID+"/content", nil)
	contentRequest.AddCookie(cookie)
	contentResponse, err := http.DefaultClient.Do(contentRequest)
	if err != nil {
		t.Fatal(err)
	}
	contentBytes, _ := io.ReadAll(contentResponse.Body)
	contentResponse.Body.Close()
	if contentResponse.StatusCode != http.StatusOK || !bytes.Equal(contentBytes, payload) || contentResponse.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("content response status=%d type=%q bytes=%q", contentResponse.StatusCode, contentResponse.Header.Get("Content-Type"), contentBytes)
	}
	postResponse := doJSON(t, server.URL+"/api/posts", http.MethodPost, map[string]any{
		"body": "", "visibility": "members", "media_ids": []string{uploaded.Media.ID}, "submit": true,
	}, cookie)
	if postResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create media post status=%d body=%s", postResponse.StatusCode, readBody(postResponse))
	}
	var postBody struct {
		Post struct {
			Status string `json:"status"`
			Media  []struct {
				ID string `json:"id"`
			} `json:"media"`
		} `json:"post"`
	}
	decodeResponse(t, postResponse, &postBody)
	if postBody.Post.Status != "pending" || len(postBody.Post.Media) != 1 || postBody.Post.Media[0].ID != uploaded.Media.ID {
		t.Fatalf("unexpected media post: %+v", postBody.Post)
	}
	deleteResponse := doJSON(t, server.URL+"/api/media/"+uploaded.Media.ID, http.MethodDelete, nil, cookie)
	if deleteResponse.StatusCode != http.StatusConflict {
		t.Fatalf("attached media deletion status=%d body=%s", deleteResponse.StatusCode, readBody(deleteResponse))
	}
	deleteResponse.Body.Close()
	clearAvatarResponse := doJSON(t, server.URL+"/api/profile/avatar", http.MethodDelete, nil, cookie)
	if clearAvatarResponse.StatusCode != http.StatusOK {
		t.Fatalf("clear avatar status=%d body=%s", clearAvatarResponse.StatusCode, readBody(clearAvatarResponse))
	}
	clearAvatarResponse.Body.Close()
}

type httpTestObjects struct{ values map[string][]byte }

func (s *httpTestObjects) Put(_ context.Context, objectPath string, body io.Reader, size int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return io.ErrUnexpectedEOF
	}
	s.values[objectPath] = data
	return nil
}
func (s *httpTestObjects) Open(_ context.Context, objectPath string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.values[objectPath])), nil
}
func (s *httpTestObjects) Stat(_ context.Context, objectPath string) (storage.ObjectInfo, error) {
	data, ok := s.values[objectPath]
	if !ok {
		return storage.ObjectInfo{}, storage.ErrNotFound
	}
	return storage.ObjectInfo{Path: objectPath, Name: path.Base(objectPath), Size: int64(len(data))}, nil
}
func (s *httpTestObjects) Delete(_ context.Context, objectPath string) error {
	if _, ok := s.values[objectPath]; !ok {
		return storage.ErrNotFound
	}
	delete(s.values, objectPath)
	return nil
}
func (*httpTestObjects) Move(context.Context, string, string) error         { return nil }
func (*httpTestObjects) ResolveURL(context.Context, string) (string, error) { return "", nil }

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
