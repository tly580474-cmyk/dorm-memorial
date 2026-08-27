package content

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
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
	draft, err := store.Create(ctx, member, WriteInput{Title: "入住第一天", Body: "第一段宿舍回忆", BodyHTML: `<h2>入住第一天</h2><p><strong>第一段</strong>宿舍回忆<script>alert(1)</script></p><pre><code class="language-go">fmt.Println("hi")</code></pre>`, ContentDate: "2024-09-01", Visibility: "members", Tags: []string{"开学", " 开学 "}}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Status != "draft" || draft.Title != "入住第一天" || !strings.Contains(draft.BodyHTML, "<strong>第一段</strong>") || !strings.Contains(draft.BodyHTML, `class="language-go"`) || strings.Contains(draft.BodyHTML, "script") || len(draft.Tags) != 1 {
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

	published, err := store.Submit(ctx, member, draft.ID, "127.0.0.1")
	if err != nil || published.Status != "published" {
		t.Fatalf("submit status=%q err=%v", published.Status, err)
	}
	feed, err := store.List(ctx, other, ListOptions{Scope: "feed"})
	if err != nil || len(feed.Posts) != 1 {
		t.Fatalf("feed after direct publish=%d err=%v", len(feed.Posts), err)
	}
	if _, err := store.List(ctx, member, ListOptions{Scope: "pending"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member pending list err=%v", err)
	}

	feed, err = store.List(ctx, other, ListOptions{Scope: "feed", Limit: 10})
	if err != nil || len(feed.Posts) != 1 || feed.Posts[0].ID != draft.ID {
		t.Fatalf("feed after approval=%+v err=%v", feed, err)
	}
	liked, likeCount, err := store.ToggleLike(ctx, other, draft.ID, "127.0.0.1")
	if err != nil || !liked || likeCount != 1 {
		t.Fatalf("first like liked=%v count=%d err=%v", liked, likeCount, err)
	}
	liked, likeCount, err = store.ToggleLike(ctx, other, draft.ID, "127.0.0.1")
	if err != nil || liked || likeCount != 0 {
		t.Fatalf("unlike liked=%v count=%d err=%v", liked, likeCount, err)
	}
	comment, err := store.AddComment(ctx, other, draft.ID, "以后看到这段还会笑。", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	comments, err := store.ListComments(ctx, member, draft.ID)
	if err != nil || len(comments) != 1 || comments[0].ID != comment.ID {
		t.Fatalf("comments=%+v err=%v", comments, err)
	}
	if err := store.DeleteComment(ctx, member, comment.ID, "127.0.0.1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-author comment delete err=%v", err)
	}
	if err := store.DeleteComment(ctx, admin, comment.ID, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	mediaID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `INSERT INTO media(id, owner_id, object_path, original_filename, media_type, mime_type, size_bytes, sha256, status, created_at, updated_at)
		VALUES(?, ?, ?, '留言照片.png', 'image', 'image/png', 128, ?, 'ready', ?, ?)`, mediaID, member.ID, "/test/guestbook.png", mediaID, now, now); err != nil {
		t.Fatal(err)
	}
	dormEntry, err := store.CreateGuestbookEntry(ctx, member, GuestbookInput{Body: "写给整个宿舍", MediaIDs: []string{mediaID}, ExternalVideoURL: "https://cdn.example.test/memory.mp4"}, "127.0.0.1")
	if err != nil || dormEntry.Recipient != nil || len(dormEntry.Media) != 1 || dormEntry.ExternalVideoURL != "https://cdn.example.test/memory.mp4" {
		t.Fatalf("create dorm guestbook entry=%+v err=%v", dormEntry, err)
	}
	dormPage, err := store.ListGuestbook(ctx, other, "", "visible", "", 20)
	if err != nil || len(dormPage.Entries) != 1 || dormPage.Entries[0].ID != dormEntry.ID {
		t.Fatalf("dorm guestbook page=%+v err=%v", dormPage, err)
	}
	if err := store.HideGuestbookEntry(ctx, other, dormEntry.ID, "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ordinary member hide dorm entry err=%v", err)
	}
	if err := store.HideGuestbookEntry(ctx, admin, dormEntry.ID, "127.0.0.1"); err != nil {
		t.Fatalf("admin hide dorm entry: %v", err)
	}
	personalEntry, err := store.CreateGuestbookEntry(ctx, member, GuestbookInput{RecipientID: other.ID, Body: "只写在你的留言页"}, "127.0.0.1")
	if err != nil || personalEntry.Recipient == nil || personalEntry.Recipient.ID != other.ID {
		t.Fatalf("create personal guestbook entry=%+v err=%v", personalEntry, err)
	}
	if err := store.HideGuestbookEntry(ctx, member, personalEntry.ID, "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("author hide personal entry err=%v", err)
	}
	if err := store.HideGuestbookEntry(ctx, other, personalEntry.ID, "127.0.0.1"); err != nil {
		t.Fatalf("recipient hide personal entry: %v", err)
	}
	hiddenPage, err := store.ListGuestbook(ctx, other, other.ID, "hidden", "", 20)
	if err != nil || len(hiddenPage.Entries) != 1 || hiddenPage.Entries[0].ID != personalEntry.ID {
		t.Fatalf("hidden personal guestbook page=%+v err=%v", hiddenPage, err)
	}
	if _, err := store.ListGuestbook(ctx, member, other.ID, "hidden", "", 20); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-recipient hidden guestbook list err=%v", err)
	}
	if err := store.RestoreGuestbookEntry(ctx, member, personalEntry.ID, "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-recipient restore guestbook entry err=%v", err)
	}
	if err := store.RestoreGuestbookEntry(ctx, other, personalEntry.ID, "127.0.0.1"); err != nil {
		t.Fatalf("recipient restore guestbook entry: %v", err)
	}
	restoredPage, err := store.ListGuestbook(ctx, member, other.ID, "visible", "", 20)
	if err != nil || len(restoredPage.Entries) != 1 || restoredPage.Entries[0].ID != personalEntry.ID {
		t.Fatalf("restored guestbook page=%+v err=%v", restoredPage, err)
	}
	deletableEntry, err := store.CreateGuestbookEntry(ctx, member, GuestbookInput{Body: "稍后由作者删除"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteGuestbookEntry(ctx, other, deletableEntry.ID, "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-author delete guestbook entry err=%v", err)
	}
	if err := store.DeleteGuestbookEntry(ctx, member, deletableEntry.ID, "127.0.0.1"); err != nil {
		t.Fatalf("author delete guestbook entry: %v", err)
	}
	if _, err := store.CreateGuestbookEntry(ctx, other, GuestbookInput{Body: "不能盗用附件", MediaIDs: []string{mediaID}}, "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-owner guestbook media err=%v", err)
	}
	var interactionNotifications int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications
		WHERE user_id = ? AND kind IN ('post_approved', 'post_like', 'post_comment')`, member.ID).Scan(&interactionNotifications); err != nil {
		t.Fatal(err)
	}
	if interactionNotifications != 2 {
		t.Fatalf("member interaction notifications=%d want=2", interactionNotifications)
	}
	var guestbookNotifications int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications
		WHERE user_id = ? AND kind = 'guestbook_entry'`, other.ID).Scan(&guestbookNotifications); err != nil {
		t.Fatal(err)
	}
	if guestbookNotifications != 3 {
		t.Fatalf("other guestbook notifications=%d want=3", guestbookNotifications)
	}
	if err := store.Delete(ctx, member, draft.ID, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	feed, err = store.List(ctx, other, ListOptions{Scope: "feed"})
	if err != nil || len(feed.Posts) != 0 {
		t.Fatalf("feed after delete=%+v err=%v", feed, err)
	}
}

func TestNormalizeExternalVideoEmbed(t *testing.T) {
	bilibili := `<iframe src="//player.bilibili.com/player.html?isOutside=true&amp;bvid=BV1kB8S6sEa1" allowfullscreen="true"></iframe>`
	got, err := normalizeExternalVideo(bilibili)
	if err != nil || got != "https://player.bilibili.com/player.html?isOutside=true&bvid=BV1kB8S6sEa1" {
		t.Fatalf("bilibili embed=%q err=%v", got, err)
	}
	youtube, err := normalizeExternalVideo(`<iframe src='https://www.youtube-nocookie.com/embed/abc123'></iframe>`)
	if err != nil || youtube != "https://www.youtube-nocookie.com/embed/abc123" {
		t.Fatalf("youtube embed=%q err=%v", youtube, err)
	}
	if _, err := normalizeExternalVideo(`<iframe src="https://evil.example/embed/1"></iframe>`); err == nil {
		t.Fatal("expected unsupported iframe host to fail")
	}
	if got, err := normalizeExternalVideo("https://cdn.example/video.mp4"); err != nil || got == "" {
		t.Fatalf("direct video=%q err=%v", got, err)
	}
}
