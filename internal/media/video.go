package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"dorm-memorial/internal/storage"
)

const maxVideoPreviewBytes = 8 << 20

func buildVideoPreview(ctx context.Context, objects storage.ObjectStorage, ffmpegPath, objectPath, ownerID, mediaID string, createdAt time.Time, durationMS int64) string {
	body, err := objects.Open(ctx, objectPath)
	if err != nil {
		return ""
	}
	defer body.Close()

	input, err := os.CreateTemp("", "dorm-video-*")
	if err != nil {
		return ""
	}
	inputPath := input.Name()
	defer os.Remove(inputPath)
	defer input.Close()
	if _, err = io.Copy(input, body); err != nil || input.Close() != nil {
		return ""
	}

	output, err := os.CreateTemp("", "dorm-video-preview-*.jpg")
	if err != nil {
		return ""
	}
	outputPath := output.Name()
	output.Close()
	defer os.Remove(outputPath)

	processCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	seek := videoPreviewSeek(mediaID, durationMS)
	if err := extractVideoFrame(processCtx, ffmpegPath, inputPath, outputPath, seek); err != nil && seek != 0 {
		if err = extractVideoFrame(processCtx, ffmpegPath, inputPath, outputPath, 0); err != nil {
			return ""
		}
	}
	encoded, err := os.ReadFile(outputPath)
	if err != nil || len(encoded) == 0 || len(encoded) > maxVideoPreviewBytes {
		return ""
	}
	previewPath := "/previews/" + remoteOwnerSegment(ownerID) + "/" + createdAt.UTC().Format("2006/01") + "/" + mediaID + ".jpg"
	if err := objects.Put(ctx, previewPath, bytes.NewReader(encoded), int64(len(encoded))); err != nil {
		return ""
	}
	return previewPath
}

func videoPreviewSeek(mediaID string, durationMS int64) float64 {
	if durationMS <= 0 {
		return 1
	}
	hash := sha256.Sum256([]byte(mediaID))
	fraction := 0.15 + (float64(hash[0])/255)*0.60
	seek := float64(durationMS) / 1000 * fraction
	if durationMS > 1500 && seek > float64(durationMS)/1000-0.5 {
		seek = float64(durationMS)/1000 - 0.5
	}
	if seek < 0 {
		return 0
	}
	return seek
}

func extractVideoFrame(ctx context.Context, ffmpegPath, inputPath, outputPath string, seek float64) error {
	args := []string{"-hide_banner", "-loglevel", "error", "-ss", strconv.FormatFloat(seek, 'f', 3, 64), "-i", inputPath, "-frames:v", "1", "-vf", "scale=960:-2:force_original_aspect_ratio=decrease", "-q:v", "3", "-y", outputPath}
	if output, err := exec.CommandContext(ctx, ffmpegPath, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("extract video frame: %w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}
