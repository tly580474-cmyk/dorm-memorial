package messaging

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dorm-memorial/internal/database"
	"dorm-memorial/internal/identity"
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

	first, err := store.SendMessage(ctx, alice, direct.ID, "毕业后也要常联系。", "127.0.0.1")
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

	reply, err := store.SendMessage(ctx, bob, direct.ID, "一定。", "127.0.0.1")
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
	if err != nil || notifications.UnreadCount != 1 || len(notifications.Notifications) != 1 || notifications.Notifications[0].Kind != "direct_message" {
		t.Fatalf("notifications=%+v err=%v", notifications, err)
	}
	if err := store.MarkNotificationRead(ctx, bob, notifications.Notifications[0].ID); err != nil {
		t.Fatal(err)
	}
	notifications, _ = store.ListNotifications(ctx, bob, "", 20)
	if notifications.UnreadCount != 0 {
		t.Fatalf("notification unread=%d", notifications.UnreadCount)
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
