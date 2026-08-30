package media

import (
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func compatibleProbeFixture() probedVideo {
	probe := probedVideo{Streams: []probedVideoStream{{
		Type: "video", Codec: "h264", Profile: "High", PixelFormat: "yuv420p", Level: 40,
		Width: 1280, Height: 720, AverageFPS: "30000/1001", NominalFPS: "30000/1001", FieldOrder: "progressive",
	}}}
	probe.Format.Name = "mov,mp4,m4a,3gp,3g2,mj2"
	probe.Format.Tags = map[string]string{"major_brand": "isom"}
	probe.Format.Duration = "10.000"
	probe.Format.BitRate = "2000000"
	return probe
}

func TestVideoProbeCompatibilityIsConservative(t *testing.T) {
	tests := []struct {
		name   string
		change func(*probedVideo)
		want   bool
	}{
		{"compatible silent MP4", func(*probedVideo) {}, true},
		{"compatible AAC", func(p *probedVideo) {
			p.Streams = append(p.Streams, probedVideoStream{Type: "audio", Codec: "aac", Profile: "LC", Channels: 2, SampleRate: "48000"})
		}, true},
		{"quicktime container", func(p *probedVideo) { p.Format.Tags["major_brand"] = "qt  " }, false},
		{"matroska container", func(p *probedVideo) { p.Format.Name = "matroska,webm" }, false},
		{"HEVC", func(p *probedVideo) { p.Streams[0].Codec = "hevc" }, false},
		{"10-bit", func(p *probedVideo) { p.Streams[0].PixelFormat = "yuv420p10le" }, false},
		{"high 444 profile", func(p *probedVideo) { p.Streams[0].Profile = "High 4:4:4 Predictive" }, false},
		{"high level", func(p *probedVideo) { p.Streams[0].Level = 51 }, false},
		{"unknown level", func(p *probedVideo) { p.Streams[0].Level = -99 }, false},
		{"large frame", func(p *probedVideo) { p.Streams[0].Width = 3840 }, false},
		{"portrait over height bound", func(p *probedVideo) { p.Streams[0].Height = 1920 }, false},
		{"high frame rate", func(p *probedVideo) { p.Streams[0].AverageFPS = "60/1" }, false},
		{"variable frame rate peak", func(p *probedVideo) { p.Streams[0].NominalFPS = "60/1" }, false},
		{"unknown frame rate", func(p *probedVideo) { p.Streams[0].AverageFPS = "0/0" }, false},
		{"interlaced", func(p *probedVideo) { p.Streams[0].FieldOrder = "tt" }, false},
		{"too much bitrate", func(p *probedVideo) { p.Format.BitRate = "4000001" }, false},
		{"missing duration", func(p *probedVideo) { p.Format.Duration = "N/A" }, false},
		{"nonfinite duration", func(p *probedVideo) { p.Format.Duration = "NaN" }, false},
		{"subtitle track", func(p *probedVideo) { p.Streams = append(p.Streams, probedVideoStream{Type: "subtitle"}) }, false},
		{"multiple videos", func(p *probedVideo) { p.Streams = append(p.Streams, p.Streams[0]) }, false},
		{"surround AAC", func(p *probedVideo) {
			p.Streams = append(p.Streams, probedVideoStream{Type: "audio", Codec: "aac", Profile: "LC", Channels: 6, SampleRate: "48000"})
		}, false},
		{"unsupported audio", func(p *probedVideo) { p.Streams = append(p.Streams, probedVideoStream{Type: "audio", Codec: "opus"}) }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := compatibleProbeFixture()
			test.change(&probe)
			if got := probe.browserCompatible(2_500_000); got != test.want {
				t.Fatalf("compatible=%t, want %t", got, test.want)
			}
		})
	}
}

func TestVideoBitrateDoesNotInflateOrOverflow(t *testing.T) {
	for _, test := range []struct {
		size, duration int64
		want           int
	}{
		{2_500_000, 10_000, 1872},
		{0, 10_000, 2000},
		{2_500_000, 0, 2000},
		{1, 10_000, 700},
		{math.MaxInt64, 1, 4000},
	} {
		if got := targetVideoBitrateKbps(test.size, test.duration); got != test.want {
			t.Errorf("bitrate(%d,%d)=%d, want %d", test.size, test.duration, got, test.want)
		}
	}
}

func preparationFFmpeg(t *testing.T) string {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if _, err := findFFprobe(ffmpeg); err != nil {
		t.Skip("ffprobe is not installed")
	}
	return ffmpeg
}

func createPreparationVideo(t *testing.T, ffmpeg, filename, codec, fps string, fastStart bool) []byte {
	t.Helper()
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=" + fps, "-t", "1", "-c:v", codec, "-pix_fmt", "yuv420p"}
	if fastStart {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, "-y", filename)
	if output, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("create video: %v: %s", err, output)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPrepareVideoPlaybackSelectsOriginalRemuxOrTranscode(t *testing.T) {
	ffmpeg := preparationFFmpeg(t)
	for _, test := range []struct {
		name, codec, fps, encoder string
		fastStart, original       bool
	}{
		{"reuse faststart H264", "libx264", "12", "original", true, true},
		{"remux H264", "libx264", "12", "copy", false, false},
		{"transcode MPEG4 preserving low fps", "mpeg4", "12", "libx264", false, false},
		{"transcode high fps", "libx264", "60", "libx264", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			input, output := filepath.Join(dir, "original.mp4"), filepath.Join(dir, "playback.mp4")
			original := createPreparationVideo(t, ffmpeg, input, test.codec, test.fps, test.fastStart)
			var steps []string
			prepared, err := prepareVideoPlaybackFile(context.Background(), ffmpeg, "cpu", input, output, int64(len(original)), 999_000, func(step, _ string) {
				steps = append(steps, step)
			})
			if err != nil {
				t.Fatal(err)
			}
			if prepared.UseOriginal != test.original || prepared.Encoder != test.encoder {
				t.Fatalf("preparation=%+v", prepared)
			}
			if prepared.DurationMS != 1000 || prepared.Width != 320 || prepared.Height != 180 {
				t.Fatalf("real metadata was not used: %+v", prepared)
			}
			unchanged, err := os.ReadFile(input)
			if err != nil || sha256.Sum256(unchanged) != sha256.Sum256(original) {
				t.Fatalf("original changed: %v", err)
			}
			selected := output
			if test.original {
				if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("reuse must not create a duplicate playback file: %v", err)
				}
				selected = input
				if !reflect.DeepEqual(steps, []string{"probing"}) {
					t.Fatalf("reuse reported encoding work: %v", steps)
				}
			} else if test.encoder == "copy" {
				if !reflect.DeepEqual(steps, []string{"probing", "finalizing"}) {
					t.Fatalf("remux steps=%v", steps)
				}
			} else if !reflect.DeepEqual(steps, []string{"probing", "encoding", "finalizing"}) {
				t.Fatalf("transcode steps=%v", steps)
			}
			if fast, err := MP4FastStart(selected); err != nil || !fast {
				t.Fatalf("playback is not fast-start: %v", err)
			}
			probe, err := probeVideo(context.Background(), ffmpeg, selected)
			if err != nil {
				t.Fatal(err)
			}
			if probe.Streams[0].Codec != "h264" || probe.Streams[0].PixelFormat != "yuv420p" {
				t.Fatalf("incompatible playback: %+v", probe.Streams[0])
			}
			wantFPS := 12.0
			if test.fps == "60" {
				wantFPS = 30
			}
			if fps := videoFrameRate(probe.Streams[0].AverageFPS); fps != wantFPS {
				t.Fatalf("fps=%v, want %v", fps, wantFPS)
			}
		})
	}
}

func TestPrepareVideoProbeFailureStillAttemptsTranscode(t *testing.T) {
	ffmpeg := preparationFFmpeg(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "invalid.mp4")
	if err := os.WriteFile(input, []byte("not a valid video"), 0o600); err != nil {
		t.Fatal(err)
	}
	var steps []string
	prepared, err := prepareVideoPlaybackFile(context.Background(), ffmpeg, "cpu", input, filepath.Join(dir, "output.mp4"), 17, 1000, func(step, _ string) {
		steps = append(steps, step)
	})
	if err == nil || prepared.UseOriginal || !reflect.DeepEqual(steps, []string{"probing", "encoding"}) {
		t.Fatalf("unsafe probe fallback: preparation=%+v steps=%v err=%v", prepared, steps, err)
	}
}

func TestVideoFinalizationFailurePreservesSuccessfulEncoder(t *testing.T) {
	ffmpeg := preparationFFmpeg(t)
	dir := t.TempDir()
	input, output := filepath.Join(dir, "input.mp4"), filepath.Join(dir, "output.mp4")
	createPreparationVideo(t, ffmpeg, input, "libx264", "12", false)
	var steps []string
	encoder, err := transcodeVideoFileWithProgress(context.Background(), ffmpeg, "cpu", input, output, 1000, func(step, _ string) {
		steps = append(steps, step)
		if step == "finalizing" {
			// Encoding has succeeded, then an unwritable destination makes
			// both remux attempts fail without invalidating the encoded video.
			if err := os.Mkdir(output, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(output, "block"), []byte("block"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	})
	if err == nil || encoder != "libx264" || !reflect.DeepEqual(steps, []string{"encoding", "finalizing"}) {
		t.Fatalf("finalization failure lost completed encoding: encoder=%q steps=%v err=%v", encoder, steps, err)
	}
}

func TestPrepareVideoHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prepared, err := prepareVideoPlaybackFile(ctx, "missing-ffmpeg", "cpu", "input", "output", 1000, 1000, nil)
	if !errors.Is(err, context.Canceled) || prepared.UseOriginal {
		t.Fatalf("preparation=%+v err=%v", prepared, err)
	}
}

func TestLegacyVideoPlaybackReusesCompatibleOriginal(t *testing.T) {
	ffmpeg := preparationFFmpeg(t)
	input := filepath.Join(t.TempDir(), "input.mp4")
	original := createPreparationVideo(t, ffmpeg, input, "libx264", "12", true)
	objects := newMemoryObjects()
	objects.objects["/original.mp4"] = original
	objectPath, mimeType, size := buildVideoPlayback(context.Background(), objects, ffmpeg, "/original.mp4", "owner", "legacy-media", time.Now().UTC().Format(time.RFC3339Nano), "cpu")
	if objectPath != "/original.mp4" || mimeType != "video/mp4" || size != int64(len(original)) {
		t.Fatalf("legacy original alias: path=%q mime=%q size=%d", objectPath, mimeType, size)
	}
	if len(objects.objects) != 1 {
		t.Fatalf("reused original created redundant objects: %d", len(objects.objects))
	}
}

func TestPrepareVideoReusesMP4WithAACAudio(t *testing.T) {
	ffmpeg := preparationFFmpeg(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "with-audio.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", "-y", input}
	if output, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("create video with audio: %v: %s", err, output)
	}
	info, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareVideoPlaybackFile(context.Background(), ffmpeg, "cpu", input, filepath.Join(dir, "playback.mp4"), info.Size(), 0, nil)
	if err != nil || !prepared.UseOriginal || prepared.Encoder != "original" {
		t.Fatalf("AAC MP4 should be reused: preparation=%+v err=%v", prepared, err)
	}
}
