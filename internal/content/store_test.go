package content

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dorm-memorial/internal/database"
	"dorm-memorial/internal/identity"
)

func TestPostDraftModerationAndFeedPermissions(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "content.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	identities := identity.NewStore(db)
	if created, err := identities.BootstrapAdmin(ctx, "admin", "admin@example.com", "correct-horse-battery", "管理员"); err != nil || !created {
		t.Fatalf("bootstrap created=%v err=%v", created, err)
	}
	admin, err := identities.Authenticate(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := identities.CreateInvite(ctx, admin, 1, time.Hour, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	member, err := identities.Register(ctx, identity.RegisterInput{InviteCode: code, Username: "member", Email: "member@example.com", Password: "member-password", Nickname: "室友"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	draft, err := store.Create(ctx, member, WriteInput{Body: "第一段宿舍回忆", ContentDate: "2024-09-01", Visibility: "members", Tags: []string{"开学", " 开学 "}}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Status != "draft" || len(draft.Tags) != 1 {
		t.Fatalf("draft=%+v", draft)
	}
	if _, err := store.Get(ctx, admin, draft.ID); err != nil {
		t.Fatalf("admin read draft: %v", err)
	}
	otherCode, _, _ := identities.CreateInvite(ctx, admin, 1, time.Hour, "127.0.0.1")
	other, _ := identities.Register(ctx, identity.RegisterInput{InviteCode: otherCode, Username: "other", Email: "other@example.com", Password: "other-password", Nickname: "另一位"}, "127.0.0.1")
	if _, err := store.Get(ctx, other, draft.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other member draft access err=%v", err)
	}

	pending, err := store.Submit(ctx, member, draft.ID, "127.0.0.1")
	if err != nil || pending.Status != "pending" {
		t.Fatalf("submit status=%q err=%v", pending.Status, err)
	}
	feed, err := store.List(ctx, other, ListOptions{Scope: "feed"})
	if err != nil || len(feed.Posts) != 0 {
		t.Fatalf("feed before approval=%d err=%v", len(feed.Posts), err)
	}
	if _, err := store.List(ctx, member, ListOptions{Scope: "pending"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member pending list err=%v", err)
	}

	published, err := store.Moderate(ctx, admin, draft.ID, "approve", "", "127.0.0.1")
	if err != nil || published.Status != "published" || published.PublishedAt == nil {
		t.Fatalf("approve post=%+v err=%v", published, err)
	}
	feed, err = store.List(ctx, other, ListOptions{Scope: "feed", Limit: 10})
	if err != nil || len(feed.Posts) != 1 || feed.Posts[0].ID != draft.ID {
		t.Fatalf("feed after approval=%+v err=%v", feed, err)
	}
	if err := store.Delete(ctx, member, draft.ID, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	feed, err = store.List(ctx, other, ListOptions{Scope: "feed"})
	if err != nil || len(feed.Posts) != 0 {
		t.Fatalf("feed after delete=%+v err=%v", feed, err)
	}
}
