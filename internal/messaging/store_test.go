package messaging

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dorm-memorial/internal/database"
	"dorm-memorial/internal/identity"
	"dorm-memorial/internal/validation"
)

func TestDirectMessagesUnreadRecallAndPrivacy(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	identities := identity.NewStore(db)
	if _, err := identities.BootstrapAdmin(ctx, "admin", "admin@example.test", "correct-horse-battery", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := identities.Authenticate(ctx, "admin", "correct-horse-battery")
	alice := registerMessagingUser(t, identities, admin, "alice", "alice@example.test", "小爱")
	bob := registerMessagingUser(t, identities, admin, "bob", "bob@example.test", "小博")
	store := NewStore(db)

	aliceConversations, err := store.ListConversations(ctx, alice)
	if err != nil || len(aliceConversations) != 1 || aliceConversations[0].Type != "group" {
		t.Fatalf("initial conversations=%+v err=%v", aliceConversations, err)
	}
	emptyGroup, err := store.ListMessages(ctx, alice, aliceConversations[0].ID, "", 20)
	if err != nil || emptyGroup.Messages == nil || len(emptyGroup.Messages) != 0 {
		t.Fatalf("empty group messages=%#v err=%v", emptyGroup.Messages, err)
	}
	direct, err := store.StartDirect(ctx, alice, bob.ID)
	if err != nil || direct.Type != "direct" || direct.Peer == nil || direct.Peer.ID != bob.ID {
		t.Fatalf("direct=%+v err=%v", direct, err)
	}
	reversed, err := store.StartDirect(ctx, bob, alice.ID)
	if err != nil || reversed.ID != direct.ID {
		t.Fatalf("reversed direct=%+v err=%v", reversed, err)
	}
	if _, err := store.ListMessages(ctx, admin, direct.ID, "", 20); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin direct access err=%v", err)
	}

	first, err := store.SendMessage(ctx, alice, direct.ID, "毕业后也要常联系。", nil, "127.0.0.1")
	if err != nil || first.Body == "" {
		t.Fatalf("first message=%+v err=%v", first, err)
	}
	bobConversations, err := store.ListConversations(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	var bobDirect Conversation
	for _, item := range bobConversations {
		if item.ID == direct.ID {
			bobDirect = item
		}
	}
	if bobDirect.UnreadCount != 1 {
		t.Fatalf("bob unread=%d", bobDirect.UnreadCount)
	}
	page, err := store.ListMessages(ctx, bob, direct.ID, "", 20)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != first.ID {
		t.Fatalf("messages=%+v err=%v", page, err)
	}
	if err := store.MarkConversationRead(ctx, bob, direct.ID); err != nil {
		t.Fatal(err)
	}
	bobConversations, _ = store.ListConversations(ctx, bob)
	for _, item := range bobConversations {
		if item.ID == direct.ID && item.UnreadCount != 0 {
			t.Fatalf("unread after mark=%d", item.UnreadCount)
		}
	}

	reply, err := store.SendMessage(ctx, bob, direct.ID, "一定。", nil, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecallMessage(ctx, alice, reply.ID, "127.0.0.1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("other member recall err=%v", err)
	}
	if err := store.RecallMessage(ctx, bob, reply.ID, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	page, _ = store.ListMessages(ctx, alice, direct.ID, "", 20)
	if len(page.Messages) != 2 || page.Messages[1].Status != "recalled" || page.Messages[1].Body != "" {
		t.Fatalf("recalled messages=%+v", page.Messages)
	}

	notifications, err := store.ListNotifications(ctx, bob, "", 20)
	if err != nil || notifications.UnreadCount != 1 || len(notifications.Notifications) != 1 || notifications.Notifications[0].Kind != "direct_message" || notifications.Notifications[0].TargetType != "message" || notifications.Notifications[0].TargetID != first.ID {
		t.Fatalf("notifications=%+v err=%v", notifications, err)
	}
	located, err := store.GetMessage(ctx, bob, first.ID)
	if err != nil || located.ID != first.ID || located.ConversationID != direct.ID {
		t.Fatalf("located message=%+v err=%v", located, err)
	}
	if _, err := store.GetMessage(ctx, admin, first.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin message lookup err=%v", err)
	}
	if err := store.MarkNotificationRead(ctx, bob, notifications.Notifications[0].ID); err != nil {
		t.Fatal(err)
	}
	notifications, _ = store.ListNotifications(ctx, bob, "", 20)
	if notifications.UnreadCount != 0 {
		t.Fatalf("notification unread=%d", notifications.UnreadCount)
	}
	if err := store.ClearNotifications(ctx, bob); err != nil {
		t.Fatal(err)
	}
	notifications, _ = store.ListNotifications(ctx, bob, "", 20)
	if len(notifications.Notifications) != 0 || notifications.UnreadCount != 0 {
		t.Fatalf("notifications after clear=%+v", notifications)
	}

	audioID := "cccccccccccccccccccccccccccccccc"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `INSERT INTO media(id, owner_id, object_path, original_filename, media_type, mime_type, size_bytes, sha256, duration_ms, status, created_at, updated_at)
		VALUES(?, ?, '/test/voice.ogg', '宿舍语音.ogg', 'audio', 'audio/ogg', 128, ?, 3200, 'ready', ?, ?)`, audioID, alice.ID, audioID, now, now); err != nil {
		t.Fatal(err)
	}
	attachmentMessage, err := store.SendMessage(ctx, alice, direct.ID, "", []string{audioID}, "127.0.0.1")
	if err != nil || attachmentMessage.Body != "" || len(attachmentMessage.Attachments) != 1 || attachmentMessage.Attachments[0].MediaType != "audio" {
		t.Fatalf("attachment message=%+v err=%v", attachmentMessage, err)
	}
	if _, err := store.SendMessage(ctx, bob, direct.ID, "不能盗用", []string{audioID}, "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-owner attachment err=%v", err)
	}
}

func TestMessageCursorHandlesCanonicalExactSecond(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "message-cursor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	identities := identity.NewStore(db)
	if _, err := identities.BootstrapAdmin(ctx, "admin", "admin@example.test", "correct-horse-battery", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, err := identities.Authenticate(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	alice := registerMessagingUser(t, identities, admin, "cursor-alice", "cursor-alice@example.test", "小爱")
	bob := registerMessagingUser(t, identities, admin, "cursor-bob", "cursor-bob@example.test", "小博")
	store := NewStore(db)
	direct, err := store.StartDirect(ctx, alice, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.SendMessage(ctx, alice, direct.ID, "旧消息", nil, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := store.SendMessage(ctx, alice, direct.ID, "新消息", nil, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	legacyOld := "2024-01-01T00:00:00Z"
	legacyNewer := "2024-01-02T00:00:00.123000Z"
	if _, err := db.ExecContext(ctx, `UPDATE messages SET created_at = CASE id WHEN ? THEN ? ELSE ? END WHERE id IN (?, ?)`, old.ID, legacyOld, legacyNewer, old.ID, newer.ID); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListMessages(ctx, alice, direct.ID, "", 1)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != newer.ID || page.NextCursor == "" {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	cursor, err := decodeCursor(page.NextCursor)
	if err != nil || cursor.Sort != legacyNewer {
		t.Fatalf("cursor sort=%q err=%v want %q", cursor.Sort, err, legacyNewer)
	}
	next, err := store.ListMessages(ctx, alice, direct.ID, page.NextCursor, 1)
	if err != nil || len(next.Messages) != 1 || next.Messages[0].ID != old.ID {
		t.Fatalf("second page=%+v err=%v oldID=%s newerID=%s cursor=%s", next, err, old.ID, newer.ID, page.NextCursor)
	}
}

func TestDisabledDirectPeerIsHiddenFromConversations(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "disabled-direct-peer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	identities := identity.NewStore(db)
	if _, err := identities.BootstrapAdmin(ctx, "admin", "admin@example.test", "correct-horse-battery", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, err := identities.Authenticate(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	alice := registerMessagingUser(t, identities, admin, "alice-hidden", "alice-hidden@example.test", "小爱")
	bob := registerMessagingUser(t, identities, admin, "bob-hidden", "bob-hidden@example.test", "小博")
	store := NewStore(db)
	direct, err := store.StartDirect(ctx, alice, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identities.UpdateAdminUser(ctx, admin, bob.ID, identity.AdminUserUpdate{Role: "member", Status: "disabled"}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	conversations, err := store.ListConversations(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range conversations {
		if item.ID == direct.ID {
			t.Fatalf("disabled direct peer must not be displayed: %+v", item)
		}
	}
	if _, err := store.StartDirect(ctx, alice, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled peer can still receive direct messages: %v", err)
	}
}

func TestAdminMessageManagementOnlyIncludesGroupChat(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "admin-messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	identities := identity.NewStore(db)
	if _, err := identities.BootstrapAdmin(ctx, "admin", "admin@example.test", "correct-horse-battery", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := identities.Authenticate(ctx, "admin", "correct-horse-battery")
	alice := registerMessagingUser(t, identities, admin, "alice2", "alice2@example.test", "小爱")
	bob := registerMessagingUser(t, identities, admin, "bob2", "bob2@example.test", "小博")
	store := NewStore(db)
	groupConversations, err := store.ListConversations(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	groupMessage, err := store.SendMessage(ctx, alice, groupConversations[0].ID, "公共消息", nil, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	direct, err := store.StartDirect(ctx, alice, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SendMessage(ctx, alice, direct.ID, "私密内容", nil, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	items, err := store.ListAdminGroupMessages(ctx, admin, "", "", 100)
	if err != nil || len(items) != 1 || items[0].ID != groupMessage.ID || strings.Contains(items[0].Body, "私密") {
		t.Fatalf("admin group messages=%+v err=%v", items, err)
	}
	if err := store.RemoveAdminGroupMessage(ctx, admin, groupMessage.ID, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	recalled, err := store.ListAdminGroupMessages(ctx, admin, "", "recalled", 100)
	if err != nil || len(recalled) != 1 || recalled[0].Body != "" {
		t.Fatalf("recalled admin messages=%+v err=%v", recalled, err)
	}
	if _, err := store.ListAdminGroupMessages(ctx, admin, "", "unknown", 100); !validation.Is(err) {
		t.Fatalf("invalid admin message status error=%v", err)
	}
	page, err := store.ListMessages(ctx, alice, groupConversations[0].ID, "", 20)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Status != "recalled" || page.Messages[0].Body != "" {
		t.Fatalf("moderated page=%+v err=%v", page, err)
	}
}

func registerMessagingUser(t *testing.T, identities *identity.Store, admin identity.User, username, email, nickname string) identity.User {
	t.Helper()
	code, _, err := identities.CreateInvite(context.Background(), admin, 1, time.Hour, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	user, err := identities.Register(context.Background(), identity.RegisterInput{InviteCode: code, Username: username, Email: email, Password: "member-password", Nickname: nickname}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	return user
}
