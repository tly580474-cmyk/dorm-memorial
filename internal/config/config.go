package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment         string
	Address             string
	DatabasePath        string
	FrontendDir         string
	PublicURL           string
	CookieSecure        bool
	SessionTTL          time.Duration
	BootstrapUsername   string
	BootstrapEmail      string
	BootstrapPassword   string
	BootstrapNickname   string
	AListBaseURL        string
	AListUsername       string
	AListPassword       string
	AListToken          string
	AListRoot           string
	MediaCacheDir       string
	MediaCacheMaxBytes  int64
	MediaStagingDir     string
	VideoEncoder        string
	MaxVideoUploadBytes int64
	MaxImageUploadBytes int64
	FFmpegPath          string
}

func Load() (Config, error) {
	fileValues, err := readEnvFile(".env")
	if err != nil {
		return Config{}, fmt.Errorf("read .env: %w", err)
	}
	cfg := Config{
		Environment:       strings.ToLower(strings.TrimSpace(env(fileValues, "APP_ENV", "development"))),
		Address:           env(fileValues, "APP_ADDRESS", "127.0.0.1:8080"),
		DatabasePath:      env(fileValues, "APP_DATABASE_PATH", "data/dorm-memorial.db"),
		FrontendDir:       env(fileValues, "APP_FRONTEND_DIR", "web/dist"),
		PublicURL:         strings.TrimRight(env(fileValues, "APP_PUBLIC_URL", "http://127.0.0.1:8080"), "/"),
		BootstrapUsername: strings.TrimSpace(env(fileValues, "APP_BOOTSTRAP_ADMIN_USERNAME", "")),
		BootstrapEmail:    strings.TrimSpace(env(fileValues, "APP_BOOTSTRAP_ADMIN_EMAIL", "")),
		BootstrapPassword: env(fileValues, "APP_BOOTSTRAP_ADMIN_PASSWORD", ""),
		BootstrapNickname: strings.TrimSpace(env(fileValues, "APP_BOOTSTRAP_ADMIN_NICKNAME", "")),
		AListBaseURL:      strings.TrimRight(strings.TrimSpace(env(fileValues, "ALIST_BASE_URL", "")), "/"),
		AListUsername:     strings.TrimSpace(env(fileValues, "ALIST_USERNAME", "")),
		AListPassword:     env(fileValues, "ALIST_PASSWORD", ""),
		AListToken:        strings.TrimSpace(env(fileValues, "ALIST_TOKEN", "")),
		AListRoot:         strings.TrimSpace(env(fileValues, "ALIST_ROOT", "/")),
		MediaCacheDir:     strings.TrimSpace(env(fileValues, "APP_MEDIA_CACHE_DIR", "data/media-cache")),
		MediaStagingDir:   strings.TrimSpace(env(fileValues, "APP_MEDIA_STAGING_DIR", "data/media-staging")),
		VideoEncoder:      strings.ToLower(strings.TrimSpace(env(fileValues, "APP_VIDEO_ENCODER", "auto"))),
		FFmpegPath:        strings.TrimSpace(env(fileValues, "APP_FFMPEG_PATH", "ffmpeg")),
	}
	cacheMaxBytes, err := strconv.ParseInt(env(fileValues, "APP_MEDIA_CACHE_MAX_BYTES", "2147483648"), 10, 64)
	if err != nil || cacheMaxBytes < 0 {
		return Config{}, errors.New("APP_MEDIA_CACHE_MAX_BYTES must be a non-negative integer")
	}
	cfg.MediaCacheMaxBytes = cacheMaxBytes

	maxVideoUploadBytes, err := strconv.ParseInt(env(fileValues, "APP_MAX_VIDEO_UPLOAD_BYTES", "314572800"), 10, 64)
	if err != nil || maxVideoUploadBytes <= 0 || maxVideoUploadBytes > 8<<30 {
		return Config{}, errors.New("APP_MAX_VIDEO_UPLOAD_BYTES must be a positive integer no greater than 8589934592")
	}
	cfg.MaxVideoUploadBytes = maxVideoUploadBytes

	maxImageUploadBytes, err := strconv.ParseInt(env(fileValues, "APP_MAX_IMAGE_UPLOAD_BYTES", "15728640"), 10, 64)
	if err != nil || maxImageUploadBytes <= 0 || maxImageUploadBytes > 8<<30 {
		return Config{}, errors.New("APP_MAX_IMAGE_UPLOAD_BYTES must be a positive integer no greater than 8589934592")
	}
	cfg.MaxImageUploadBytes = maxImageUploadBytes

	secure, err := strconv.ParseBool(env(fileValues, "APP_COOKIE_SECURE", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("parse APP_COOKIE_SECURE: %w", err)
	}
	cfg.CookieSecure = secure

	ttl, err := time.ParseDuration(env(fileValues, "APP_SESSION_TTL", "720h"))
	if err != nil || ttl < time.Hour {
		return Config{}, errors.New("APP_SESSION_TTL must be a duration of at least one hour")
	}
	cfg.SessionTTL = ttl

	if cfg.Environment != "development" && cfg.Environment != "test" && cfg.Environment != "production" {
		return Config{}, errors.New("APP_ENV must be development, test, or production")
	}
	publicURL, err := url.Parse(cfg.PublicURL)
	if err != nil || publicURL.Host == "" || publicURL.User != nil || (publicURL.Scheme != "http" && publicURL.Scheme != "https") || publicURL.RawQuery != "" || publicURL.Fragment != "" {
		return Config{}, errors.New("APP_PUBLIC_URL must be an http or https URL without credentials, query, or fragment")
	}
	if cfg.Environment == "production" && publicURL.Scheme != "https" {
		return Config{}, errors.New("APP_PUBLIC_URL must use https in production")
	}
	if cfg.Environment == "production" && !cfg.CookieSecure {
		return Config{}, errors.New("APP_COOKIE_SECURE must be true in production")
	}
	if cfg.DatabasePath == "" || cfg.Address == "" || cfg.FFmpegPath == "" || cfg.MediaStagingDir == "" || (cfg.MediaCacheMaxBytes > 0 && cfg.MediaCacheDir == "") {
		return Config{}, errors.New("APP_DATABASE_PATH, APP_ADDRESS, and enabled cache directory are required")
	}
	if cfg.VideoEncoder != "auto" && cfg.VideoEncoder != "cpu" && cfg.VideoEncoder != "nvenc" && cfg.VideoEncoder != "qsv" && cfg.VideoEncoder != "amf" {
		return Config{}, errors.New("APP_VIDEO_ENCODER must be auto, cpu, nvenc, qsv, or amf")
	}
	bootstrapSet := cfg.BootstrapUsername != "" || cfg.BootstrapEmail != "" || cfg.BootstrapPassword != ""
	if bootstrapSet && (cfg.BootstrapUsername == "" || cfg.BootstrapEmail == "" || len(cfg.BootstrapPassword) < 12 || len(cfg.BootstrapPassword) > 72) {
		return Config{}, errors.New("bootstrap admin requires username, email, and a password of 12-72 UTF-8 bytes")
	}
	return cfg, nil
}

func env(fileValues map[string]string, name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	if value, ok := fileValues[name]; ok {
		return value
	}
	return fallback
}

func readEnvFile(path string) (map[string]string, error) {
	values := make(map[string]string)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		values[name] = value
	}
	return values, scanner.Err()
}
