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
