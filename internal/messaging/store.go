package messaging

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"dorm-memorial/internal/identity"
)

var (
	ErrNotFound  = errors.New("message not found")
	ErrForbidden = errors.New("message access forbidden")
	ErrConflict  = errors.New("message state conflict")
)

type Person struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	Nickname   string `json:"nickname"`
	AvatarPath string `json:"avatar_path"`
}

type Message struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	Sender         Person     `json:"sender"`
	Body           string     `json:"body"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	RecalledAt     *time.Time `json:"recalled_at"`
}

type Conversation struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Peer        *Person  `json:"peer"`
	LastMessage *Message `json:"last_message"`
	UnreadCount int      `json:"unread_count"`
}

type MessagePage struct {
	Messages   []Message `json:"messages"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type Notification struct {
	ID         string     `json:"id"`
	Actor      *Person    `json:"actor"`
	Kind       string     `json:"kind"`
	TargetType string     `json:"target_type"`
	TargetID   string     `json:"target_id"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	CreatedAt  time.Time  `json:"created_at"`
	ReadAt     *time.Time `json:"read_at"`
}

type NotificationPage struct {
	Notifications []Notification `json:"notifications"`
	NextCursor    string         `json:"next_cursor,omitempty"`
	UnreadCount   int            `json:"unread_count"`
}

type NotificationInput struct {
	UserID     string
	ActorID    string
	Kind       string
	TargetType string
	TargetID   string
	Title      string
	Body       string
	EventKey   string
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) ListConversations(ctx context.Context, actor identity.User) ([]Conversation, error) {
	if err := s.ensureDormConversation(ctx, actor.ID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.type, c.title
		FROM conversations c JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE cm.user_id = ? ORDER BY c.updated_at DESC, c.id DESC`, actor.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Conversation{}
	for rows.Next() {
		var item Conversation
		if err := rows.Scan(&item.ID, &item.Type, &item.Title); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range items {
		if items[index].Type == "direct" {
			peer, err := s.directPeer(ctx, items[index].ID, actor.ID)
			if err != nil {
				return nil, err
			}
			items[index].Peer = &peer
			items[index].Title = peer.Nickname
		}
		last, err := s.lastMessage(ctx, items[index].ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if err == nil {
			items[index].LastMessage = &last
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages m JOIN conversation_members cm ON cm.conversation_id = m.conversation_id
			WHERE m.conversation_id = ? AND cm.user_id = ? AND m.sender_id <> ? AND m.created_at > cm.last_read_at`, items[index].ID, actor.ID, actor.ID).Scan(&items[index].UnreadCount); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) StartDirect(ctx context.Context, actor identity.User, recipientID string) (Conversation, error) {
	recipientID = strings.TrimSpace(recipientID)
	if recipientID == "" || recipientID == actor.ID {
		return Conversation{}, errors.New("invalid direct message recipient")
	}
	var active int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND status = 'active')`, recipientID).Scan(&active); err != nil {
		return Conversation{}, err
	}
	if active == 0 {
		return Conversation{}, ErrNotFound
	}
	ids := []string{actor.ID, recipientID}
	sort.Strings(ids)
	key := ids[0] + ":" + ids[1]
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM conversations WHERE direct_key = ?`, key).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		id, err = s.createDirect(ctx, actor.ID, recipientID, key)
		if err != nil {
			return Conversation{}, err
		}
	}
	items, err := s.ListConversations(ctx, actor)
	if err != nil {
		return Conversation{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Conversation{}, ErrNotFound
}

func (s *Store) ListMessages(ctx context.Context, actor identity.User, conversationID, cursorValue string, limit int) (MessagePage, error) {
	if err := s.requireMember(ctx, conversationID, actor.ID); err != nil {
		return MessagePage{}, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	where := "WHERE m.conversation_id = ?"
	args := []any{conversationID}
	if cursorValue != "" {
		cursor, err := decodeCursor(cursorValue)
		if err != nil {
			return MessagePage{}, errors.New("invalid message cursor")
		}
		where += " AND (m.created_at < ? OR (m.created_at = ? AND m.id < ?))"
		args = append(args, cursor.Sort, cursor.Sort, cursor.ID)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, messageSelect()+" "+where+" ORDER BY m.created_at DESC, m.id DESC LIMIT ?", args...)
	if err != nil {
		return MessagePage{}, err
	}
	defer rows.Close()
	desc := []Message{}
	for rows.Next() {
		item, err := scanMessage(rows)
		if err != nil {
			return MessagePage{}, err
		}
		desc = append(desc, item)
	}
	if err := rows.Err(); err != nil {
		return MessagePage{}, err
	}
	page := MessagePage{}
	if len(desc) > limit {
		oldest := desc[limit-1]
		page.NextCursor = encodeCursor(messageCursor{Sort: oldest.CreatedAt.Format(time.RFC3339Nano), ID: oldest.ID})
		desc = desc[:limit]
	}
	for index := len(desc) - 1; index >= 0; index-- {
		page.Messages = append(page.Messages, desc[index])
	}
	return page, nil
}

func (s *Store) SendMessage(ctx context.Context, actor identity.User, conversationID, body, ip string) (Message, error) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > 4000 {
		return Message{}, errors.New("message must be 1-4000 characters")
	}
	if err := s.requireMember(ctx, conversationID, actor.ID); err != nil {
		return Message{}, err
	}
	id, now := newID(), time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id, conversation_id, sender_id, body, status, created_at) VALUES(?, ?, ?, ?, 'sent', ?)`, id, conversationID, actor.ID, body, nowText(now)); err != nil {
		return Message{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, nowText(now), conversationID); err != nil {
		return Message{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversation_members SET last_read_at = ? WHERE conversation_id = ? AND user_id = ?`, nowText(now), conversationID, actor.ID); err != nil {
		return Message{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT cm.user_id, c.type FROM conversation_members cm JOIN conversations c ON c.id = cm.conversation_id WHERE cm.conversation_id = ? AND cm.user_id <> ?`, conversationID, actor.ID)
	if err != nil {
		return Message{}, err
	}
	recipients := []string{}
	conversationType := "direct"
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID, &conversationType); err != nil {
			rows.Close()
			return Message{}, err
		}
		recipients = append(recipients, userID)
	}
	if err := rows.Close(); err != nil {
		return Message{}, err
	}
	for _, userID := range recipients {
		title := actor.Nickname + "发来一条私信"
		kind := "direct_message"
		if conversationType == "group" {
			title = actor.Nickname + "在宿舍群聊中发了消息"
			kind = "group_message"
		}
		if err := CreateNotification(ctx, tx, NotificationInput{UserID: userID, ActorID: actor.ID, Kind: kind, TargetType: "conversation", TargetID: conversationID, Title: title, EventKey: "message:" + id + ":" + userID}); err != nil {
			return Message{}, err
		}
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'message.send', 'message', ?, ?, ?)`, actor.ID, id, ip, nowText(now))
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return s.messageByID(ctx, id)
}

func (s *Store) MarkConversationRead(ctx context.Context, actor identity.User, conversationID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE conversation_members SET last_read_at = ? WHERE conversation_id = ? AND user_id = ?`, nowText(time.Now().UTC()), conversationID, actor.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrForbidden
	}
	return nil
}

func (s *Store) RecallMessage(ctx context.Context, actor identity.User, id, ip string) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE messages SET status = 'recalled', recalled_at = ?
		WHERE id = ? AND sender_id = ? AND status = 'sent' AND created_at >= ?`, nowText(now), id, actor.ID, nowText(now.Add(-2*time.Minute)))
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrConflict
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit_logs(actor_id, action, target_type, target_id, ip_address, created_at) VALUES(?, 'message.recall', 'message', ?, ?, ?)`, actor.ID, id, ip, nowText(now))
	return nil
}

func (s *Store) ListNotifications(ctx context.Context, actor identity.User, cursorValue string, limit int) (NotificationPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	where := "WHERE n.user_id = ?"
	args := []any{actor.ID}
	if cursorValue != "" {
		cursor, err := decodeCursor(cursorValue)
		if err != nil {
			return NotificationPage{}, errors.New("invalid notification cursor")
		}
		where += " AND (n.created_at < ? OR (n.created_at = ? AND n.id < ?))"
		args = append(args, cursor.Sort, cursor.Sort, cursor.ID)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT n.id, n.kind, n.target_type, n.target_id, n.title, n.body, n.created_at, n.read_at,
		u.id, u.username, p.nickname, p.avatar_path
		FROM notifications n LEFT JOIN users u ON u.id = n.actor_id LEFT JOIN profiles p ON p.user_id = u.id `+where+` ORDER BY n.created_at DESC, n.id DESC LIMIT ?`, args...)
	if err != nil {
		return NotificationPage{}, err
	}
	defer rows.Close()
	items := []Notification{}
	for rows.Next() {
		item, err := scanNotification(rows)
		if err != nil {
			return NotificationPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return NotificationPage{}, err
	}
	page := NotificationPage{Notifications: items}
	if len(items) > limit {
		last := items[limit-1]
		page.Notifications = items[:limit]
		page.NextCursor = encodeCursor(messageCursor{Sort: last.CreatedAt.Format(time.RFC3339Nano), ID: last.ID})
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read_at IS NULL`, actor.ID).Scan(&page.UnreadCount); err != nil {
		return NotificationPage{}, err
	}
	return page, nil
}

func (s *Store) MarkNotificationRead(ctx context.Context, actor identity.User, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = COALESCE(read_at, ?) WHERE id = ? AND user_id = ?`, nowText(time.Now().UTC()), id, actor.ID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkAllNotificationsRead(ctx context.Context, actor identity.User) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = ? WHERE user_id = ? AND read_at IS NULL`, nowText(time.Now().UTC()), actor.ID)
	return err
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func CreateNotification(ctx context.Context, executor sqlExecutor, input NotificationInput) error {
	if input.UserID == "" || input.EventKey == "" || input.Title == "" {
		return errors.New("invalid notification")
	}
	var actor any
	if input.ActorID != "" {
		actor = input.ActorID
	}
	_, err := executor.ExecContext(ctx, `INSERT OR IGNORE INTO notifications(id, user_id, actor_id, kind, target_type, target_id, title, body, event_key, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, newID(), input.UserID, actor, input.Kind, input.TargetType, input.TargetID, input.Title, input.Body, input.EventKey, nowText(time.Now().UTC()))
	return err
}

func (s *Store) ensureDormConversation(ctx context.Context, actorID string) error {
	now := nowText(time.Now().UTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM conversations WHERE type = 'group' ORDER BY created_at LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id = newID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO conversations(id, type, title, created_at, updated_at) VALUES(?, 'group', '3048 宿舍群聊', ?, ?)`, id, now, now); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO conversation_members(conversation_id, user_id, joined_at, last_read_at)
		SELECT ?, id, ?, ? FROM users WHERE status = 'active'`, id, now, now); err != nil {
		return err
	}
	var member int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conversation_members WHERE conversation_id = ? AND user_id = ?)`, id, actorID).Scan(&member); err != nil {
		return err
	}
	if member == 0 {
		return ErrForbidden
	}
	return tx.Commit()
}

func (s *Store) createDirect(ctx context.Context, actorID, recipientID, key string) (string, error) {
	id, now := newID(), nowText(time.Now().UTC())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO conversations(id, type, direct_key, created_by, created_at, updated_at) VALUES(?, 'direct', ?, ?, ?, ?)`, id, key, actorID, now, now); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			if scanErr := tx.QueryRowContext(ctx, `SELECT id FROM conversations WHERE direct_key = ?`, key).Scan(&id); scanErr == nil {
				return id, nil
			}
		}
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO conversation_members(conversation_id, user_id, joined_at, last_read_at) VALUES(?, ?, ?, ?), (?, ?, ?, ?)`, id, actorID, now, now, id, recipientID, now, now); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) requireMember(ctx context.Context, conversationID, userID string) error {
	var member int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conversation_members WHERE conversation_id = ? AND user_id = ?)`, conversationID, userID).Scan(&member); err != nil {
		return err
	}
	if member == 0 {
		return ErrForbidden
	}
	return nil
}

func (s *Store) directPeer(ctx context.Context, conversationID, actorID string) (Person, error) {
	var peer Person
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.username, p.nickname, p.avatar_path FROM conversation_members cm
		JOIN users u ON u.id = cm.user_id JOIN profiles p ON p.user_id = u.id
		WHERE cm.conversation_id = ? AND cm.user_id <> ? LIMIT 1`, conversationID, actorID).Scan(&peer.ID, &peer.Username, &peer.Nickname, &peer.AvatarPath)
	if errors.Is(err, sql.ErrNoRows) {
		return Person{}, ErrNotFound
	}
	return peer, err
}

func (s *Store) lastMessage(ctx context.Context, conversationID string) (Message, error) {
	item, err := scanMessage(s.db.QueryRowContext(ctx, messageSelect()+` WHERE m.conversation_id = ? ORDER BY m.created_at DESC, m.id DESC LIMIT 1`, conversationID))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	return item, err
}

func (s *Store) messageByID(ctx context.Context, id string) (Message, error) {
	item, err := scanMessage(s.db.QueryRowContext(ctx, messageSelect()+` WHERE m.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	return item, err
}

func messageSelect() string {
	return `SELECT m.id, m.conversation_id, m.body, m.status, m.created_at, m.recalled_at,
		u.id, u.username, p.nickname, p.avatar_path FROM messages m
		JOIN users u ON u.id = m.sender_id JOIN profiles p ON p.user_id = u.id`
}

type scanner interface{ Scan(...any) error }

func scanMessage(row scanner) (Message, error) {
	var item Message
	var created string
	var recalled sql.NullString
	if err := row.Scan(&item.ID, &item.ConversationID, &item.Body, &item.Status, &created, &recalled, &item.Sender.ID, &item.Sender.Username, &item.Sender.Nickname, &item.Sender.AvatarPath); err != nil {
		return Message{}, err
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if recalled.Valid {
		value, _ := time.Parse(time.RFC3339Nano, recalled.String)
		item.RecalledAt = &value
	}
	if item.Status == "recalled" {
		item.Body = ""
	}
	return item, nil
}

func scanNotification(row scanner) (Notification, error) {
	var item Notification
	var created string
	var readAt, actorID, username, nickname, avatar sql.NullString
	if err := row.Scan(&item.ID, &item.Kind, &item.TargetType, &item.TargetID, &item.Title, &item.Body, &created, &readAt, &actorID, &username, &nickname, &avatar); err != nil {
		return Notification{}, err
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if readAt.Valid {
		value, _ := time.Parse(time.RFC3339Nano, readAt.String)
		item.ReadAt = &value
	}
	if actorID.Valid {
		item.Actor = &Person{ID: actorID.String, Username: username.String, Nickname: nickname.String, AvatarPath: avatar.String}
	}
	return item, nil
}

type messageCursor struct {
	Sort string `json:"s"`
	ID   string `json:"i"`
}

func encodeCursor(value messageCursor) string {
	data, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(value string) (messageCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return messageCursor{}, err
	}
	var cursor messageCursor
	err = json.Unmarshal(data, &cursor)
	if err != nil || cursor.Sort == "" || cursor.ID == "" {
		return messageCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func newID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	return hex.EncodeToString(value[:])
}

func nowText(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
