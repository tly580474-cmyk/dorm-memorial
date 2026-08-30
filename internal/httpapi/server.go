package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"dorm-memorial/internal/config"
	"dorm-memorial/internal/content"
	appdatabase "dorm-memorial/internal/database"
	"dorm-memorial/internal/identity"
	mediastore "dorm-memorial/internal/media"
	"dorm-memorial/internal/messaging"
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
	cfg       config.Config
	db        *sql.DB
	identity  *identity.Store
	content   *content.Store
	media     *mediastore.Store
	messaging *messaging.Store
	logger    *slog.Logger
	handler   http.Handler
	backupMu  sync.Mutex
}

func New(cfg config.Config, db *sql.DB, identities *identity.Store, logger *slog.Logger, objects ...storage.ObjectStorage) *Server {
	var objectStore storage.ObjectStorage
	if len(objects) > 0 {
		objectStore = objects[0]
	}
	mediaStore := mediastore.NewStore(db, objectStore, cfg.FFmpegPath)
	mediaStore.ConfigureVideoProcessing(cfg.MediaStagingDir, cfg.VideoEncoder)
	s := &Server{cfg: cfg, db: db, identity: identities, content: content.NewStore(db), media: mediaStore, messaging: messaging.NewStore(db), logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.Handle("POST /api/auth/logout", s.requireAuth(http.HandlerFunc(s.logout)))
	mux.Handle("GET /api/auth/me", s.requireAuth(http.HandlerFunc(s.me)))
	mux.Handle("POST /api/auth/deactivate", s.requireAuth(http.HandlerFunc(s.deactivateAccount)))
	mux.Handle("GET /api/auth/sessions", s.requireAuth(http.HandlerFunc(s.sessions)))
	mux.Handle("GET /api/members", s.requireAuth(http.HandlerFunc(s.listMembers)))
	mux.Handle("GET /api/messages/conversations", s.requireAuth(http.HandlerFunc(s.listConversations)))
	mux.Handle("POST /api/messages/conversations/direct", s.requireAuth(http.HandlerFunc(s.startDirectConversation)))
	mux.Handle("GET /api/messages/conversations/{id}", s.requireAuth(http.HandlerFunc(s.listMessages)))
	mux.Handle("POST /api/messages/conversations/{id}", s.requireAuth(http.HandlerFunc(s.sendMessage)))
	mux.Handle("POST /api/messages/conversations/{id}/read", s.requireAuth(http.HandlerFunc(s.markConversationRead)))
	mux.Handle("GET /api/messages/items/{id}", s.requireAuth(http.HandlerFunc(s.getMessage)))
	mux.Handle("POST /api/messages/items/{id}/recall", s.requireAuth(http.HandlerFunc(s.recallMessage)))
	mux.Handle("GET /api/notifications", s.requireAuth(http.HandlerFunc(s.listNotifications)))
	mux.Handle("DELETE /api/notifications", s.requireAuth(http.HandlerFunc(s.clearNotifications)))
	mux.Handle("POST /api/notifications/read-all", s.requireAuth(http.HandlerFunc(s.markAllNotificationsRead)))
	mux.Handle("POST /api/notifications/{id}/read", s.requireAuth(http.HandlerFunc(s.markNotificationRead)))
	mux.Handle("DELETE /api/auth/sessions/{id}", s.requireAuth(http.HandlerFunc(s.revokeSession)))
	mux.Handle("PATCH /api/profile", s.requireAuth(http.HandlerFunc(s.updateProfile)))
	mux.Handle("PATCH /api/account", s.requireAuth(http.HandlerFunc(s.updateAccount)))
	mux.Handle("POST /api/profile/avatar", s.requireAuth(http.HandlerFunc(s.updateAvatar)))
	mux.Handle("DELETE /api/profile/avatar", s.requireAuth(http.HandlerFunc(s.clearAvatar)))
	mux.Handle("POST /api/admin/invites", s.requireAuth(http.HandlerFunc(s.createInvite)))
	mux.Handle("GET /api/admin/health", s.requireAuth(http.HandlerFunc(s.adminHealth)))
	mux.Handle("GET /api/admin/users", s.requireAuth(http.HandlerFunc(s.listAdminUsers)))
	mux.Handle("PATCH /api/admin/users/{id}", s.requireAuth(http.HandlerFunc(s.updateAdminUser)))
	mux.Handle("GET /api/admin/messages", s.requireAuth(http.HandlerFunc(s.listAdminMessages)))
	mux.Handle("DELETE /api/admin/messages/{id}", s.requireAuth(http.HandlerFunc(s.deleteAdminMessage)))
	mux.Handle("GET /api/admin/media", s.requireAuth(http.HandlerFunc(s.listAdminMedia)))
	mux.Handle("DELETE /api/admin/media/{id}", s.requireAuth(http.HandlerFunc(s.purgeAdminMedia)))
	mux.Handle("POST /api/admin/backup", s.requireAuth(http.HandlerFunc(s.exportAdminBackup)))
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
	mux.Handle("GET /api/guestbook", s.requireAuth(http.HandlerFunc(s.listGuestbook)))
	mux.Handle("POST /api/guestbook", s.requireAuth(http.HandlerFunc(s.createGuestbookEntry)))
	mux.Handle("POST /api/guestbook/{id}/hide", s.requireAuth(http.HandlerFunc(s.hideGuestbookEntry)))
	mux.Handle("POST /api/guestbook/{id}/restore", s.requireAuth(http.HandlerFunc(s.restoreGuestbookEntry)))
	mux.Handle("DELETE /api/guestbook/{id}", s.requireAuth(http.HandlerFunc(s.deleteGuestbookEntry)))
	mux.Handle("POST /api/media/uploads", s.requireAuth(http.HandlerFunc(s.uploadMedia)))
	mux.Handle("GET /api/media-upload-jobs/{id}", s.requireAuth(http.HandlerFunc(s.mediaUploadStatus)))
	mux.Handle("GET /api/media/usage", s.requireAuth(http.HandlerFunc(s.mediaUsage)))
	mux.Handle("GET /api/media/limits", s.requireAuth(http.HandlerFunc(s.mediaLimits)))
	mux.Handle("DELETE /api/media/{id}", s.requireAuth(http.HandlerFunc(s.deleteMedia)))
	mux.Handle("GET /api/media/{id}/content", s.requireAuth(http.HandlerFunc(s.mediaContent)))
	mux.Handle("/", s.frontend())
	s.handler = s.middleware(mux)
	return s
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) StartMediaMaintenance(ctx context.Context) error {
	return s.media.StartMaintenance(ctx)
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	members, err := s.identity.ListMembers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法读取成员列表")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	items, err := s.messaging.ListConversations(r.Context(), mustPrincipal(r).User)
	if err != nil {
		writeMessagingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": items})
}

func (s *Server) startDirectConversation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RecipientID string `json:"recipient_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	item, err := s.messaging.StartDirect(r.Context(), mustPrincipal(r).User, body.RecipientID)
	if err != nil {
		writeMessagingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": item})
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := s.messaging.ListMessages(r.Context(), mustPrincipal(r).User, r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeMessagingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body     string   `json:"body"`
		MediaIDs []string `json:"media_ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	item, err := s.messaging.SendMessage(r.Context(), mustPrincipal(r).User, r.PathValue("id"), body.Body, body.MediaIDs, remoteIP(r))
	if err != nil {
		writeMessagingError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": item})
}

func (s *Server) markConversationRead(w http.ResponseWriter, r *http.Request) {
	if err := s.messaging.MarkConversationRead(r.Context(), mustPrincipal(r).User, r.PathValue("id")); err != nil {
		writeMessagingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) recallMessage(w http.ResponseWriter, r *http.Request) {
	if err := s.messaging.RecallMessage(r.Context(), mustPrincipal(r).User, r.PathValue("id"), remoteIP(r)); err != nil {
		writeMessagingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	item, err := s.messaging.GetMessage(r.Context(), mustPrincipal(r).User, r.PathValue("id"))
	if err != nil {
		writeMessagingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": item})
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := s.messaging.ListNotifications(r.Context(), mustPrincipal(r).User, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeMessagingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	if err := s.messaging.MarkNotificationRead(r.Context(), mustPrincipal(r).User, r.PathValue("id")); err != nil {
		writeMessagingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) markAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if err := s.messaging.MarkAllNotificationsRead(r.Context(), mustPrincipal(r).User); err != nil {
		writeMessagingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearNotifications(w http.ResponseWriter, r *http.Request) {
	if err := s.messaging.ClearNotifications(r.Context(), mustPrincipal(r).User); err != nil {
		writeMessagingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(self), geolocation=()")
		if strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/api/media/") {
			w.Header().Set("Cache-Control", "no-store")
		}
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
			if !errors.Is(err, identity.ErrInvalidCredentials) {
				s.logger.Error("session_lookup_failed", "error", err)
				writeError(w, http.StatusServiceUnavailable, "会话服务暂时不可用，请稍后重试")
				return
			}
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
		if errors.Is(err, identity.ErrAccountDeactivated) {
			writeError(w, http.StatusForbidden, "该账号已注销")
			return
		}
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

func (s *Server) deactivateAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	p := mustPrincipal(r)
	if err := s.identity.SelfDeactivate(r.Context(), p.User, body.Password, remoteIP(r)); err != nil {
		switch {
		case errors.Is(err, identity.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "当前密码不正确")
		case errors.Is(err, identity.ErrNotFound):
			writeError(w, http.StatusConflict, "账号状态已变化，请刷新页面")
		default:
			writeError(w, http.StatusInternalServerError, "注销失败，请稍后重试")
		}
		return
	}
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

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	var body identity.AccountInput
	if !decodeJSON(w, r, &body) {
		return
	}
	principal := mustPrincipal(r)
	user, err := s.identity.UpdateAccount(r.Context(), principal.User.ID, principal.SessionID, body, remoteIP(r))
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

func (s *Server) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	items, err := s.identity.ListAdminUsers(r.Context(), p.User, r.URL.Query().Get("search"), r.URL.Query().Get("role"), r.URL.Query().Get("status"))
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": items, "count": len(items)})
}

func (s *Server) updateAdminUser(w http.ResponseWriter, r *http.Request) {
	var body identity.AdminUserUpdate
	if !decodeJSON(w, r, &body) {
		return
	}
	item, err := s.identity.UpdateAdminUser(r.Context(), mustPrincipal(r).User, r.PathValue("id"), body, remoteIP(r))
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": item})
}

func (s *Server) listAdminMessages(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.messaging.ListAdminGroupMessages(r.Context(), mustPrincipal(r).User, r.URL.Query().Get("search"), r.URL.Query().Get("status"), limit)
	if err != nil {
		writeMessagingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": items, "count": len(items)})
}

func (s *Server) deleteAdminMessage(w http.ResponseWriter, r *http.Request) {
	if err := s.messaging.RemoveAdminGroupMessage(r.Context(), mustPrincipal(r).User, r.PathValue("id"), remoteIP(r)); err != nil {
		writeMessagingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAdminMedia(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.media.ListAdmin(r.Context(), mustPrincipal(r).User, r.URL.Query().Get("search"), r.URL.Query().Get("type"), r.URL.Query().Get("status"), limit)
	if err != nil {
		writeMediaError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"media": items, "count": len(items)})
}

func (s *Server) purgeAdminMedia(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Force bool `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "删除请求无效")
		return
	}
	if err := s.media.Purge(r.Context(), mustPrincipal(r).User, r.PathValue("id"), input.Force, remoteIP(r)); err != nil {
		writeMediaError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) exportAdminBackup(w http.ResponseWriter, r *http.Request) {
	p := mustPrincipal(r)
	if p.User.Role != "admin" {
		writeError(w, http.StatusForbidden, "administrator required")
		return
	}
	if !s.backupMu.TryLock() {
		writeError(w, http.StatusConflict, "已有备份正在生成，请稍后重试")
		return
	}
	defer s.backupMu.Unlock()

	tempDir, err := os.MkdirTemp("", "dorm-memorial-export-")
	if err != nil {
		s.logger.Error("admin_backup_temp_failed", "error", err)
		writeError(w, http.StatusInternalServerError, "无法创建备份临时目录")
		return
	}
	defer os.RemoveAll(tempDir)

	createdAt := time.Now().UTC()
	filename := "dorm-memorial-backup-" + createdAt.Format("20060102-150405") + ".db"
	destination := filepath.Join(tempDir, filename)
	if err := appdatabase.Backup(r.Context(), s.db, destination); err != nil {
		s.logger.Error("admin_backup_failed", "user_id", p.User.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "备份生成或完整性校验失败")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO audit_logs(actor_id, action, target_type, metadata_json, ip_address, created_at)
		VALUES(?, 'admin.backup.export', 'database', '{"format":"sqlite"}', ?, ?)`, p.User.ID, remoteIP(r), createdAt.Format(time.RFC3339Nano)); err != nil {
		s.logger.Error("admin_backup_audit_failed", "user_id", p.User.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "无法记录备份审计")
		return
	}
	file, err := os.Open(destination)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法读取已生成的备份")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法读取备份信息")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, file); err != nil {
		s.logger.Warn("admin_backup_download_interrupted", "user_id", p.User.ID, "error", err)
	}
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

func (s *Server) listGuestbook(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := s.content.ListGuestbook(r.Context(), mustPrincipal(r).User, strings.TrimSpace(r.URL.Query().Get("recipient_id")), strings.TrimSpace(r.URL.Query().Get("status")), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) createGuestbookEntry(w http.ResponseWriter, r *http.Request) {
	var body content.GuestbookInput
	if !decodeJSON(w, r, &body) {
		return
	}
	entry, err := s.content.CreateGuestbookEntry(r.Context(), mustPrincipal(r).User, body, remoteIP(r))
	if err != nil {
		writeContentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"entry": entry})
}

func (s *Server) hideGuestbookEntry(w http.ResponseWriter, r *http.Request) {
	if err := s.content.HideGuestbookEntry(r.Context(), mustPrincipal(r).User, r.PathValue("id"), remoteIP(r)); err != nil {
		writeContentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) restoreGuestbookEntry(w http.ResponseWriter, r *http.Request) {
	if err := s.content.RestoreGuestbookEntry(r.Context(), mustPrincipal(r).User, r.PathValue("id"), remoteIP(r)); err != nil {
		writeContentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteGuestbookEntry(w http.ResponseWriter, r *http.Request) {
	if err := s.content.DeleteGuestbookEntry(r.Context(), mustPrincipal(r).User, r.PathValue("id"), remoteIP(r)); err != nil {
		writeContentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mediaLimits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"max_image_upload_bytes":     s.cfg.MaxImageUploadBytes,
		"max_image_pixels":           mediastore.MaxImagePixels,
		"supported_image_mime_types": mediastore.SupportedImageMIMETypes(),
	})
}

func (s *Server) uploadMedia(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength <= 0 {
		writeError(w, http.StatusLengthRequired, "上传文件必须提供大小")
		return
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	maxFileSize := mediastore.MaxFileSize
	if strings.HasPrefix(mimeType, "video/") {
		maxFileSize = s.cfg.MaxVideoUploadBytes
	} else if strings.HasPrefix(mimeType, "image/") {
		maxFileSize = s.cfg.MaxImageUploadBytes
	}
	if r.ContentLength > maxFileSize {
		switch {
		case strings.HasPrefix(mimeType, "video/"):
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("单个视频不能超过 %d MiB", maxFileSize/(1024*1024)))
		case strings.HasPrefix(mimeType, "image/"):
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("单个图片不能超过 %d MiB", maxFileSize/(1024*1024)))
		default:
			writeError(w, http.StatusRequestEntityTooLarge, "单个文件不能超过 8 GiB")
		}
		return
	}
	filename, err := url.QueryUnescape(r.Header.Get("X-File-Name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "文件名格式不正确")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFileSize)
	input := mediastore.UploadInput{
		ClientRequestID: r.Header.Get("X-Upload-ID"),
		Filename:        filename,
		MimeType:        r.Header.Get("Content-Type"),
		Size:            r.ContentLength,
		Body:            r.Body,
		IPAddress:       remoteIP(r),
		Width:           parsePositiveHeader(r.Header.Get("X-Media-Width")),
		Height:          parsePositiveHeader(r.Header.Get("X-Media-Height")),
		DurationMS:      int64(parsePositiveHeader(r.Header.Get("X-Media-Duration-MS"))),
	}
	if strings.HasPrefix(mimeType, "video/") {
		job, err := s.media.StageVideo(r.Context(), mustPrincipal(r).User, input)
		if err != nil {
			s.logger.Warn("media_video_stage_failed", "user_id", mustPrincipal(r).User.ID, "error", err)
			writeMediaError(w, err)
			return
		}
		usage, _ := s.media.Usage(r.Context(), mustPrincipal(r).User.ID)
		writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "usage": usage})
		return
	}
	record, err := s.media.Upload(r.Context(), mustPrincipal(r).User, input)
	if err != nil {
		s.logger.Warn("media_upload_failed", "user_id", mustPrincipal(r).User.ID, "error", err)
		writeMediaError(w, err)
		return
	}
	usage, _ := s.media.Usage(r.Context(), mustPrincipal(r).User.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"media": record, "usage": usage})
}

func (s *Server) mediaUploadStatus(w http.ResponseWriter, r *http.Request) {
	job, err := s.media.ProcessingStatus(r.Context(), mustPrincipal(r).User, r.PathValue("id"))
	if err != nil {
		writeMediaError(w, err)
		return
	}
	response := map[string]any{"job": job}
	if r.URL.Query().Get("include_usage") != "0" {
		usage, err := s.media.Usage(r.Context(), mustPrincipal(r).User.ID)
		if err != nil {
			writeMediaError(w, err)
			return
		}
		response["usage"] = usage
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
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
	trace := storage.NewTrace()
	ctx := storage.WithTrace(r.Context(), trace)
	descriptor, err := s.media.InspectContent(ctx, mustPrincipal(r).User, r.PathValue("id"))
	if err != nil {
		writeMediaError(w, err)
		return
	}
	variant := strings.TrimSpace(r.URL.Query().Get("variant"))
	if variant == "" {
		variant = "original"
	}
	if variant != "original" && variant != "preview" && variant != "display" && variant != "playback" && variant != "watch" {
		writeError(w, http.StatusBadRequest, "不支持的媒体版本")
		return
	}
	requestedVariant := variant
	if variant == "watch" {
		if descriptor.MediaType != "video" {
			writeError(w, http.StatusBadRequest, "播放资源只适用于视频")
			return
		}
		variant = "original"
		if descriptor.PlaybackPath != "" {
			variant = "playback"
		}
	}
	etag := `"media-` + r.PathValue("id") + `-` + variant + `"`
	w.Header().Set("X-Media-Variant", variant)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=604800, immutable")
	if requestedVariant == "watch" {
		// A legacy upload may gain a prepared rendition after this URL was read.
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	if r.Header.Get("Range") == "" && r.Header.Get("If-None-Match") == etag {
		w.Header().Set("Server-Timing", trace.ServerTiming())
		w.WriteHeader(http.StatusNotModified)
		return
	}
	content, err := s.media.OpenDescriptor(ctx, descriptor, r.Header.Get("Range"), variant)
	if err != nil {
		w.Header().Del("ETag")
		w.Header().Set("Cache-Control", "no-store")
		writeMediaError(w, err)
		return
	}
	defer content.Body.Close()
	w.Header().Set("Server-Timing", trace.ServerTiming())
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
	if r.Method == http.MethodHead {
		return
	}
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
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
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

func writeMessagingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, messaging.ErrNotFound):
		writeError(w, http.StatusNotFound, "消息或会话不存在")
	case errors.Is(err, messaging.ErrForbidden):
		writeError(w, http.StatusForbidden, "无权访问该会话")
	case errors.Is(err, messaging.ErrConflict):
		writeError(w, http.StatusConflict, "消息状态已变化或已超过撤回时间")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func writeMediaError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mediastore.ErrImagePixelLimit):
		writeError(w, http.StatusBadRequest, fmt.Sprintf("图片像素或 GIF 累计帧像素超过 %d 万，请缩小尺寸或减少帧数后重试", mediastore.MaxImagePixels/10000))
	case errors.Is(err, mediastore.ErrPreviewPending):
		w.Header().Set("Retry-After", "2")
		w.Header().Set("X-Media-State", "preparing")
		writeError(w, http.StatusServiceUnavailable, "媒体版本正在生成，请稍后重试")
	case errors.Is(err, mediastore.ErrNotFound):
		writeError(w, http.StatusNotFound, "媒体不存在")
	case errors.Is(err, mediastore.ErrForbidden):
		writeError(w, http.StatusForbidden, "无权访问该媒体")
	case errors.Is(err, mediastore.ErrConfirmationNeeded):
		writeError(w, http.StatusConflict, "该媒体仍在内容中展示，必须明确确认后才能永久删除")
	case errors.Is(err, mediastore.ErrConflict):
		writeError(w, http.StatusConflict, "上传任务或媒体状态已变化")
	case errors.Is(err, mediastore.ErrQuotaExceeded):
		writeError(w, http.StatusRequestEntityTooLarge, "媒体空间额度不足")
	case errors.Is(err, mediastore.ErrStorageUnavailable):
		writeError(w, http.StatusServiceUnavailable, "远端存储暂时不可用，请稍后重试")
	case errors.Is(err, mediastore.ErrInvalid):
		writeError(w, http.StatusBadRequest, "仅支持有效的图片、视频或音频文件")
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
