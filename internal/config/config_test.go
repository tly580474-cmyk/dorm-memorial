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
}

func TestProductionRequiresSecureCookie(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_COOKIE_SECURE", "false")
	if _, err := Load(); err == nil {
		t.Fatal("expected production without secure cookies to fail")
	}
}
