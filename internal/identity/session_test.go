package identity

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dorm-memorial/internal/database"
)

func TestSessionReadsThrottleActivityAndStillEnforceRevocation(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
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
	token, id, _, err := store.CreateSession(ctx, user.ID, "test", "127.0.0.1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	seen := func() string {
		t.Helper()
		var value string
		if err := db.QueryRowContext(ctx, "SELECT last_seen_at FROM sessions WHERE id = ?", id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	initial := seen()
	for range 3 {
		got, sessionID, err := store.UserForToken(ctx, token)
		if err != nil || got.ID != user.ID || sessionID != id {
			t.Fatalf("session lookup user=%v id=%s err=%v", got, sessionID, err)
		}
	}
	if seen() != initial {
		t.Fatal("frequent requests rewrote the activity timestamp")
	}
	old := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ? WHERE id = ?", old, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UserForToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	if seen() == old {
		t.Fatal("stale activity was not refreshed")
	}
	if err := store.RevokeSession(ctx, user.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UserForToken(ctx, token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked session accepted: %v", err)
	}
}

func TestSessionDatabaseFailureIsNotInvalidCredentials(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UserForToken(context.Background(), "token"); err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("database failure lost its identity: %v", err)
	}
}
