package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"dorm-memorial/internal/config"
	"dorm-memorial/internal/database"
	"dorm-memorial/internal/media"
	"dorm-memorial/internal/storage"
	"dorm-memorial/internal/storage/alist"
	storagecache "dorm-memorial/internal/storage/cache"
)

type videoRow struct {
	id         string
	objectPath string
}

func main() {
	apply := flag.Bool("apply", false, "replace non-fast-start MP4 objects after validation")
	onlyID := flag.String("id", "", "migrate only one media id")
	flag.Parse()
	if !*apply {
		fmt.Fprintln(os.Stderr, "refusing to modify media without -apply")
		os.Exit(2)
	}
	if err := run(strings.TrimSpace(*onlyID)); err != nil {
		fmt.Fprintln(os.Stderr, "media fast-start migration failed:", err)
		os.Exit(1)
	}
}

func run(onlyID string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()
	objects, err := openStorage(ctx, cfg)
	if err != nil {
		return err
	}
	rows, err := selectVideos(ctx, db, onlyID)
	if err != nil {
		return err
	}
	migrated, skipped := 0, 0
	for _, row := range rows {
		changed, err := migrateOne(ctx, db, objects, cfg.FFmpegPath, row)
		if err != nil {
			return fmt.Errorf("media %s: %w", row.id, err)
		}
		if changed {
			migrated++
			fmt.Printf("migrated media=%s\n", row.id)
		} else {
			skipped++
			fmt.Printf("already_fast_start media=%s\n", row.id)
		}
	}
	fmt.Printf("migration_complete migrated=%d skipped=%d total=%d\n", migrated, skipped, len(rows))
	return nil
}

func openStorage(ctx context.Context, cfg config.Config) (storage.ObjectStorage, error) {
	if cfg.AListBaseURL == "" || (cfg.AListToken == "" && (cfg.AListUsername == "" || cfg.AListPassword == "")) {
		return nil, errors.New("AList credentials are not configured")
	}
	client, err := alist.New(alist.Config{BaseURL: cfg.AListBaseURL, Token: cfg.AListToken, Username: cfg.AListUsername, Password: cfg.AListPassword, Root: cfg.AListRoot})
	if err != nil {
		return nil, err
	}
	if cfg.AListUsername != "" && cfg.AListPassword != "" {
		authCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err = client.Authenticate(authCtx)
		cancel()
		if err != nil {
			return nil, err
		}
	}
	var objects storage.ObjectStorage = client
	if cfg.MediaCacheMaxBytes > 0 {
		objects, err = storagecache.New(objects, cfg.MediaCacheDir, cfg.MediaCacheMaxBytes)
		if err != nil {
			return nil, err
		}
	}
	return objects, nil
}

func selectVideos(ctx context.Context, db *sql.DB, onlyID string) ([]videoRow, error) {
	query := `SELECT id, object_path FROM media WHERE media_type = 'video' AND status = 'ready' AND lower(object_path) LIKE '%.mp4'`
	args := []any{}
	if onlyID != "" {
		query += " AND id = ?"
		args = append(args, onlyID)
	}
	query += " ORDER BY created_at"
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]videoRow, 0)
	for rows.Next() {
		var row videoRow
		if err := rows.Scan(&row.id, &row.objectPath); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func migrateOne(ctx context.Context, db *sql.DB, objects storage.ObjectStorage, ffmpegPath string, row videoRow) (bool, error) {
	input, err := os.CreateTemp("", "dorm-media-source-*.mp4")
	if err != nil {
		return false, err
	}
	inputPath := input.Name()
	outputPath := inputPath + ".faststart.mp4"
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)
	body, err := objects.Open(ctx, row.objectPath)
	if err != nil {
		input.Close()
		return false, err
	}
	_, copyErr := io.Copy(input, body)
	closeBodyErr := body.Close()
	closeInputErr := input.Close()
	if copyErr != nil || closeBodyErr != nil || closeInputErr != nil {
		return false, errors.Join(copyErr, closeBodyErr, closeInputErr)
	}
	fastStart, err := media.MP4FastStart(inputPath)
	if err != nil {
		return false, err
	}
	if fastStart {
		return false, nil
	}
	if err := media.RemuxMP4FastStart(ctx, ffmpegPath, inputPath, outputPath); err != nil {
		return false, err
	}
	prepared, err := os.Open(outputPath)
	if err != nil {
		return false, err
	}
	info, err := prepared.Stat()
	if err != nil {
		prepared.Close()
		return false, err
	}
	hash, err := fileSHA256(prepared)
	if err != nil {
		prepared.Close()
		return false, err
	}
	if _, err := prepared.Seek(0, io.SeekStart); err != nil {
		prepared.Close()
		return false, err
	}

	ext := path.Ext(row.objectPath)
	backupPath := strings.TrimSuffix(row.objectPath, ext) + ".pre-faststart-" + row.id[:8] + ext
	_ = objects.Delete(ctx, backupPath)
	if err := objects.Move(ctx, row.objectPath, backupPath); err != nil {
		prepared.Close()
		return false, fmt.Errorf("preserve original: %w", err)
	}
	restore := func(cause error) error {
		_ = objects.Delete(context.WithoutCancel(ctx), row.objectPath)
		if restoreErr := objects.Move(context.WithoutCancel(ctx), backupPath, row.objectPath); restoreErr != nil {
			return errors.Join(cause, fmt.Errorf("restore original: %w", restoreErr))
		}
		return cause
	}
	if err := objects.Put(ctx, row.objectPath, prepared, info.Size()); err != nil {
		prepared.Close()
		return false, restore(fmt.Errorf("upload optimized object: %w", err))
	}
	if err := prepared.Close(); err != nil {
		return false, restore(err)
	}
	if err := verifyObjectSize(ctx, objects, row.objectPath, info.Size()); err != nil {
		return false, restore(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := db.ExecContext(ctx, `UPDATE media SET size_bytes = ?, sha256 = ?, updated_at = ? WHERE id = ? AND object_path = ? AND status = 'ready'`, info.Size(), hash, now, row.id, row.objectPath)
	if err != nil {
		return false, restore(fmt.Errorf("update media metadata: %w", err))
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return false, restore(errors.New("media row changed during migration"))
	}
	if err := objects.Delete(context.WithoutCancel(ctx), backupPath); err != nil && !errors.Is(err, storage.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "warning: media=%s backup_cleanup=%v path=%s\n", row.id, err, backupPath)
	}
	return true, nil
}

func fileSHA256(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func verifyObjectSize(ctx context.Context, objects storage.ObjectStorage, objectPath string, expected int64) error {
	var lastErr error
	for _, delay := range []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second} {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		if refresher, ok := objects.(storage.DirectoryRefresher); ok {
			_ = refresher.RefreshDirectory(ctx, path.Dir(objectPath))
		}
		info, err := objects.Stat(ctx, objectPath)
		if err == nil && info.Size == expected {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("size=%d want=%d", info.Size, expected)
		}
	}
	return fmt.Errorf("verify optimized object: %w", lastErr)
}
