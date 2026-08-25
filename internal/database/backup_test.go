package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(ctx, filepath.Join(dir, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id, username, email, password_hash, role, status, created_at, updated_at)
		VALUES('u1', 'member1', 'member@example.com', 'hash', 'member', 'active', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backup.db")
	if err := Backup(ctx, db, backup); err != nil {
		t.Fatal(err)
	}
	db.Close()
	restoredPath := filepath.Join(dir, "restored.db")
	if err := Restore(ctx, backup, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var count int
	if err := restored.QueryRow("SELECT COUNT(*) FROM users WHERE id = 'u1'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("restored count=%d err=%v", count, err)
	}
}
