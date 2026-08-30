package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// videoPreparation describes the single resource selected for browser playback.
// UseOriginal means callers should alias the original, without copying it.
type videoPreparation struct {
	UseOriginal bool
	Encoder     string
	DurationMS  int64
	Width       int
	Height      int
}

type probedVideo struct {
	Streams []probedVideoStream `json:"streams"`
	Format  struct {
		Name     string            `json:"format_name"`
		Duration string            `json:"duration"`
		BitRate  string            `json:"bit_rate"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

type probedVideoStream struct {
	Codec       string `json:"codec_name"`
	Type        string `json:"codec_type"`
	Profile     string `json:"profile"`
	PixelFormat string `json:"pix_fmt"`
	Level       int    `json:"level"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	AverageFPS  string `json:"avg_frame_rate"`
	NominalFPS  string `json:"r_frame_rate"`
	BitRate     string `json:"bit_rate"`
	Duration    string `json:"duration"`
	FieldOrder  string `json:"field_order"`
	Channels    int    `json:"channels"`
	SampleRate  string `json:"sample_rate"`
	Disposition struct {
		AttachedPicture int `json:"attached_pic"`
	} `json:"disposition"`
}

func prepareVideoPlaybackFile(ctx context.Context, ffmpegPath, preference, inputPath, outputPath string, sizeBytes, durationMS int64, onStep func(step, encoder string)) (videoPreparation, error) {
	prepared := videoPreparation{DurationMS: durationMS}
	if err := ctx.Err(); err != nil {
		return prepared, err
	}
	if onStep != nil {
		onStep("probing", "")
	}
	probe, probeErr := probeVideo(ctx, ffmpegPath, inputPath)
	if ctx.Err() != nil {
		return prepared, ctx.Err()
	}
	if probeErr == nil {
		if actual := probe.durationMS(); actual > 0 {
			prepared.DurationMS = actual
		}
		for _, stream := range probe.Streams {
			if stream.Type == "video" && stream.Disposition.AttachedPicture == 0 {
				prepared.Width, prepared.Height = stream.Width, stream.Height
				break
			}
		}
		if probe.browserCompatible(sizeBytes) {
			fastStart, inspectErr := MP4FastStart(inputPath)
			if inspectErr == nil {
				if fastStart {
					prepared.UseOriginal, prepared.Encoder = true, "original"
					return prepared, nil
				}
				prepared.Encoder = "copy"
				if onStep != nil {
					onStep("finalizing", prepared.Encoder)
				}
				return prepared, remuxVideoPlayback(ctx, ffmpegPath, inputPath, outputPath)
			}
		}
	}
	// Unknown metadata is never enough to bypass normalization. ffprobe is
	// optional so an existing installation with just ffmpeg still works.
	encoder, err := transcodeVideoFileWithProgress(ctx, ffmpegPath, preference, inputPath, outputPath, targetVideoBitrateKbps(sizeBytes, prepared.DurationMS), onStep)
	prepared.Encoder = encoder
	return prepared, err
}

func probeVideo(ctx context.Context, ffmpegPath, inputPath string) (probedVideo, error) {
	var probe probedVideo
	probePath, err := findFFprobe(ffmpegPath)
	if err != nil {
		return probe, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"-v", "error", "-show_entries", "format=format_name,duration,bit_rate:format_tags=major_brand:stream=codec_name,codec_type,profile,pix_fmt,level,width,height,avg_frame_rate,r_frame_rate,bit_rate,duration,field_order,channels,sample_rate:stream_disposition=attached_pic", "-of", "json", inputPath}
	command := exec.CommandContext(probeCtx, probePath, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return probe, fmt.Errorf("probe video: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	if err := json.Unmarshal(stdout.Bytes(), &probe); err != nil {
		return probe, fmt.Errorf("parse video metadata: %w", err)
	}
	return probe, nil
}

func findFFprobe(ffmpegPath string) (string, error) {
	resolved, err := exec.LookPath(ffmpegPath)
	if err == nil {
		name := "ffprobe"
		if strings.EqualFold(filepath.Ext(resolved), ".exe") {
			name += ".exe"
		}
		if sibling, err := exec.LookPath(filepath.Join(filepath.Dir(resolved), name)); err == nil {
			return sibling, nil
		}
	}
	return exec.LookPath("ffprobe")
}

func (probe probedVideo) durationMS() int64 {
	duration := positiveNumber(probe.Format.Duration)
	if duration == 0 {
		for _, stream := range probe.Streams {
			if stream.Type == "video" && stream.Disposition.AttachedPicture == 0 {
				duration = positiveNumber(stream.Duration)
				break
			}
		}
	}
	if duration <= 0 || duration >= float64(math.MaxInt64)/1000 {
		return 0
	}
	return int64(math.Round(duration * 1000))
}

// browserCompatible is deliberately conservative: metadata we cannot verify
// falls back to transcode, and a MOV file is not accepted merely because it
// shares FFmpeg's mov,mp4,... demuxer name with MP4.
func (probe probedVideo) browserCompatible(sizeBytes int64) bool {
	if !containsString(strings.Split(probe.Format.Name, ","), "mp4") {
		return false
	}
	switch strings.TrimSpace(probe.Format.Tags["major_brand"]) {
	case "isom", "iso2", "iso3", "iso4", "iso5", "iso6", "mp41", "mp42", "avc1", "M4V":
	default:
		return false
	}
	duration := probe.durationMS()
	if duration <= 0 {
		return false
	}
	bitRate := positiveNumber(probe.Format.BitRate)
	if bitRate == 0 && sizeBytes > 0 {
		bitRate = float64(sizeBytes) * 8000 / float64(duration)
	}
	if bitRate <= 0 || bitRate > 4_000_000 {
		return false
	}
	videos, audios := 0, 0
	for _, stream := range probe.Streams {
		switch stream.Type {
		case "video":
			videos++
			if stream.Codec != "h264" || stream.PixelFormat != "yuv420p" || stream.Level <= 0 || stream.Level > 41 || stream.Disposition.AttachedPicture != 0 {
				return false
			}
			switch stream.Profile {
			case "Constrained Baseline", "Baseline", "Main", "High":
			default:
				return false
			}
			if stream.Width <= 0 || stream.Height <= 0 || stream.Width > 1920 || stream.Height > 1080 || stream.Width%2 != 0 || stream.Height%2 != 0 {
				return false
			}
			if stream.FieldOrder != "progressive" || positiveNumber(stream.BitRate) > 4_000_000 {
				return false
			}
			for _, rate := range []string{stream.AverageFPS, stream.NominalFPS} {
				if fps := videoFrameRate(rate); fps <= 0 || fps > 30 {
					return false
				}
			}
		case "audio":
			audios++
			if stream.Codec != "aac" || stream.Profile != "LC" || stream.Channels < 1 || stream.Channels > 2 {
				return false
			}
			if rate := positiveNumber(stream.SampleRate); rate <= 0 || rate > 48000 {
				return false
			}
		default:
			// Extra subtitle, data, and attached-picture tracks are dropped by
			// the standard transcode path instead of copied into playback.
			return false
		}
	}
	return videos == 1 && audios <= 1
}

func positiveNumber(value string) float64 {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number <= 0 {
		return 0
	}
	return number
}

func videoFrameRate(value string) float64 {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return positiveNumber(value)
	}
	denominator := positiveNumber(parts[1])
	if denominator == 0 {
		return 0
	}
	return positiveNumber(parts[0]) / denominator
}

// Retry a failed finalization separately; successfully encoded frames are
// retained for both attempts and never trigger another encoder candidate.
func remuxVideoPlayback(ctx context.Context, ffmpegPath, inputPath, outputPath string) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		_ = os.Remove(outputPath)
		if lastErr = RemuxMP4FastStart(ctx, ffmpegPath, inputPath, outputPath); lastErr == nil {
			return nil
		}
	}
	_ = os.Remove(outputPath)
	return lastErr
}
