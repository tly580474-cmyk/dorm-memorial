package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMigratesAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := IntegrityCheck(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil || count != 10 {
		t.Fatalf("migrations count=%d err=%v", count, err)
	}
}

func TestMessageMediaMigrationPreservesExistingAttachments(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"0001_platform.sql", "0002_memorial_content.sql", "0003_guestbook_media.sql", "0004_messages_notifications.sql"} {
		script, err := migrationFS.ReadFile("migrations/" + version)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(script)); err != nil {
			t.Fatalf("apply %s: %v", version, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	statements := []string{
		`INSERT INTO users(id, username, email, password_hash, role, status, created_at, updated_at) VALUES('u1', 'user', 'user@example.test', 'hash', 'member', 'active', '` + now + `', '` + now + `')`,
		`INSERT INTO posts(id, author_id, body, status, visibility, created_at, updated_at) VALUES('p1', 'u1', 'memory', 'published', 'members', '` + now + `', '` + now + `')`,
		`INSERT INTO guestbook_entries(id, author_id, body, status, created_at, updated_at) VALUES('g1', 'u1', 'hello', 'visible', '` + now + `', '` + now + `')`,
		`INSERT INTO media(id, owner_id, object_path, original_filename, media_type, mime_type, size_bytes, sha256, status, created_at, updated_at) VALUES('m1', 'u1', '/m1', 'photo.png', 'image', 'image/png', 4, 'hash', 'ready', '` + now + `', '` + now + `')`,
		`INSERT INTO post_media(post_id, media_id, position) VALUES('p1', 'm1', 0)`,
		`INSERT INTO guestbook_media(entry_id, media_id, position) VALUES('g1', 'm1', 0)`,
		`INSERT INTO conversations(id, type, title, created_by, created_at, updated_at) VALUES('c1', 'group', '宿舍群聊', 'u1', '` + now + `', '` + now + `')`,
		`INSERT INTO conversation_members(conversation_id, user_id, joined_at, last_read_at) VALUES('c1', 'u1', '` + now + `', '` + now + `')`,
		`INSERT INTO messages(id, conversation_id, sender_id, body, status, created_at) VALUES('11111111111111111111111111111111', 'c1', 'u1', '旧消息', 'sent', '` + now + `')`,
		`INSERT INTO notifications(id, user_id, actor_id, kind, target_type, target_id, title, event_key, created_at) VALUES('n1', 'u1', 'u1', 'group_message', 'conversation', 'c1', '旧通知', 'message:11111111111111111111111111111111:u1', '` + now + `')`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{
		"post attachment":      `SELECT COUNT(*) FROM post_media WHERE post_id = 'p1' AND media_id = 'm1'`,
		"guestbook attachment": `SELECT COUNT(*) FROM guestbook_media WHERE entry_id = 'g1' AND media_id = 'm1'`,
	} {
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", name, count, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO media(id, owner_id, object_path, original_filename, media_type, mime_type, size_bytes, sha256, status, created_at, updated_at) VALUES('a1', 'u1', '/a1', 'voice.m4a', 'audio', 'audio/mp4', 4, 'hash2', 'ready', ?, ?)`, now, now); err != nil {
		t.Fatalf("audio media rejected after migration: %v", err)
	}
	var targetType, targetID string
	if err := db.QueryRowContext(ctx, `SELECT target_type, target_id FROM notifications WHERE id = 'n1'`).Scan(&targetType, &targetID); err != nil || targetType != "message" || targetID != "11111111111111111111111111111111" {
		t.Fatalf("notification target=%s/%s err=%v", targetType, targetID, err)
	}
	if err := IntegrityCheck(ctx, db); err != nil {
		t.Fatal(err)
	}
}
