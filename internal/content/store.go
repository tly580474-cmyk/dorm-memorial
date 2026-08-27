package content

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"

	"dorm-memorial/internal/identity"
	"dorm-memorial/internal/messaging"
)

var (
	ErrNotFound  = errors.New("content not found")
	ErrForbidden = errors.New("content access forbidden")
	ErrConflict  = errors.New("content state conflict")
	iframeSource = regexp.MustCompile(`(?is)<iframe\b[^>]*\bsrc\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s>]+))`)
)

type Author struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	Nickname   string `json:"nickname"`
	AvatarPath string `json:"avatar_path"`
}

type Post struct {
	ID               string     `json:"id"`
	Author           Author     `json:"author"`
	Body             string     `json:"body"`
	Status           string     `json:"status"`
	Visibility       string     `json:"visibility"`
	ContentDate      *time.Time `json:"content_date"`
	ModerationNote   string     `json:"moderation_note"`
	SubmittedAt      *time.Time `json:"submitted_at"`
	PublishedAt      *time.Time `json:"published_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Tags             []string   `json:"tags"`
	CommentCount     int        `json:"comment_count"`
	LikeCount        int        `json:"like_count"`
	LikedByMe        bool       `json:"liked_by_me"`
	Media            []Media    `json:"media"`
	ExternalVideoURL string     `json:"external_video_url"`
}

type Media struct {
	ID               string `json:"id"`
	OriginalFilename string `json:"original_filename"`
	MediaType        string `json:"media_type"`
	MimeType         string `json:"mime_type"`
	SizeBytes        int64  `json:"size_bytes"`
	Status           string `json:"status"`
	Width            *int   `json:"width"`
	Height           *int   `json:"height"`
	DurationMS       *int64 `json:"duration_ms"`
	HasPreview       bool   `json:"has_preview"`
}

type Comment struct {
	ID        string    `json:"id"`
	PostID    string    `json:"post_id"`
	Author    Author    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type GuestbookEntry struct {
	ID               string    `json:"id"`
	Author           Author    `json:"author"`
	Recipient        *Author   `json:"recipient"`
	Body             string    `json:"body"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	Media            []Media   `json:"media"`
	ExternalVideoURL string    `json:"external_video_url"`
}

type GuestbookInput struct {
	RecipientID      string   `json:"recipient_id"`
	Body             string   `json:"body"`
	MediaIDs         []string `json:"media_ids"`
	ExternalVideoURL string   `json:"external_video_url"`
}

type GuestbookPage struct {
	Entries    []GuestbookEntry `json:"entries"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type WriteInput struct {
	Body             string   `json:"body"`
	ContentDate      string   `json:"content_date"`
	Visibility       string   `json:"visibility"`
	Tags             []string `json:"tags"`
	MediaIDs         []string `json:"media_ids"`
	Submit           bool     `json:"submit"`
	ExternalVideoURL string   `json:"external_video_url"`
}

type ListOptions struct {
	Scope  string
	Status string
	Cursor string
	Limit  int
}

type Page struct {
	Posts      []Post `json:"posts"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type cursor struct {
	Sort string `json:"sort"`
	ID   string `json:"id"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Create(ctx context.Context, actor identity.User, input WriteInput, ip string) (Post, error) {
	input, contentDate, err := validateWrite(input)
	if err != nil {
		return Post{}, err
	}
	status := "draft"
	if input.Submit {
		status = "published"
	}
	now := time.Now().UTC()
	id := newID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback()
	var submitted any
	if status == "published" {
		submitted = now.Format(time.RFC3339Nano)
	}
	var published any
	if status == "published" {
		published = submitted
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO posts(id, author_id, body, status, visibility, content_date, submitted_at, published_at, external_video_url, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, actor.ID, input.Body, status, input.Visibility, nullableDate(contentDate), submitted, published, input.ExternalVideoURL, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return Post{}, err
	}
	if err := replaceTags(ctx, tx, id, input.Tags); err != nil {
		return Post{}, err
	}
	if err := replaceMedia(ctx, tx, actor.ID, id, input.MediaIDs); err != nil {
		return Post{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, metadata_json, ip_address, created_at)
		VALUES(?, ?, 'post', ?, ?, ?, ?)`, actor.ID, "post.create", id, fmt.Sprintf(`{"status":%q}`, status), ip, now.Format(time.RFC3339Nano)); err != nil {
		return Post{}, err
	}
	if err := tx.Commit(); err != nil {
		return Post{}, err
	}
	return s.Get(ctx, actor, id)
}

func (s *Store) Update(ctx context.Context, actor identity.User, id string, input WriteInput, ip string) (Post, error) {
	input, contentDate, err := validateWrite(input)
	if err != nil {
		return Post{}, err
	}
	var authorID, status string
	if err := s.db.QueryRowContext(ctx, "SELECT author_id, status FROM posts WHERE id = ?", id).Scan(&authorID, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Post{}, ErrNotFound
		}
		return Post{}, err
	}
	if authorID != actor.ID && actor.Role != "admin" {
		return Post{}, ErrForbidden
	}
	if status != "draft" && status != "published" {
		return Post{}, ErrConflict
	}
	nextStatus := status
	var submitted any
	if input.Submit {
		nextStatus = "published"
		submitted = time.Now().UTC().Format(time.RFC3339Nano)
	} else if status == "published" {
		submitted = time.Now().UTC().Format(time.RFC3339Nano)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback()
	var published any
	if nextStatus == "published" {
		published = nowText()
	}
	result, err := tx.ExecContext(ctx, `UPDATE posts SET body = ?, visibility = ?, content_date = ?, status = ?, submitted_at = COALESCE(?, submitted_at), published_at = COALESCE(published_at, ?), external_video_url = ?, moderation_note = '', updated_at = ? WHERE id = ? AND status IN ('draft', 'published')`, input.Body, input.Visibility, nullableDate(contentDate), nextStatus, submitted, published, input.ExternalVideoURL, nowText(), id)
	if err != nil {
		return Post{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Post{}, ErrConflict
	}
	if err := replaceTags(ctx, tx, id, input.Tags); err != nil {
		return Post{}, err
	}
	if err := replaceMedia(ctx, tx, actor.ID, id, input.MediaIDs); err != nil {
		return Post{}, err
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'post.update', 'post', ?, ?, ?)`, actor.ID, id, ip, nowText())
	if err := tx.Commit(); err != nil {
		return Post{}, err
	}
	return s.Get(ctx, actor, id)
}

func (s *Store) Submit(ctx context.Context, actor identity.User, id, ip string) (Post, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE posts SET status = 'published', submitted_at = ?, published_at = ?, moderation_note = '', updated_at = ?
		WHERE id = ? AND author_id = ? AND status = 'draft' AND (length(trim(body)) > 0 OR external_video_url <> '' OR EXISTS(SELECT 1 FROM post_media WHERE post_id = posts.id))`, nowText(), nowText(), nowText(), id, actor.ID)
	if err != nil {
		return Post{}, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return Post{}, ErrConflict
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'post.submit', 'post', ?, ?, ?)`, actor.ID, id, ip, nowText())
	return s.Get(ctx, actor, id)
}

func (s *Store) Moderate(ctx context.Context, actor identity.User, id, action, note, ip string) (Post, error) {
	if actor.Role != "admin" {
		return Post{}, ErrForbidden
	}
	if len([]rune(note)) > 500 {
		return Post{}, errors.New("moderation note is too long")
	}
	var query string
	switch action {
	case "approve":
		query = `UPDATE posts SET status = 'published', moderation_note = ?, published_at = ?, updated_at = ? WHERE id = ? AND status = 'pending'`
	case "hide":
		query = `UPDATE posts SET status = 'hidden', moderation_note = ?, updated_at = ? WHERE id = ? AND status IN ('pending', 'published')`
	default:
		return Post{}, errors.New("invalid moderation action")
	}
	var result sql.Result
	var err error
	if action == "approve" {
		result, err = s.db.ExecContext(ctx, query, strings.TrimSpace(note), nowText(), nowText(), id)
	} else {
		result, err = s.db.ExecContext(ctx, query, strings.TrimSpace(note), nowText(), id)
	}
	if err != nil {
		return Post{}, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return Post{}, ErrConflict
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, metadata_json, ip_address, created_at) VALUES(?, ?, 'post', ?, ?, ?, ?)`, actor.ID, "post."+action, id, fmt.Sprintf(`{"note":%q}`, strings.TrimSpace(note)), ip, nowText())
	post, err := s.Get(ctx, actor, id)
	if err != nil {
		return Post{}, err
	}
	if post.Author.ID != actor.ID {
		title := "你的投稿已通过审核"
		kind := "post_approved"
		if action == "hide" {
			title = "你的投稿未通过审核"
			kind = "post_hidden"
		}
		_ = messaging.CreateNotification(ctx, s.db, messaging.NotificationInput{UserID: post.Author.ID, ActorID: actor.ID, Kind: kind, TargetType: "post", TargetID: post.ID, Title: title, Body: strings.TrimSpace(note), EventKey: "moderation:" + post.ID + ":" + action})
	}
	return post, nil
}

func (s *Store) Delete(ctx context.Context, actor identity.User, id, ip string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE posts SET status = 'deleted', deleted_at = ?, updated_at = ?
		WHERE id = ? AND status != 'deleted' AND (author_id = ? OR ? = 'admin')`, nowText(), nowText(), id, actor.ID, actor.Role)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ErrNotFound
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'post.delete', 'post', ?, ?, ?)`, actor.ID, id, ip, nowText())
	return nil
}

func (s *Store) ListComments(ctx context.Context, actor identity.User, postID string) ([]Comment, error) {
	post, err := s.Get(ctx, actor, postID)
	if err != nil {
		return nil, err
	}
	if post.Status != "published" {
		return nil, ErrConflict
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.post_id, c.body, c.created_at, u.id, u.username, p.nickname, p.avatar_path
		FROM comments c JOIN users u ON u.id = c.author_id JOIN profiles p ON p.user_id = u.id
		WHERE c.post_id = ? AND c.status = 'visible' ORDER BY c.created_at, c.id LIMIT 500`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	comments := []Comment{}
	for rows.Next() {
		var item Comment
		var created string
		if err := rows.Scan(&item.ID, &item.PostID, &item.Body, &created, &item.Author.ID, &item.Author.Username, &item.Author.Nickname, &item.Author.AvatarPath); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		comments = append(comments, item)
	}
	return comments, rows.Err()
}

func (s *Store) AddComment(ctx context.Context, actor identity.User, postID, body, ip string) (Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > 2000 {
		return Comment{}, errors.New("comment must be 1-2000 characters")
	}
	var postAuthorID string
	if err := s.db.QueryRowContext(ctx, `SELECT author_id FROM posts WHERE id = ? AND status = 'published' AND visibility = 'members'`, postID).Scan(&postAuthorID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Comment{}, ErrForbidden
		}
		return Comment{}, err
	}
	id, now := newID(), time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO comments(id, post_id, author_id, body, status, created_at, updated_at) VALUES(?, ?, ?, ?, 'visible', ?, ?)`, id, postID, actor.ID, body, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return Comment{}, err
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'comment.create', 'comment', ?, ?, ?)`, actor.ID, id, ip, now.Format(time.RFC3339Nano))
	if postAuthorID != actor.ID {
		_ = messaging.CreateNotification(ctx, s.db, messaging.NotificationInput{UserID: postAuthorID, ActorID: actor.ID, Kind: "post_comment", TargetType: "post", TargetID: postID, Title: actor.Nickname + "评论了你的回忆", Body: truncateRunes(body, 80), EventKey: "comment:" + id})
	}
	return Comment{ID: id, PostID: postID, Author: Author{ID: actor.ID, Username: actor.Username, Nickname: actor.Nickname, AvatarPath: actor.AvatarPath}, Body: body, CreatedAt: now}, nil
}

func (s *Store) DeleteComment(ctx context.Context, actor identity.User, id, ip string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE comments SET status = 'deleted', deleted_at = ?, updated_at = ? WHERE id = ? AND status = 'visible' AND (author_id = ? OR ? = 'admin')`, nowText(), nowText(), id, actor.ID, actor.Role)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'comment.delete', 'comment', ?, ?, ?)`, actor.ID, id, ip, nowText())
	return nil
}

func (s *Store) CreateGuestbookEntry(ctx context.Context, actor identity.User, input GuestbookInput, ip string) (GuestbookEntry, error) {
	input, err := validateGuestbookInput(input)
	if err != nil {
		return GuestbookEntry{}, err
	}
	if input.RecipientID != "" {
		var active int
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND status = 'active')`, input.RecipientID).Scan(&active); err != nil {
			return GuestbookEntry{}, err
		}
		if active == 0 {
			return GuestbookEntry{}, ErrNotFound
		}
	}
	id, now := newID(), time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestbookEntry{}, err
	}
	defer tx.Rollback()
	var recipient any
	if input.RecipientID != "" {
		recipient = input.RecipientID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO guestbook_entries(id, author_id, recipient_id, body, external_video_url, status, created_at, updated_at) VALUES(?, ?, ?, ?, ?, 'visible', ?, ?)`, id, actor.ID, recipient, input.Body, input.ExternalVideoURL, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return GuestbookEntry{}, err
	}
	if err := replaceGuestbookMedia(ctx, tx, actor.ID, id, input.MediaIDs); err != nil {
		return GuestbookEntry{}, err
	}
	if input.RecipientID != "" && input.RecipientID != actor.ID {
		if err := messaging.CreateNotification(ctx, tx, messaging.NotificationInput{UserID: input.RecipientID, ActorID: actor.ID, Kind: "guestbook_entry", TargetType: "guestbook", TargetID: input.RecipientID, Title: actor.Nickname + "在你的留言页留下了新内容", Body: truncateRunes(input.Body, 80), EventKey: "guestbook:" + id + ":" + input.RecipientID}); err != nil {
			return GuestbookEntry{}, err
		}
	} else if input.RecipientID == "" {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM users WHERE status = 'active' AND id <> ?`, actor.ID)
		if err != nil {
			return GuestbookEntry{}, err
		}
		recipients := []string{}
		for rows.Next() {
			var userID string
			if err := rows.Scan(&userID); err != nil {
				rows.Close()
				return GuestbookEntry{}, err
			}
			recipients = append(recipients, userID)
		}
		if err := rows.Close(); err != nil {
			return GuestbookEntry{}, err
		}
		for _, userID := range recipients {
			if err := messaging.CreateNotification(ctx, tx, messaging.NotificationInput{UserID: userID, ActorID: actor.ID, Kind: "guestbook_entry", TargetType: "guestbook", Title: actor.Nickname + "在宿舍留言册留下了新内容", Body: truncateRunes(input.Body, 80), EventKey: "guestbook:" + id + ":" + userID}); err != nil {
				return GuestbookEntry{}, err
			}
		}
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'guestbook.create', 'guestbook_entry', ?, ?, ?)`, actor.ID, id, ip, now.Format(time.RFC3339Nano))
	if err := tx.Commit(); err != nil {
		return GuestbookEntry{}, err
	}
	return s.getGuestbookEntry(ctx, id)
}

func (s *Store) ListGuestbook(ctx context.Context, actor identity.User, recipientID, status, cursorValue string, limit int) (GuestbookPage, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if status == "" {
		status = "visible"
	}
	if status != "visible" && status != "hidden" {
		return GuestbookPage{}, errors.New("invalid guestbook status")
	}
	if status == "hidden" && actor.Role != "admin" && recipientID != actor.ID {
		return GuestbookPage{}, ErrForbidden
	}
	args := []any{status}
	where := "WHERE g.status = ? AND g.recipient_id IS NULL"
	if recipientID != "" {
		var active int
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND status = 'active')`, recipientID).Scan(&active); err != nil {
			return GuestbookPage{}, err
		}
		if active == 0 {
			return GuestbookPage{}, ErrNotFound
		}
		where = "WHERE g.status = ? AND g.recipient_id = ?"
		args = append(args, recipientID)
	}
	if cursorValue != "" {
		decoded, err := decodeCursor(cursorValue)
		if err != nil {
			return GuestbookPage{}, errors.New("invalid cursor")
		}
		where += " AND (g.created_at < ? OR (g.created_at = ? AND g.id < ?))"
		args = append(args, decoded.Sort, decoded.Sort, decoded.ID)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, guestbookSelect()+" "+where+" ORDER BY g.created_at DESC, g.id DESC LIMIT ?", args...)
	if err != nil {
		return GuestbookPage{}, err
	}
	defer rows.Close()
	entries := make([]GuestbookEntry, 0, limit+1)
	for rows.Next() {
		entry, err := scanGuestbookEntry(rows)
		if err != nil {
			return GuestbookPage{}, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return GuestbookPage{}, err
	}
	for index := range entries {
		if err := s.loadGuestbookMedia(ctx, &entries[index]); err != nil {
			return GuestbookPage{}, err
		}
	}
	page := GuestbookPage{Entries: entries}
	if len(entries) > limit {
		last := entries[limit-1]
		page.Entries = entries[:limit]
		page.NextCursor = encodeCursor(cursor{Sort: last.CreatedAt.Format(time.RFC3339Nano), ID: last.ID})
	}
	return page, nil
}

func (s *Store) HideGuestbookEntry(ctx context.Context, actor identity.User, id, ip string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE guestbook_entries SET status = 'hidden', updated_at = ?
		WHERE id = ? AND status = 'visible' AND (? = 'admin' OR recipient_id = ?)`, nowText(), id, actor.Role, actor.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrForbidden
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'guestbook.hide', 'guestbook_entry', ?, ?, ?)`, actor.ID, id, ip, nowText())
	return nil
}

func (s *Store) RestoreGuestbookEntry(ctx context.Context, actor identity.User, id, ip string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE guestbook_entries SET status = 'visible', updated_at = ?
		WHERE id = ? AND status = 'hidden' AND (? = 'admin' OR recipient_id = ?)`, nowText(), id, actor.Role, actor.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrForbidden
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'guestbook.restore', 'guestbook_entry', ?, ?, ?)`, actor.ID, id, ip, nowText())
	return nil
}

func (s *Store) DeleteGuestbookEntry(ctx context.Context, actor identity.User, id, ip string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE guestbook_entries SET status = 'deleted', deleted_at = ?, updated_at = ?
		WHERE id = ? AND status <> 'deleted' AND (author_id = ? OR ? = 'admin')`, nowText(), nowText(), id, actor.ID, actor.Role)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrForbidden
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'guestbook.delete', 'guestbook_entry', ?, ?, ?)`, actor.ID, id, ip, nowText())
	return nil
}

func (s *Store) ToggleLike(ctx context.Context, actor identity.User, postID, ip string) (bool, int, error) {
	var postAuthorID string
	if err := s.db.QueryRowContext(ctx, `SELECT author_id FROM posts WHERE id = ? AND status = 'published' AND visibility = 'members'`, postID).Scan(&postAuthorID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, 0, ErrForbidden
		}
		return false, 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reactions WHERE post_id = ? AND user_id = ? AND kind = 'like')`, postID, actor.ID).Scan(&exists); err != nil {
		return false, 0, err
	}
	liked := exists == 0
	if liked {
		_, err = tx.ExecContext(ctx, `INSERT INTO reactions(post_id, user_id, kind, created_at) VALUES(?, ?, 'like', ?)`, postID, actor.ID, nowText())
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM reactions WHERE post_id = ? AND user_id = ? AND kind = 'like'`, postID, actor.ID)
	}
	if err != nil {
		return false, 0, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM reactions WHERE post_id = ? AND kind = 'like'`, postID).Scan(&count); err != nil {
		return false, 0, err
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, ?, 'post', ?, ?, ?)`, actor.ID, map[bool]string{true: "post.like", false: "post.unlike"}[liked], postID, ip, nowText())
	if liked && postAuthorID != actor.ID {
		if err := messaging.CreateNotification(ctx, tx, messaging.NotificationInput{UserID: postAuthorID, ActorID: actor.ID, Kind: "post_like", TargetType: "post", TargetID: postID, Title: actor.Nickname + "赞了你的回忆", EventKey: "like:" + postID + ":" + actor.ID}); err != nil {
			return false, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return liked, count, nil
}

func (s *Store) Get(ctx context.Context, actor identity.User, id string) (Post, error) {
	post, _, err := s.scanOne(ctx, actor.ID, `WHERE p.id = ?`, id)
	if err != nil {
		return Post{}, err
	}
	if !canRead(actor, post) {
		return Post{}, ErrForbidden
	}
	return post, nil
}

func (s *Store) List(ctx context.Context, actor identity.User, options ListOptions) (Page, error) {
	if options.Limit <= 0 || options.Limit > 50 {
		options.Limit = 20
	}
	where := "WHERE p.status = 'published' AND p.visibility = 'members'"
	args := []any{actor.ID}
	sortExpr := "COALESCE(p.published_at, p.updated_at)"
	switch options.Scope {
	case "", "feed":
	case "mine":
		where = "WHERE p.author_id = ? AND p.status != 'deleted'"
		args = append(args, actor.ID)
		sortExpr = "p.updated_at"
	case "pending":
		if actor.Role != "admin" {
			return Page{}, ErrForbidden
		}
		where = "WHERE p.status = 'pending'"
		sortExpr = "p.submitted_at"
	default:
		return Page{}, errors.New("invalid list scope")
	}
	if options.Status != "" && options.Scope == "mine" {
		if !validStatus(options.Status) || options.Status == "deleted" {
			return Page{}, errors.New("invalid status filter")
		}
		where += " AND p.status = ?"
		args = append(args, options.Status)
	}
	if options.Cursor != "" {
		decoded, err := decodeCursor(options.Cursor)
		if err != nil {
			return Page{}, errors.New("invalid cursor")
		}
		where += fmt.Sprintf(" AND (%s < ? OR (%s = ? AND p.id < ?))", sortExpr, sortExpr)
		args = append(args, decoded.Sort, decoded.Sort, decoded.ID)
	}
	query := postSelect() + " " + where + " ORDER BY " + sortExpr + " DESC, p.id DESC LIMIT ?"
	args = append(args, options.Limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	posts := make([]Post, 0, options.Limit)
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return Page{}, err
		}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if err := rows.Close(); err != nil {
		return Page{}, err
	}
	for index := range posts {
		if err := s.loadTags(ctx, &posts[index]); err != nil {
			return Page{}, err
		}
		if err := s.loadMedia(ctx, &posts[index]); err != nil {
			return Page{}, err
		}
	}
	page := Page{Posts: posts}
	if len(posts) > options.Limit {
		last := posts[options.Limit-1]
		page.Posts = posts[:options.Limit]
		sortTime := last.PublishedAt
		if options.Scope == "mine" {
			sortTime = &last.UpdatedAt
		} else if options.Scope == "pending" {
			sortTime = last.SubmittedAt
		}
		if sortTime != nil {
			page.NextCursor = encodeCursor(cursor{Sort: sortTime.Format(time.RFC3339Nano), ID: last.ID})
		}
	}
	return page, nil
}

type scanner interface{ Scan(...any) error }

func (s *Store) scanOne(ctx context.Context, viewerID, where string, args ...any) (Post, string, error) {
	queryArgs := append([]any{viewerID}, args...)
	row := s.db.QueryRowContext(ctx, postSelect()+" "+where, queryArgs...)
	post, err := scanPost(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Post{}, "", ErrNotFound
	}
	if err != nil {
		return Post{}, "", err
	}
	if err := s.loadTags(ctx, &post); err != nil {
		return Post{}, "", err
	}
	if err := s.loadMedia(ctx, &post); err != nil {
		return Post{}, "", err
	}
	return post, post.Author.ID, nil
}

func postSelect() string {
	return `SELECT p.id, p.body, p.status, p.visibility, p.content_date, p.moderation_note,
		p.submitted_at, p.published_at, p.created_at, p.updated_at, p.external_video_url,
		u.id, u.username, pr.nickname, pr.avatar_path,
		(SELECT COUNT(*) FROM comments c WHERE c.post_id = p.id AND c.status = 'visible'),
		(SELECT COUNT(*) FROM reactions r WHERE r.post_id = p.id AND r.kind = 'like'),
		EXISTS(SELECT 1 FROM reactions mine WHERE mine.post_id = p.id AND mine.user_id = ? AND mine.kind = 'like')
		FROM posts p JOIN users u ON u.id = p.author_id JOIN profiles pr ON pr.user_id = u.id`
}

func scanPost(row scanner) (Post, error) {
	var post Post
	var contentDate, submittedAt, publishedAt sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(&post.ID, &post.Body, &post.Status, &post.Visibility, &contentDate, &post.ModerationNote,
		&submittedAt, &publishedAt, &createdAt, &updatedAt, &post.ExternalVideoURL,
		&post.Author.ID, &post.Author.Username, &post.Author.Nickname, &post.Author.AvatarPath,
		&post.CommentCount, &post.LikeCount, &post.LikedByMe)
	if err != nil {
		return Post{}, err
	}
	post.ContentDate = parseOptionalTime(contentDate)
	post.SubmittedAt = parseOptionalTime(submittedAt)
	post.PublishedAt = parseOptionalTime(publishedAt)
	post.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	post.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	post.Tags = []string{}
	post.Media = []Media{}
	return post, nil
}

func (s *Store) loadTags(ctx context.Context, post *Post) error {
	rows, err := s.db.QueryContext(ctx, `SELECT t.name FROM tags t JOIN content_tags ct ON ct.tag_id = t.id WHERE ct.post_id = ? ORDER BY t.name COLLATE NOCASE`, post.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		post.Tags = append(post.Tags, name)
	}
	return rows.Err()
}

func (s *Store) loadMedia(ctx context.Context, post *Post) error {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.original_filename, m.media_type, m.mime_type, m.size_bytes, m.status, m.width, m.height, m.duration_ms, m.preview_path <> ''
		FROM media m JOIN post_media pm ON pm.media_id = m.id WHERE pm.post_id = ? AND m.status <> 'deleted' ORDER BY pm.position`, post.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item Media
		var width, height, duration sql.NullInt64
		if err := rows.Scan(&item.ID, &item.OriginalFilename, &item.MediaType, &item.MimeType, &item.SizeBytes, &item.Status, &width, &height, &duration, &item.HasPreview); err != nil {
			return err
		}
		if width.Valid {
			value := int(width.Int64)
			item.Width = &value
		}
		if height.Valid {
			value := int(height.Int64)
			item.Height = &value
		}
		if duration.Valid {
			item.DurationMS = &duration.Int64
		}
		post.Media = append(post.Media, item)
	}
	return rows.Err()
}

func (s *Store) getGuestbookEntry(ctx context.Context, id string) (GuestbookEntry, error) {
	entry, err := scanGuestbookEntry(s.db.QueryRowContext(ctx, guestbookSelect()+" WHERE g.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return GuestbookEntry{}, ErrNotFound
	}
	if err != nil {
		return GuestbookEntry{}, err
	}
	if err := s.loadGuestbookMedia(ctx, &entry); err != nil {
		return GuestbookEntry{}, err
	}
	return entry, nil
}

func guestbookSelect() string {
	return `SELECT g.id, g.body, g.external_video_url, g.status, g.created_at, g.updated_at,
		a.id, a.username, ap.nickname, ap.avatar_path,
		r.id, r.username, rp.nickname, rp.avatar_path
		FROM guestbook_entries g
		JOIN users a ON a.id = g.author_id JOIN profiles ap ON ap.user_id = a.id
		LEFT JOIN users r ON r.id = g.recipient_id LEFT JOIN profiles rp ON rp.user_id = r.id`
}

func scanGuestbookEntry(row scanner) (GuestbookEntry, error) {
	var entry GuestbookEntry
	var created, updated string
	var recipientID, recipientUsername, recipientNickname, recipientAvatar sql.NullString
	err := row.Scan(&entry.ID, &entry.Body, &entry.ExternalVideoURL, &entry.Status, &created, &updated,
		&entry.Author.ID, &entry.Author.Username, &entry.Author.Nickname, &entry.Author.AvatarPath,
		&recipientID, &recipientUsername, &recipientNickname, &recipientAvatar)
	if err != nil {
		return GuestbookEntry{}, err
	}
	entry.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	entry.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	entry.Media = []Media{}
	if recipientID.Valid {
		entry.Recipient = &Author{ID: recipientID.String, Username: recipientUsername.String, Nickname: recipientNickname.String, AvatarPath: recipientAvatar.String}
	}
	return entry, nil
}

func (s *Store) loadGuestbookMedia(ctx context.Context, entry *GuestbookEntry) error {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.original_filename, m.media_type, m.mime_type, m.size_bytes, m.status, m.width, m.height, m.duration_ms, m.preview_path <> ''
		FROM media m JOIN guestbook_media gm ON gm.media_id = m.id WHERE gm.entry_id = ? AND m.status <> 'deleted' ORDER BY gm.position`, entry.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item Media
		var width, height, duration sql.NullInt64
		if err := rows.Scan(&item.ID, &item.OriginalFilename, &item.MediaType, &item.MimeType, &item.SizeBytes, &item.Status, &width, &height, &duration, &item.HasPreview); err != nil {
			return err
		}
		if width.Valid {
			value := int(width.Int64)
			item.Width = &value
		}
		if height.Valid {
			value := int(height.Int64)
			item.Height = &value
		}
		if duration.Valid {
			item.DurationMS = &duration.Int64
		}
		entry.Media = append(entry.Media, item)
	}
	return rows.Err()
}

func replaceTags(ctx context.Context, tx *sql.Tx, postID string, tags []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM content_tags WHERE post_id = ?", postID); err != nil {
		return err
	}
	for _, name := range tags {
		id := newID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO tags(id, name, created_at) VALUES(?, ?, ?) ON CONFLICT(name) DO NOTHING`, id, name, nowText()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO content_tags(post_id, tag_id) SELECT ?, id FROM tags WHERE name = ? COLLATE NOCASE`, postID, name); err != nil {
			return err
		}
	}
	return nil
}

func replaceMedia(ctx context.Context, tx *sql.Tx, ownerID, postID string, mediaIDs []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM post_media WHERE post_id = ?", postID); err != nil {
		return err
	}
	for position, mediaID := range mediaIDs {
		result, err := tx.ExecContext(ctx, `INSERT INTO post_media(post_id, media_id, position)
			SELECT ?, id, ? FROM media WHERE id = ? AND owner_id = ? AND status = 'ready'`, postID, position, mediaID, ownerID)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrForbidden
		}
	}
	return nil
}

func replaceGuestbookMedia(ctx context.Context, tx *sql.Tx, ownerID, entryID string, mediaIDs []string) error {
	for position, mediaID := range mediaIDs {
		result, err := tx.ExecContext(ctx, `INSERT INTO guestbook_media(entry_id, media_id, position)
			SELECT ?, id, ? FROM media WHERE id = ? AND owner_id = ? AND status = 'ready'`, entryID, position, mediaID, ownerID)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return ErrForbidden
		}
	}
	return nil
}

func validateGuestbookInput(input GuestbookInput) (GuestbookInput, error) {
	input.Body = strings.TrimSpace(input.Body)
	input.RecipientID = strings.TrimSpace(input.RecipientID)
	externalVideoURL, err := normalizeExternalVideo(input.ExternalVideoURL)
	if err != nil {
		return input, err
	}
	input.ExternalVideoURL = externalVideoURL
	if input.RecipientID != "" && len(input.RecipientID) != 32 {
		return input, errors.New("invalid guestbook recipient")
	}
	if len([]rune(input.Body)) > 2000 {
		return input, errors.New("guestbook message must be at most 2000 characters")
	}
	if input.Body == "" && len(input.MediaIDs) == 0 && input.ExternalVideoURL == "" {
		return input, errors.New("guestbook message, media, or external video is required")
	}
	if len(input.MediaIDs) > 6 {
		return input, errors.New("a guestbook entry can have at most 6 media files")
	}
	seen := make(map[string]bool)
	clean := make([]string, 0, len(input.MediaIDs))
	for _, id := range input.MediaIDs {
		id = strings.TrimSpace(id)
		if len(id) != 32 {
			return input, errors.New("invalid media id")
		}
		if !seen[id] {
			seen[id] = true
			clean = append(clean, id)
		}
	}
	input.MediaIDs = clean
	return input, nil
}

func validateWrite(input WriteInput) (WriteInput, *time.Time, error) {
	input.Body = strings.TrimSpace(input.Body)
	externalVideoURL, err := normalizeExternalVideo(input.ExternalVideoURL)
	if err != nil {
		return input, nil, err
	}
	input.ExternalVideoURL = externalVideoURL
	if len([]rune(input.Body)) > 10000 {
		return input, nil, errors.New("content body is too long")
	}
	if input.Submit && input.Body == "" && len(input.MediaIDs) == 0 && input.ExternalVideoURL == "" {
		return input, nil, errors.New("content body is required before submission")
	}
	if input.Visibility == "" {
		input.Visibility = "members"
	}
	if input.Visibility != "members" && input.Visibility != "private" {
		return input, nil, errors.New("invalid visibility")
	}
	var contentDate *time.Time
	if input.ContentDate != "" {
		parsed, err := time.Parse("2006-01-02", input.ContentDate)
		if err != nil {
			return input, nil, errors.New("content_date must use YYYY-MM-DD")
		}
		parsed = parsed.UTC()
		contentDate = &parsed
	}
	if len(input.Tags) > 10 {
		return input, nil, errors.New("a post can have at most 10 tags")
	}
	seen := make(map[string]bool)
	cleanTags := make([]string, 0, len(input.Tags))
	for _, tag := range input.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len([]rune(tag)) > 30 {
			return input, nil, errors.New("tags must be 1-30 characters")
		}
		key := strings.ToLower(tag)
		if !seen[key] {
			seen[key] = true
			cleanTags = append(cleanTags, tag)
		}
	}
	input.Tags = cleanTags
	if len(input.MediaIDs) > 20 {
		return input, nil, errors.New("a post can have at most 20 media files")
	}
	seenMedia := make(map[string]bool)
	cleanMedia := make([]string, 0, len(input.MediaIDs))
	for _, mediaID := range input.MediaIDs {
		mediaID = strings.TrimSpace(mediaID)
		if len(mediaID) != 32 {
			return input, nil, errors.New("invalid media id")
		}
		if !seenMedia[mediaID] {
			seenMedia[mediaID] = true
			cleanMedia = append(cleanMedia, mediaID)
		}
	}
	input.MediaIDs = cleanMedia
	return input, contentDate, nil
}

func normalizeExternalVideo(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	fromIframe := strings.Contains(strings.ToLower(value), "<iframe")
	if fromIframe {
		matches := iframeSource.FindStringSubmatch(value)
		if len(matches) == 0 {
			return "", errors.New("iframe embed code does not contain a valid src")
		}
		value = ""
		for _, candidate := range matches[1:] {
			if candidate != "" {
				value = html.UnescapeString(candidate)
				break
			}
		}
	}
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	}
	if len(value) > 2048 {
		return "", errors.New("external video URL is too long")
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("external video must be a valid http or https URL or iframe embed code")
	}
	if fromIframe && !allowedEmbedHost(parsed.Hostname()) {
		return "", errors.New("iframe player domain is not supported")
	}
	return parsed.String(), nil
}

func allowedEmbedHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "player.bilibili.com" || host == "www.youtube.com" || host == "youtube.com" || host == "www.youtube-nocookie.com"
}

func canRead(actor identity.User, post Post) bool {
	if actor.Role == "admin" || post.Author.ID == actor.ID {
		return true
	}
	return post.Status == "published" && post.Visibility == "members"
}

func validStatus(value string) bool {
	return value == "draft" || value == "pending" || value == "published" || value == "hidden" || value == "deleted"
}

func parseOptionalTime(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func nullableDate(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Format(time.RFC3339Nano)
}

func encodeCursor(value cursor) string {
	data, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(value string) (cursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor{}, err
	}
	var decoded cursor
	err = json.Unmarshal(data, &decoded)
	if err != nil || decoded.Sort == "" || decoded.ID == "" {
		return cursor{}, errors.New("invalid cursor")
	}
	return decoded, nil
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func nowText() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}
