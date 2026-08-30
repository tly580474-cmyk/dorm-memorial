package identity

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dorm-memorial/internal/database"
)

func TestLastActiveAdminCannotSelfDeactivate(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "last-admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewStore(db)
	if _, err := store.BootstrapAdmin(ctx, "admin", "admin@example.test", "correct-horse-battery", "管理员"); err != nil {
		t.Fatal(err)
	}
	admin, err := store.Authenticate(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	token, _, _, err := store.CreateSession(ctx, admin.ID, "test", "127.0.0.1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SelfDeactivate(ctx, admin, "correct-horse-battery", "127.0.0.1"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("last admin deactivation=%v", err)
	}
	if _, _, err := store.UserForToken(ctx, token); err != nil {
		t.Fatalf("rejected deactivation changed session: %v", err)
	}
	code, _, err := store.CreateInvite(ctx, admin, 1, time.Hour, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.Register(ctx, RegisterInput{InviteCode: code, Username: "successor", Email: "successor@example.test", Password: "member-password", Nickname: "继任管理员"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAdminUser(ctx, admin, member.ID, AdminUserUpdate{Role: "admin", Status: "active"}, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SelfDeactivate(ctx, admin, "correct-horse-battery", "127.0.0.1"); err != nil {
		t.Fatalf("deactivation after handover failed: %v", err)
	}
	if _, _, err := store.UserForToken(ctx, token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("deactivated admin session remains valid: %v", err)
	}
}

func TestPasswordValidationUsesBcryptByteLimit(t *testing.T) {
	for _, password := range []string{strings.Repeat("a", 73), strings.Repeat("密", 25)} {
		if err := validateAccount("member", "member@example.test", password, "室友"); err == nil {
			t.Fatal("accepted password exceeding bcrypt byte limit")
		}
	}
	if err := validateAccount("member", "member@example.test", strings.Repeat("密", 24), "室友"); err != nil {
		t.Fatalf("72-byte password rejected: %v", err)
	}
}
