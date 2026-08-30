package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dorm-memorial/internal/identity"
	"dorm-memorial/internal/storage"
)

type recoveringObjects struct {
	*memoryObjects
	puts             map[string]int
	playbackFailures int
	putError         error
	onPut            func(string)
}

func (m *recoveringObjects) Put(ctx context.Context, path string, body io.Reader, size int64) error {
	m.puts[path]++
	if m.onPut != nil {
		m.onPut(path)
	}
	if strings.HasPrefix(path, "/playback/") && m.playbackFailures > 0 {
		m.playbackFailures--
		return m.putError
	}
	return m.memoryObjects.Put(ctx, path, body, size)
}

func testFastStartBytes() []byte {
	var result bytes.Buffer
	for _, atom := range []string{"ftyp", "moov", "mdat"} {
		_ = binary.Write(&result, binary.BigEndian, uint32(12))
		result.WriteString(atom)
		result.WriteString("test")
	}
	return result.Bytes()
}

// Seed the state left by a process stopping after preparation. FFmpeg is
// deliberately unavailable: any accidental re-encode will make the test fail.
func recoveryFixture(t *testing.T, useOriginal bool) (*Store, *recoveringObjects, stagedVideo, videoCheckpoint, identity.User) {
	t.Helper()
	db, user := mediaTestUser(t)
	objects := &recoveringObjects{memoryObjects: newMemoryObjects(), puts: make(map[string]int), putError: storage.ErrUnavailable}
	store := NewStore(db, objects)
	store.ffmpegPath = filepath.Join(t.TempDir(), "ffmpeg-must-not-run")
	store.verifyDelays = []time.Duration{0}
	original := []byte("archived original video")
	if useOriginal {
		original = testFastStartBytes()
	}
	stagingPath := filepath.Join(t.TempDir(), "staged.mp4")
	if err := os.WriteFile(stagingPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(original)
	video := stagedVideo{jobID: newID(), mediaID: newID(), userID: user.ID, stagingPath: stagingPath, objectPath: "/originals/test.mp4", filename: "test.mp4", mimeType: "video/mp4", sha256: hex.EncodeToString(sum[:]), createdText: nowText(time.Now().UTC()), size: int64(len(original))}
	playback := testFastStartBytes()
	if !useOriginal {
		if err := os.WriteFile(stagingPath+".playback.mp4", playback, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var preview bytes.Buffer
	if err := jpeg.Encode(&preview, image.NewRGBA(image.Rect(0, 0, 8, 6)), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagingPath+".poster.jpg", preview.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	encoder := "libx264"
	if useOriginal {
		encoder = "original"
	}
	checkpoint := videoCheckpoint{Prepared: true, Preparation: videoPreparation{UseOriginal: useOriginal, Encoder: encoder, DurationMS: 1234, Width: 320, Height: 180}, PlaybackSize: int64(len(playback)), PreviewSize: int64(preview.Len())}
	encoded, _ := json.Marshal(checkpoint)
	if err := store.reserve(context.Background(), user.ID, video.jobID, "checkpoint-"+video.jobID, video.objectPath, video.size); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO media(id, owner_id, object_path, original_filename, media_type, mime_type, size_bytes, sha256, status, created_at, updated_at) VALUES(?, ?, ?, ?, 'video', 'video/mp4', ?, ?, 'uploading', ?, ?)`, video.mediaID, video.userID, video.objectPath, video.filename, video.size, video.sha256, video.createdText, video.createdText); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO media_processing_jobs(id, media_id, user_id, staging_path, phase, checkpoint_json, created_at, updated_at) VALUES(?, ?, ?, ?, 'uploading', ?, ?, ?)`, video.jobID, video.mediaID, video.userID, video.stagingPath, string(encoded), video.createdText, video.createdText); err != nil {
		t.Fatal(err)
	}
	return store, objects, video, checkpoint, user
}

func assertProcessingComplete(t *testing.T, store *Store, video stagedVideo, user identity.User) {
	t.Helper()
	status, err := store.ProcessingStatus(context.Background(), user, video.jobID)
	if err != nil || status.Phase != "completed" || status.Media == nil || status.Media.Status != "ready" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	for _, suffix := range []string{"", ".playback.mp4", ".poster.jpg"} {
		if _, err := os.Stat(video.stagingPath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("staged artifact %q was not cleaned: %v", suffix, err)
		}
	}
}

func TestStagedVideoRetriesOnlyFailedUpload(t *testing.T) {
	store, objects, video, _, user := recoveryFixture(t, false)
	objects.playbackFailures = 1
	objects.onPut = func(path string) {
		if !strings.HasPrefix(path, "/playback/") {
			return
		}
		if _, err := os.Stat(video.stagingPath + ".playback.mp4"); err != nil {
			t.Fatalf("prepared output removed during retry: %v", err)
		}
		if _, err := objects.Stat(context.Background(), video.objectPath); err != nil {
			t.Fatalf("saved original removed during playback retry: %v", err)
		}
	}
	store.processStagedVideo(context.Background(), video.jobID)
	assertProcessingComplete(t, store, video, user)
	if objects.puts[video.objectPath] != 1 {
		t.Fatalf("original writes=%d", objects.puts[video.objectPath])
	}
	for path, count := range objects.puts {
		if strings.HasPrefix(path, "/playback/") && count != 2 {
			t.Fatalf("playback attempts=%d, want 2", count)
		}
	}
}

func TestStagedVideoResumesVerifiedRemoteCheckpoint(t *testing.T) {
	store, objects, video, checkpoint, user := recoveryFixture(t, false)
	original, _ := os.ReadFile(video.stagingPath)
	objects.objects[video.objectPath] = original
	checkpoint.OriginalUploaded = true
	if err := store.saveVideoCheckpoint(context.Background(), video.jobID, checkpoint); err != nil {
		t.Fatal(err)
	}
	store.processStagedVideo(context.Background(), video.jobID)
	assertProcessingComplete(t, store, video, user)
	if objects.puts[video.objectPath] != 0 {
		t.Fatalf("verified original was uploaded again: %d", objects.puts[video.objectPath])
	}
}

func TestStagedVideoRepairsStaleRemoteCheckpoint(t *testing.T) {
	store, objects, video, checkpoint, user := recoveryFixture(t, false)
	objects.objects[video.objectPath] = []byte("truncated")
	checkpoint.OriginalUploaded = true
	if err := store.saveVideoCheckpoint(context.Background(), video.jobID, checkpoint); err != nil {
		t.Fatal(err)
	}
	store.processStagedVideo(context.Background(), video.jobID)
	assertProcessingComplete(t, store, video, user)
	if objects.puts[video.objectPath] != 1 {
		t.Fatal("mismatched remote original was trusted instead of replaced")
	}
}

func TestStagedVideoReusesOriginalAndPersistsProbedMetadata(t *testing.T) {
	store, objects, video, checkpoint, user := recoveryFixture(t, true)
	store.processStagedVideo(context.Background(), video.jobID)
	assertProcessingComplete(t, store, video, user)
	var playbackPath, mimeType string
	var size, duration int64
	var width, height int
	if err := store.db.QueryRow(`SELECT v.object_path, v.mime_type, v.size_bytes, m.duration_ms, m.width, m.height FROM media_variants v JOIN media m ON m.id = v.media_id WHERE m.id = ?`, video.mediaID).Scan(&playbackPath, &mimeType, &size, &duration, &width, &height); err != nil {
		t.Fatal(err)
	}
	if playbackPath != video.objectPath || mimeType != "video/mp4" || size != video.size || duration != checkpoint.Preparation.DurationMS || width != 320 || height != 180 {
		t.Fatalf("playback=%q %q size=%d metadata=%dx%d/%d", playbackPath, mimeType, size, width, height, duration)
	}
	if len(objects.objects) != 2 {
		t.Fatalf("remote objects=%d, want original and preview only", len(objects.objects))
	}
}

func TestStagedVideoRejectsStalePreparedFile(t *testing.T) {
	store, objects, video, _, user := recoveryFixture(t, false)
	if err := os.WriteFile(video.stagingPath+".playback.mp4", []byte("incomplete"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.processStagedVideo(context.Background(), video.jobID)
	status, err := store.ProcessingStatus(context.Background(), user, video.jobID)
	if err != nil || status.Phase != "failed" || status.ErrorCode != "transcode_failed" || len(objects.puts) != 0 {
		t.Fatalf("stale preparation was trusted: status=%+v writes=%v err=%v", status, objects.puts, err)
	}
}

func TestCompleteStagedVideoBeginFailureDoesNotPanic(t *testing.T) {
	store, _, video, checkpoint, _ := recoveryFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.completeStagedVideo(ctx, video, checkpoint, video.objectPath, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("finalize canceled transaction err=%v", err)
	}
}

func TestStagedVideoTransactionFailureRollsBackBeforeFailureStatus(t *testing.T) {
	store, objects, video, _, user := recoveryFixture(t, false)
	if _, err := store.db.Exec(`CREATE TRIGGER reject_playback_variant BEFORE INSERT ON media_variants BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		store.processStagedVideo(ctx, video.jobID)
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("transaction failure did not release SQLite connection before status update")
	}
	status, err := store.ProcessingStatus(context.Background(), user, video.jobID)
	if err != nil || status.Phase != "failed" || status.ErrorCode != "database_failed" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	var mediaStatus string
	if err := store.db.QueryRow(`SELECT status FROM media WHERE id = ?`, video.mediaID).Scan(&mediaStatus); err != nil || mediaStatus != "unavailable" {
		t.Fatalf("media status=%q err=%v", mediaStatus, err)
	}
	if len(objects.objects) != 0 {
		t.Fatalf("terminal failure left %d remote objects", len(objects.objects))
	}
}

func TestStagedVideoDoesNotResurrectMediaDeletedDuringUpload(t *testing.T) {
	for _, useOriginal := range []bool{false, true} {
		name := "prepared_playback"
		if useOriginal {
			name = "original_alias"
		}
		t.Run(name, func(t *testing.T) {
			store, objects, video, _, user := recoveryFixture(t, useOriginal)
			objects.onPut = func(path string) {
				if path != video.objectPath {
					return
				}
				if _, err := store.db.Exec(`UPDATE media SET status = 'deleted' WHERE id = ?`, video.mediaID); err != nil {
					t.Fatal(err)
				}
			}
			store.processStagedVideo(context.Background(), video.jobID)
			var status string
			if err := store.db.QueryRow(`SELECT status FROM media WHERE id = ?`, video.mediaID).Scan(&status); err != nil || status != "deleted" {
				t.Fatalf("deleted media resurrected or overwritten: status=%q err=%v", status, err)
			}
			job, err := store.ProcessingStatus(context.Background(), user, video.jobID)
			if err != nil || job.Phase != "failed" {
				t.Fatalf("processing did not fail: job=%+v err=%v", job, err)
			}
			var variants int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM media_variants WHERE media_id = ?`, video.mediaID).Scan(&variants); err != nil || variants != 0 {
				t.Fatalf("deleted media has %d variants err=%v", variants, err)
			}
			if len(objects.objects) != 0 {
				t.Fatalf("deleted media left %d remote objects", len(objects.objects))
			}
		})
	}
}

func TestStagedVideoDoesNotRecreateMediaPurgedDuringUpload(t *testing.T) {
	store, objects, video, _, _ := recoveryFixture(t, true)
	objects.onPut = func(path string) {
		if path != video.objectPath {
			return
		}
		if _, err := store.db.Exec(`DELETE FROM media WHERE id = ?`, video.mediaID); err != nil {
			t.Fatal(err)
		}
	}
	store.processStagedVideo(context.Background(), video.jobID)
	var mediaCount, variantCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM media WHERE id = ?`, video.mediaID).Scan(&mediaCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM media_variants WHERE media_id = ?`, video.mediaID).Scan(&variantCount); err != nil {
		t.Fatal(err)
	}
	if mediaCount != 0 || variantCount != 0 || len(objects.objects) != 0 {
		t.Fatalf("purge left media=%d variants=%d objects=%d", mediaCount, variantCount, len(objects.objects))
	}
}

func TestProcessingDedupIgnoresUnavailableAndDeletedMedia(t *testing.T) {
	for _, status := range []string{"unavailable", "deleted"} {
		t.Run(status, func(t *testing.T) {
			store, _, video, _, _ := recoveryFixture(t, false)
			if _, err := store.db.Exec(`UPDATE media SET status = ? WHERE id = ?`, status, video.mediaID); err != nil {
				t.Fatal(err)
			}
			if _, found, err := store.processingByHash(context.Background(), video.userID, video.sha256); err != nil || found {
				t.Fatalf("hash dedup reused %s media: found=%v err=%v", status, found, err)
			}
			if _, found, err := store.processingByRequest(context.Background(), video.userID, "checkpoint-"+video.jobID); err != nil || found {
				t.Fatalf("request dedup reused %s media: found=%v err=%v", status, found, err)
			}
		})
	}
}

func TestVideoStorageRetryStopsForPermanentErrorAndCancellation(t *testing.T) {
	for _, failure := range []error{storage.ErrUnauthorized, storage.ErrInvalidPath, sql.ErrNoRows} {
		attempts := 0
		err := retryVideoStorage(context.Background(), func() error { attempts++; return failure })
		if !errors.Is(err, failure) || attempts != 1 {
			t.Fatalf("permanent failure=%v err=%v attempts=%d", failure, err, attempts)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := retryVideoStorage(ctx, func() error { attempts++; cancel(); return storage.ErrUnavailable })
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("canceled retry err=%v attempts=%d", err, attempts)
	}
}
