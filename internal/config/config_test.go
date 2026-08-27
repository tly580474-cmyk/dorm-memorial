package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("APP_ADDRESS", "")
	t.Setenv("APP_DATABASE_PATH", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected empty required values to fail")
	}
}

func TestEnvironmentOverridesFileValues(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("APP_ADDRESS", "127.0.0.1:9090")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Address != "127.0.0.1:9090" {
		t.Fatalf("address=%q", cfg.Address)
	}
	if cfg.MediaCacheMaxBytes != 2<<30 || cfg.MediaCacheDir != "data/media-cache" {
		t.Fatalf("unexpected cache defaults: dir=%q bytes=%d", cfg.MediaCacheDir, cfg.MediaCacheMaxBytes)
	}
	if cfg.MaxVideoUploadBytes != 150<<20 {
		t.Fatalf("unexpected video upload limit: %d", cfg.MaxVideoUploadBytes)
	}
	if cfg.FFmpegPath != "ffmpeg" {
		t.Fatalf("unexpected ffmpeg default: %q", cfg.FFmpegPath)
	}
}

func TestRejectsInvalidMaxVideoUploadSize(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("APP_MAX_VIDEO_UPLOAD_BYTES", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected non-positive video upload size to fail")
	}
}

func TestRejectsInvalidMediaCacheSize(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("APP_MEDIA_CACHE_MAX_BYTES", "2GiB")
	if _, err := Load(); err == nil {
		t.Fatal("expected non-integer cache size to fail")
	}
}

func TestProductionRequiresSecureCookie(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_COOKIE_SECURE", "false")
	if _, err := Load(); err == nil {
		t.Fatal("expected production without secure cookies to fail")
	}
}
