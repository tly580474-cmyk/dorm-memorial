package database

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func Backup(ctx context.Context, db *sql.DB, destination string) error {
	abs, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve backup path: %w", err)
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("backup destination already exists: %s", abs)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
		return fmt.Errorf("checkpoint database: %w", err)
	}
	quoted := strings.ReplaceAll(filepath.ToSlash(abs), "'", "''")
	if _, err := db.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); err != nil {
		return fmt.Errorf("create online backup: %w", err)
	}
	backupDB, err := sql.Open("sqlite", abs)
	if err != nil {
		return err
	}
	defer backupDB.Close()
	if err := IntegrityCheck(ctx, backupDB); err != nil {
		return fmt.Errorf("verify backup: %w", err)
	}
	return nil
}

func Restore(ctx context.Context, source, destination string) error {
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	destinationAbs, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if sourceAbs == destinationAbs {
		return fmt.Errorf("source and destination must differ")
	}
	if _, err := os.Stat(destinationAbs); err == nil {
		return fmt.Errorf("restore destination already exists: %s", destinationAbs)
	} else if !os.IsNotExist(err) {
		return err
	}
	sourceDB, err := sql.Open("sqlite", sourceAbs+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	if err := IntegrityCheck(ctx, sourceDB); err != nil {
		sourceDB.Close()
		return fmt.Errorf("verify backup before restore: %w", err)
	}
	sourceDB.Close()

	if err := os.MkdirAll(filepath.Dir(destinationAbs), 0o750); err != nil {
		return err
	}
	in, err := os.Open(sourceAbs)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destinationAbs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		out.Close()
		if !completed {
			_ = os.Remove(destinationAbs)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	restored, err := sql.Open("sqlite", destinationAbs)
	if err != nil {
		return err
	}
	defer restored.Close()
	if err := IntegrityCheck(ctx, restored); err != nil {
		return fmt.Errorf("verify restored database: %w", err)
	}
	completed = true
	return nil
}
