package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dorm-memorial/internal/config"
	"dorm-memorial/internal/content"
	"dorm-memorial/internal/identity"
	mediastore "dorm-memorial/internal/media"
	"dorm-memorial/internal/storage"
)

const sessionCookie = "dm_session"

type contextKey string

const principalKey contextKey = "principal"

type principal struct {
	User      identity.User
	SessionID string
}

type Server struct {
	cfg      config.Config
	db       *sql.DB
	identity *identity.Store
	content  *content.Store
	media    *mediastore.Store
	logger   *slog.Logger
	handler  http.Handler
}

func New(cfg config.Config, db *sql.DB, identities *identity.Store, logger *slog.Logger, objects ...storage.ObjectStorage) *Server {
	var objectStore storage.ObjectStorage
	if len(objects) > 0 {
		objectStore = objects[0]
	}
	s := &Server{cfg: cfg, db: db, identity: identities, content: content.NewStore(db), media: mediastore.NewStore(db, objectStore), logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.Handle("POST /api/auth/logout", s.requireAuth(http.HandlerFunc(s.logout)))
	mux.Handle("GET /api/auth/me", s.requireAuth(http.HandlerFunc(s.me)))
	mux.Handle("GET /api/auth/sessions", s.requireAuth(http.HandlerFunc(s.sessions)))
	mux.Handle("DELETE /api/auth/sessions/{id}", s.requireAuth(http.HandlerFunc(s.revokeSession)))
	mux.Handle("PATCH /api/profile", s.requireAuth(http.HandlerFunc(s.updateProfile)))
	mux.Handle("POST /api/profile/avatar", s.requireAuth(http.HandlerFunc(s.updateAvatar)))
	mux.Handle("DELETE /api/profile/avatar", s.requireAuth(http.HandlerFunc(s.clearAvatar)))
	mux.Handle("POST /api/admin/invites", s.requireAuth(http.HandlerFunc(s.createInvite)))
	mux.Handle("GET /api/admin/health", s.requireAuth(http.HandlerFunc(s.adminHealth)))
	mux.Handle("GET /api/posts", s.requireAuth(http.HandlerFunc(s.listPosts)))
	mux.Handle("POST /api/posts", s.requireAuth(http.HandlerFunc(s.createPost)))
	mux.Handle("GET /api/posts/{id}", s.requireAuth(http.HandlerFunc(s.getPost)))
	mux.Handle("PATCH /api/posts/{id}", s.requireAuth(http.HandlerFunc(s.updatePost)))
	mux.Handle("DELETE /api/posts/{id}", s.requireAuth(http.HandlerFunc(s.deletePost)))
	mux.Handle("POST /api/posts/{id}/submit", s.requireAuth(http.HandlerFunc(s.submitPost)))
	mux.Handle("GET /api/posts/{id}/comments", s.requireAuth(http.HandlerFunc(s.listComments)))
	mux.Handle("POST /api/posts/{id}/comments", s.requireAuth(http.HandlerFunc(s.addComment)))
	mux.Handle("POST /api/posts/{id}/like", s.requireAuth(http.HandlerFunc(s.toggleLike)))
	mux.Handle("DELETE /api/comments/{id}", s.requireAuth(http.HandlerFunc(s.deleteComment)))
	mux.Handle("POST /api/admin/posts/{id}/moderate", s.requireAuth(http.HandlerFunc(s.moderatePost)))
	mux.Handle("POST /api/media/uploads", s.requireAuth(http.HandlerFunc(s.uploadMedia)))
	mux.Handle("GET /api/media/usage", s.requireAuth(http.HandlerFunc(s.mediaUsage)))
	mux.Handle("DELETE /api/media/{id}", s.requireAuth(http.HandlerFunc(s.deleteMedia)))
	mux.Handle("GET /api/media/{id}/content", s.requireAuth(http.HandlerFunc(s.mediaContent)))
	mux.Handle("/", s.frontend())
	s.handler = s.middleware(mux)
	return s
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, http.StatusForbidden, "cross-site request rejected")
			return
		}
		next.ServeHTTP(w, r)
		s.logger.Info("http_request", "method", r.Method, "path", r.URL.Path, "elapsed_ms", time.Since(started).Milliseconds(), "remote_ip", remoteIP(r))
	})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		user, sessionID, err := s.identity.UserForToken(r.Context(), cookie.Value)
		if err != nil {
			clearSessionCookie(w, s.cfg.CookieSecure)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, principal{User: user, SessionID: sessionID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "dorm-memorial"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var writable int
	if err := s.db.QueryRowContext(ctx, "PRAGMA query_only").Scan(&writable); err != nil || writable != 0 {
		writeError(w, http.StatusServiceUnavailable, "database is not writable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "database": "ok"})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InviteCode string `json:"invite_code"`
		Username   string `json:"username"`
		Email      string `json:"email"`
		Password   string `json:"password"`
		Nickname   string `json:"nickname"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	user, err := s.identity.Register(r.Context(), identity.RegisterInput(body), remoteIP(r))
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	if err := s.startSession(w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start session")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	user, err := s.identity.Authenticate(r.Context(), body.Identifier, body.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "用户名、邮箱或密码不正确")
		return
	}
	if err := s.startSession(w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user identity.User) error {
	token, _, expires, err := s.identity.CreateSession(r.Context(), user.ID, r.UserAgent(), remoteIP(r), s.cfg.SessionTTL)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds()),
	})
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	_ = s.identity.RevokeSession(r.Context(), p.User.ID, p.SessionID)
	clearSessionCookie(w, s.cfg.CookieSecure)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": mustPrincipal(r).User})
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	items, err := s.identity.ListSessions(r.Context(), p.User.ID, p.SessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list sessions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": items})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	if err := s.identity.RevokeSession(r.Context(), p.User.ID, r.PathValue("id")); err != nil {
		writeIdentityError(w, err)
		return
	}
	if r.PathValue("id") == p.SessionID {
		clearSessionCookie(w, s.cfg.CookieSecure)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	var body identity.ProfileInput
	if !decodeJSON(w, r, &body) {
		return
	}
	user, err := s.identity.UpdateProfile(r.Context(), mustPrincipal(r).User.ID, body, remoteIP(r))
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	if p.User.Role != "admin" {
		writeError(w, http.StatusForbidden, "administrator required")
		return
	}
	var body struct {
		MaxUses int `json:"max_uses"`
		Hours   int `json:"expires_in_hours"`
		Count   int `json:"count"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.MaxUses == 0 {
		body.MaxUses = 1
	}
	if body.Hours == 0 {
		body.Hours = 168
	}
	if body.Count == 0 {
		body.Count = 1
	}
	if body.Count < 1 || body.Count > 20 {
		writeError(w, http.StatusBadRequest, "一次可以生成 1～20 个邀请码")
		return
	}
	type inviteResponse struct {
		Code      string    `json:"code"`
		ExpiresAt time.Time `json:"expires_at"`
		MaxUses   int       `json:"max_uses"`
	}
	invites := make([]inviteResponse, 0, body.Count)
	for range body.Count {
		code, expires, err := s.identity.CreateInvite(r.Context(), p.User, body.MaxUses, time.Duration(body.Hours)*time.Hour, remoteIP(r))
		if err != nil {
			writeIdentityError(w, err)
			return
		}
		invites = append(invites, inviteResponse{Code: code, ExpiresAt: expires, MaxUses: body.MaxUses})
	}
	writeJSON(w, http.StatusCreated, map[string]any{"invites": invites, "count": len(invites)})
}

func (s *Server) adminHealth(w http.ResponseWriter, r *http.Request) {
	if mustPrincipal(r).User.Role != "admin" {
		writeError(w, http.StatusForbidden, "administrator required")
		return
	}
	var users, sessions int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM users WHERE status = 'active'").Scan(&users)
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM sessions WHERE revoked_at IS NULL AND expires_at > ?", time.Now().UTC().Format(time.RFC3339Nano)).Scan(&sessions)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "active_users": users, "active_sessions": sessions})
}

func (s *Server) listPosts(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := s.content.List(r.Context(), p.User, content.ListOptions{
		Scope: r.URL.Query().Get("scope"), Status: r.URL.Query().Get("status"), Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) createPost(w http.ResponseWriter, r *http.Request) {
	var body content.WriteInput
	if !decodeJSON(w, r, &body) {
		return
	}
	post, err := s.content.Create(r.Context(), mustPrincipal(r).User, body, remoteIP(r))
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"post": post})
}

func (s *Server) getPost(w http.ResponseWriter, r *http.Request) {
	post, err := s.content.Get(r.Context(), mustPrincipal(r).User, r.PathValue("id"))
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"post": post})
}

func (s *Server) updatePost(w http.ResponseWriter, r *http.Request) {
	var body content.WriteInput
	if !decodeJSON(w, r, &body) {
		return
	}
	post, err := s.content.Update(r.Context(), mustPrincipal(r).User, r.PathValue("id"), body, remoteIP(r))
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"post": post})
}

func (s *Server) submitPost(w http.ResponseWriter, r *http.Request) {
	post, err := s.content.Submit(r.Context(), mustPrincipal(r).User, r.PathValue("id"), remoteIP(r))
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"post": post})
}

func (s *Server) moderatePost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	post, err := s.content.Moderate(r.Context(), mustPrincipal(r).User, r.PathValue("id"), body.Action, body.Note, remoteIP(r))
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"post": post})
}

func (s *Server) deletePost(w http.ResponseWriter, r *http.Request) {
	if err := s.content.Delete(r.Context(), mustPrincipal(r).User, r.PathValue("id"), remoteIP(r)); err != nil {
		writeContentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	comments, err := s.content.ListComments(r.Context(), mustPrincipal(r).User, r.PathValue("id"))
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": comments})
}

func (s *Server) addComment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	comment, err := s.content.AddComment(r.Context(), mustPrincipal(r).User, r.PathValue("id"), body.Body, remoteIP(r))
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"comment": comment})
}

func (s *Server) deleteComment(w http.ResponseWriter, r *http.Request) {
	if err := s.content.DeleteComment(r.Context(), mustPrincipal(r).User, r.PathValue("id"), remoteIP(r)); err != nil {
		writeContentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) toggleLike(w http.ResponseWriter, r *http.Request) {
	liked, count, err := s.content.ToggleLike(r.Context(), mustPrincipal(r).User, r.PathValue("id"), remoteIP(r))
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"liked": liked, "like_count": count})
}

func (s *Server) uploadMedia(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength <= 0 {
		writeError(w, http.StatusLengthRequired, "上传文件必须提供大小")
		return
	}
	if r.ContentLength > mediastore.MaxFileSize {
		writeError(w, http.StatusRequestEntityTooLarge, "单个文件不能超过 8 GiB")
		return
	}
	filename, err := url.QueryUnescape(r.Header.Get("X-File-Name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "文件名格式不正确")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mediastore.MaxFileSize)
	record, err := s.media.Upload(r.Context(), mustPrincipal(r).User, mediastore.UploadInput{
		ClientRequestID: r.Header.Get("X-Upload-ID"),
		Filename:        filename,
		MimeType:        r.Header.Get("Content-Type"),
		Size:            r.ContentLength,
		Body:            r.Body,
		IPAddress:       remoteIP(r),
		Width:           parsePositiveHeader(r.Header.Get("X-Media-Width")),
		Height:          parsePositiveHeader(r.Header.Get("X-Media-Height")),
		DurationMS:      int64(parsePositiveHeader(r.Header.Get("X-Media-Duration-MS"))),
	})
	if err != nil {
		s.logger.Warn("media_upload_failed", "user_id", mustPrincipal(r).User.ID, "error", err)
		writeMediaError(w, err)
		return
	}
	usage, _ := s.media.Usage(r.Context(), mustPrincipal(r).User.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"media": record, "usage": usage})
}

func parsePositiveHeader(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func (s *Server) mediaUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := s.media.Usage(r.Context(), mustPrincipal(r).User.ID)
	if err != nil {
		writeMediaError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": usage})
}

func (s *Server) deleteMedia(w http.ResponseWriter, r *http.Request) {
	if err := s.media.Delete(r.Context(), mustPrincipal(r).User, r.PathValue("id"), remoteIP(r)); err != nil {
		writeMediaError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mediaContent(w http.ResponseWriter, r *http.Request) {
	content, err := s.media.OpenContent(r.Context(), mustPrincipal(r).User, r.PathValue("id"), r.Header.Get("Range"), r.URL.Query().Get("variant") == "preview")
	if err != nil {
		writeMediaError(w, err)
		return
	}
	defer content.Body.Close()
	w.Header().Set("Content-Type", content.MimeType)
	w.Header().Set("Content-Disposition", "inline")
	if content.AcceptRanges != "" {
		w.Header().Set("Accept-Ranges", content.AcceptRanges)
	}
	if content.ContentRange != "" {
		w.Header().Set("Content-Range", content.ContentRange)
	}
	if content.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(content.ContentLength, 10))
	}
	w.WriteHeader(content.StatusCode)
	if _, err := io.Copy(w, content.Body); err != nil {
		s.logger.Warn("media_stream_interrupted", "media_id", r.PathValue("id"), "error", err)
	}
}

func (s *Server) updateAvatar(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MediaID string `json:"media_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	p := mustPrincipal(r)
	user, previous, err := s.identity.SetAvatar(r.Context(), p.User.ID, body.MediaID, remoteIP(r))
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	if previous != "" && previous != body.MediaID {
		_ = s.media.Delete(r.Context(), p.User, previous, remoteIP(r))
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) clearAvatar(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	user, previous, err := s.identity.ClearAvatar(r.Context(), p.User.ID, remoteIP(r))
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	if previous != "" {
		_ = s.media.Delete(r.Context(), p.User, previous, remoteIP(r))
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) frontend() http.Handler {
	index := filepath.Join(s.cfg.FrontendDir, "index.html")
	files := http.FileServer(http.Dir(s.cfg.FrontendDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/health/") {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(s.cfg.FrontendDir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			w.Header().Del("Cache-Control")
			files.ServeHTTP(w, r)
			return
		}
		if _, err := os.Stat(index); err != nil {
			writeError(w, http.StatusNotFound, "frontend is not built")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}

func mustPrincipal(r *http.Request) principal { return r.Context().Value(principalKey).(principal) }

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			writeError(w, http.StatusBadRequest, "请求包含不支持的字段")
		} else {
			writeError(w, http.StatusBadRequest, "请求内容格式不正确")
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON object")
		return false
	}
	return true
}

func writeIdentityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, identity.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "authentication failed")
	case errors.Is(err, identity.ErrInviteInvalid):
		writeError(w, http.StatusBadRequest, "邀请码无效、已过期或已用完")
	case errors.Is(err, identity.ErrConflict):
		writeError(w, http.StatusConflict, "用户名或邮箱已被使用")
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, identity.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func writeContentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, content.ErrNotFound):
		writeError(w, http.StatusNotFound, "内容不存在")
	case errors.Is(err, content.ErrForbidden):
		writeError(w, http.StatusForbidden, "无权访问该内容")
	case errors.Is(err, content.ErrConflict):
		writeError(w, http.StatusConflict, "内容状态已变化，请刷新后重试")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func writeMediaError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mediastore.ErrNotFound):
		writeError(w, http.StatusNotFound, "媒体不存在")
	case errors.Is(err, mediastore.ErrForbidden):
		writeError(w, http.StatusForbidden, "无权访问该媒体")
	case errors.Is(err, mediastore.ErrConflict):
		writeError(w, http.StatusConflict, "上传任务或媒体状态已变化")
	case errors.Is(err, mediastore.ErrQuotaExceeded):
		writeError(w, http.StatusRequestEntityTooLarge, "媒体空间额度不足")
	case errors.Is(err, mediastore.ErrStorageUnavailable):
		writeError(w, http.StatusServiceUnavailable, "远端存储暂时不可用，请稍后重试")
	case errors.Is(err, mediastore.ErrInvalid):
		writeError(w, http.StatusBadRequest, "仅支持有效的图片或视频文件")
	default:
		writeError(w, http.StatusInternalServerError, "媒体操作失败")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": message, "status": strconv.Itoa(status)}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
