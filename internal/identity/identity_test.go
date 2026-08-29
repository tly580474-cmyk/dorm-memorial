package identity

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dorm-memorial/internal/database"
)

func TestAdminUserManagementProtectsCurrentAndLastAdmin(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewStore(db)
	if _, err := store.BootstrapAdmin(ctx, "admin", "admin@example.test", "correct-horse-battery", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := store.Authenticate(ctx, "admin", "correct-horse-battery")
	code, _, _ := store.CreateInvite(ctx, admin, 1, time.Hour, "127.0.0.1")
	member, err := store.Register(ctx, RegisterInput{InviteCode: code, Username: "roommate", Email: "roommate@example.test", Password: "member-password", Nickname: "室友"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.UpdateAdminUser(ctx, admin, admin.ID, AdminUserUpdate{Role: "member", Status: "active"}, "127.0.0.1"); err == nil {
		t.Fatal("current administrator demotion should be rejected")
	}
	updated, err := store.UpdateAdminUser(ctx, admin, member.ID, AdminUserUpdate{Role: "admin", Status: "active"}, "127.0.0.1")
	if err != nil || updated.Role != "admin" {
		t.Fatalf("promote=%+v err=%v", updated, err)
	}
	if _, err := store.UpdateAdminUser(ctx, admin, member.ID, AdminUserUpdate{Role: "admin", Status: "disabled"}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, "roommate", "member-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled authenticate err=%v", err)
	}
	items, err := store.ListAdminUsers(ctx, admin, "room", "", "disabled")
	if err != nil || len(items) != 1 || items[0].ID != member.ID {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestUserCanUpdateOwnAccountAndRevokeOtherSessions(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "account.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewStore(db)
	if _, err := store.BootstrapAdmin(ctx, "admin", "admin@example.test", "correct-horse-battery", "管理员"); err != nil {
		t.Fatal(err)
	}
	user, err := store.Authenticate(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	_, currentSessionID, _, err := store.CreateSession(ctx, user.ID, "current", "127.0.0.1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = store.CreateSession(ctx, user.ID, "other", "127.0.0.1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	nicknameOnly, err := store.UpdateAccount(ctx, user.ID, currentSessionID, AccountInput{Username: "admin", Email: "admin@example.test", Nickname: "免密新昵称"}, "127.0.0.1")
	if err != nil || nicknameOnly.Nickname != "免密新昵称" || nicknameOnly.Username != "admin" || nicknameOnly.Email != "admin@example.test" {
		t.Fatalf("nickname-only update=%+v err=%v", nicknameOnly, err)
	}
	if _, err := store.UpdateAccount(ctx, user.ID, currentSessionID, AccountInput{Username: "renamed-admin", Email: "renamed@example.test", Nickname: "新昵称", CurrentPassword: "wrong-password"}, "127.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password err=%v", err)
	}
	updated, err := store.UpdateAccount(ctx, user.ID, currentSessionID, AccountInput{Username: "renamed-admin", Email: "renamed@example.test", Nickname: "新昵称", CurrentPassword: "correct-horse-battery", NewPassword: "new-correct-password"}, "127.0.0.1")
	if err != nil || updated.Username != "renamed-admin" || updated.Email != "renamed@example.test" || updated.Nickname != "新昵称" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	if _, err := store.Authenticate(ctx, "admin", "correct-horse-battery"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old credentials err=%v", err)
	}
	if _, err := store.Authenticate(ctx, "renamed-admin", "new-correct-password"); err != nil {
		t.Fatalf("new credentials: %v", err)
	}
	sessions, err := store.ListSessions(ctx, user.ID, currentSessionID)
	if err != nil || len(sessions) != 1 || !sessions[0].Current {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
}

func TestSelfDeactivateKeepsContentButBlocksLoginAndSessions(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "deactivate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewStore(db)
	if _, err := store.BootstrapAdmin(ctx, "admin", "admin@example.test", "correct-horse-battery", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := store.Authenticate(ctx, "admin", "correct-horse-battery")
	code, _, _ := store.CreateInvite(ctx, admin, 1, time.Hour, "127.0.0.1")
	member, err := store.Register(ctx, RegisterInput{InviteCode: code, Username: "leaver", Email: "leaver@example.test", Password: "member-password", Nickname: "离舍"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	token, _, _, err := store.CreateSession(ctx, member.ID, "browser", "127.0.0.1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UserForToken(ctx, token); err != nil {
		t.Fatalf("active session err=%v", err)
	}

	// 错误密码必须被拒绝。
	if err := store.SelfDeactivate(ctx, member, "wrong-password", "127.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password err=%v", err)
	}
	if err := store.SelfDeactivate(ctx, member, "member-password", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	// 全部会话立即失效，账号不可再登录。
	if _, _, err := store.UserForToken(ctx, token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("deactivated token err=%v", err)
	}
	if _, err := store.Authenticate(ctx, "leaver", "member-password"); !errors.Is(err, ErrAccountDeactivated) {
		t.Fatalf("deactivated login err=%v", err)
	}

	// 历史内容不删除：注销账号仍出现在成员列表中并带标记。
	members, err := store.ListMembers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range members {
		if item.ID == member.ID {
			found = item.Deactivated
		}
	}
	if !found {
		t.Fatalf("deactivated member not listed or not flagged: %+v", members)
	}
	// 注销账号仍可在管理后台恢复。
	restored, err := store.UpdateAdminUser(ctx, admin, member.ID, AdminUserUpdate{Role: "member", Status: "active"}, "127.0.0.1")
	if err != nil || restored.Status != "active" {
		t.Fatalf("restore=%+v err=%v", restored, err)
	}
	if _, err := store.Authenticate(ctx, "leaver", "member-password"); err != nil {
		t.Fatalf("restored login err=%v", err)
	}
}

func TestListMembersIncludesDeactivatedFlag(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "members.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewStore(db)
	if _, err := store.BootstrapAdmin(ctx, "admin", "admin@example.test", "correct-horse-battery", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, _ := store.Authenticate(ctx, "admin", "correct-horse-battery")
	code, _, _ := store.CreateInvite(ctx, admin, 1, time.Hour, "127.0.0.1")
	member, err := store.Register(ctx, RegisterInput{InviteCode: code, Username: "ghost", Email: "ghost@example.test", Password: "member-password", Nickname: "幽灵"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SelfDeactivate(ctx, member, "member-password", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAdminUser(ctx, admin, member.ID, AdminUserUpdate{Role: "member", Status: "active"}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	members, err := store.ListMembers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range members {
		if item.Deactivated {
			t.Fatalf("restored member still flagged: %+v", item)
		}
	}
}
