package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"dorm-memorial/internal/storage"
)

const maxVideoPreviewBytes = 8 << 20

func prepareMP4Upload(ctx context.Context, ffmpegPath string, input UploadInput) (UploadInput, func(), error) {
	inputFile, err := os.CreateTemp("", "dorm-video-upload-*.mp4")
	if err != nil {
		return UploadInput{}, func() {}, fmt.Errorf("create video staging file: %w", err)
	}
	inputPath := inputFile.Name()
	outputPath := inputPath + ".faststart.mp4"
	cleanup := func() {
		_ = inputFile.Close()
		_ = os.Remove(inputPath)
		_ = os.Remove(outputPath)
	}
	written, copyErr := io.Copy(inputFile, input.Body)
	closeErr := inputFile.Close()
	if copyErr != nil || closeErr != nil || written != input.Size {
		cleanup()
		return UploadInput{}, func() {}, fmt.Errorf("stage video upload: %w", ErrInvalid)
	}

	preparedPath := inputPath
	fastStart, inspectErr := MP4FastStart(inputPath)
	if inspectErr == nil && !fastStart {
		if err := RemuxMP4FastStart(ctx, ffmpegPath, inputPath, outputPath); err != nil {
			cleanup()
			return UploadInput{}, func() {}, fmt.Errorf("prepare MP4 fast start: %w", errors.Join(ErrInvalid, err))
		}
		preparedPath = outputPath
	}

	prepared, err := os.Open(preparedPath)
	if err != nil {
		cleanup()
		return UploadInput{}, func() {}, fmt.Errorf("open prepared video: %w", err)
	}
	info, err := prepared.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > MaxFileSize {
		prepared.Close()
		cleanup()
		return UploadInput{}, func() {}, fmt.Errorf("prepared video size: %w", ErrInvalid)
	}
	input.Body = prepared
	input.Size = info.Size()
	return input, func() {
		_ = prepared.Close()
		cleanup()
	}, nil
}

// MP4FastStart reports whether the movie metadata atom precedes media data.
// Browsers can begin playback immediately only when moov appears before mdat.
func MP4FastStart(filename string) (bool, error) {
	moov, mdat, err := mp4AtomOffsets(filename)
	if err != nil {
		return false, err
	}
	if moov < 0 || mdat < 0 {
		return false, fmt.Errorf("required moov/mdat atoms not found")
	}
	return moov < mdat, nil
}

func mp4AtomOffsets(filename string) (moov, mdat int64, err error) {
	file, err := os.Open(filename)
	if err != nil {
		return -1, -1, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return -1, -1, err
	}
	moov, mdat = -1, -1
	for offset := int64(0); offset+8 <= info.Size(); {
		header := make([]byte, 16)
		if _, err := file.ReadAt(header[:8], offset); err != nil {
			return -1, -1, err
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		atomType := string(header[4:8])
		headerSize := int64(8)
		switch size {
		case 0:
			size = info.Size() - offset
		case 1:
			if _, err := file.ReadAt(header[8:16], offset+8); err != nil {
				return -1, -1, err
			}
			if binary.BigEndian.Uint64(header[8:16]) > uint64(^uint64(0)>>1) {
				return -1, -1, fmt.Errorf("MP4 atom is too large")
			}
			size = int64(binary.BigEndian.Uint64(header[8:16]))
			headerSize = 16
		}
		if size < headerSize || offset+size > info.Size() {
			return -1, -1, fmt.Errorf("invalid MP4 atom %q at %d", atomType, offset)
		}
		if atomType == "moov" && moov < 0 {
			moov = offset
		}
		if atomType == "mdat" && mdat < 0 {
			mdat = offset
		}
		offset += size
	}
	return moov, mdat, nil
}

// RemuxMP4FastStart moves metadata to the front without re-encoding streams.
func RemuxMP4FastStart(ctx context.Context, ffmpegPath, inputPath, outputPath string) error {
	processCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-i", inputPath, "-map", "0", "-c", "copy", "-movflags", "+faststart", "-y", outputPath}
	if output, err := exec.CommandContext(processCtx, ffmpegPath, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("remux MP4: %w: %s", err, bytes.TrimSpace(output))
	}
	fastStart, err := MP4FastStart(outputPath)
	if err != nil {
		return err
	}
	if !fastStart {
		return fmt.Errorf("ffmpeg output is not fast start")
	}
	return nil
}

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
	defer input.Close()
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

// buildVideoPlayback creates a broadly compatible, bounded-bitrate web
// rendition while preserving the uploaded original for downloads.
func buildVideoPlayback(ctx context.Context, objects storage.ObjectStorage, ffmpegPath, objectPath, ownerID, mediaID, createdText string) (string, string, int64) {
	body, err := objects.Open(ctx, objectPath)
	if err != nil {
		return "", "", 0
	}
	defer body.Close()
	input, err := os.CreateTemp("", "dorm-video-playback-source-*.mp4")
	if err != nil {
		return "", "", 0
	}
	inputPath := input.Name()
	defer os.Remove(inputPath)
	if _, err = io.Copy(input, body); err != nil || input.Close() != nil {
		return "", "", 0
	}
	output, err := os.CreateTemp("", "dorm-video-playback-*.mp4")
	if err != nil {
		return "", "", 0
	}
	outputPath := output.Name()
	output.Close()
	defer os.Remove(outputPath)

	if _, err := transcodeVideoFile(ctx, ffmpegPath, "cpu", inputPath, outputPath, 2000); err != nil {
		return "", "", 0
	}
	file, err := os.Open(outputPath)
	if err != nil {
		return "", "", 0
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() <= 0 {
		return "", "", 0
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, createdText)
	playbackPath := "/playback/" + remoteOwnerSegment(ownerID) + "/" + createdAt.UTC().Format("2006/01") + "/" + mediaID + ".mp4"
	if err := objects.Put(ctx, playbackPath, file, info.Size()); err != nil {
		return "", "", 0
	}
	return playbackPath, "video/mp4", info.Size()
}

// transcodeVideoFile prefers a supported GPU H.264 encoder and falls back to
// libx264 if the driver rejects the hardware path. Audio is always normalized
// to AAC and Fast Start keeps MP4 metadata at the front of the file.
func transcodeVideoFile(ctx context.Context, ffmpegPath, preference, inputPath, outputPath string, targetKbps int) (string, error) {
	return transcodeVideoFileWithProgress(ctx, ffmpegPath, preference, inputPath, outputPath, targetKbps, nil)
}

// transcodeVideoFileWithProgress separates hardware encoding from the
// Fast Start remux. This lets callers stop reporting GPU activity as soon as
// the encoder exits, while the file is still being finalized on disk.
func transcodeVideoFileWithProgress(ctx context.Context, ffmpegPath, preference, inputPath, outputPath string, targetKbps int, onStep func(step, encoder string)) (string, error) {
	available := ffmpegEncoders(ctx, ffmpegPath)
	candidates := videoEncoderCandidates(preference, available)
	encodedPath := outputPath + ".encoded.mp4"
	defer os.Remove(encodedPath)
	var lastErr error
	for _, encoder := range candidates {
		_ = os.Remove(outputPath)
		_ = os.Remove(encodedPath)
		if onStep != nil {
			onStep("encoding", encoder)
		}
		processCtx, cancel := context.WithTimeout(ctx, 90*time.Minute)
		args := []string{
			"-hide_banner", "-loglevel", "error", "-i", inputPath,
			"-map", "0:v:0", "-map", "0:a:0?",
			"-vf", videoFilter(targetKbps),
		}
		args = append(args, videoEncoderArgs(encoder, targetKbps)...)
		args = append(args, "-profile:v", "high", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "128k", "-y", encodedPath)
		output, err := exec.CommandContext(processCtx, ffmpegPath, args...).CombinedOutput()
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("%s: %w: %s", encoder, err, bytes.TrimSpace(output))
			continue
		}
		info, statErr := os.Stat(encodedPath)
		if statErr != nil || info.Size() <= 0 {
			lastErr = fmt.Errorf("%s produced an invalid MP4", encoder)
			continue
		}
		if onStep != nil {
			onStep("finalizing", encoder)
		}
		if err := RemuxMP4FastStart(ctx, ffmpegPath, encodedPath, outputPath); err != nil {
			lastErr = fmt.Errorf("%s Fast Start remux: %w", encoder, err)
			continue
		}
		info, statErr = os.Stat(outputPath)
		fastStart, fastStartErr := MP4FastStart(outputPath)
		if statErr != nil || info.Size() <= 0 || fastStartErr != nil || !fastStart {
			lastErr = fmt.Errorf("%s produced an invalid fast-start MP4", encoder)
			continue
		}
		return encoder, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no H.264 encoder is available")
	}
	return "", lastErr
}

func ffmpegEncoders(ctx context.Context, ffmpegPath string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(probeCtx, ffmpegPath, "-hide_banner", "-encoders").CombinedOutput()
	if err != nil {
		return ""
	}
	return string(output)
}

func videoEncoderCandidates(preference, available string) []string {
	hardware := map[string]string{"nvenc": "h264_nvenc", "qsv": "h264_qsv", "amf": "h264_amf"}
	preference = strings.ToLower(strings.TrimSpace(preference))
	if preference == "cpu" {
		return []string{"libx264"}
	}
	var result []string
	if encoder := hardware[preference]; encoder != "" && strings.Contains(available, encoder) {
		result = append(result, encoder)
	}
	if preference == "auto" || len(result) == 0 {
		for _, encoder := range []string{"h264_nvenc", "h264_qsv", "h264_amf"} {
			if strings.Contains(available, encoder) && !containsString(result, encoder) {
				result = append(result, encoder)
			}
		}
	}
	return append(result, "libx264")
}

func videoEncoderArgs(encoder string, targetKbps int) []string {
	if targetKbps <= 0 {
		targetKbps = 2000
	}
	bitrate := strconv.Itoa(targetKbps) + "k"
	maxrate := strconv.Itoa(targetKbps*5/4) + "k"
	bufsize := strconv.Itoa(targetKbps*5/2) + "k"
	switch encoder {
	case "h264_nvenc":
		return []string{"-c:v", encoder, "-preset", "p4", "-tune", "hq", "-rc", "vbr", "-b:v", bitrate, "-maxrate", maxrate, "-bufsize", bufsize, "-spatial_aq", "1", "-temporal_aq", "1"}
	case "h264_qsv":
		return []string{"-c:v", encoder, "-preset", "veryfast", "-b:v", bitrate, "-maxrate", maxrate, "-bufsize", bufsize}
	case "h264_amf":
		return []string{"-c:v", encoder, "-quality", "speed", "-rc", "vbr_peak", "-b:v", bitrate, "-maxrate", maxrate, "-bufsize", bufsize}
	default:
		return []string{"-c:v", "libx264", "-preset", "veryfast", "-b:v", bitrate, "-maxrate", maxrate, "-bufsize", bufsize}
	}
}

func targetVideoBitrateKbps(sizeBytes, durationMS int64) int {
	if sizeBytes <= 0 || durationMS <= 0 {
		return 2000
	}
	sourceTotalKbps := sizeBytes * 8 / durationMS
	target := int(sourceTotalKbps*3/2) - 128
	if target < 700 {
		return 700
	}
	if target > 4000 {
		return 4000
	}
	return target
}

func videoFilter(targetKbps int) string {
	if targetKbps > 0 && targetKbps < 1500 {
		return "scale=w='min(1280,iw)':h='min(720,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2,fps=30"
	}
	return "scale=w='min(1920,iw)':h='min(1080,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2,fps=30"
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
