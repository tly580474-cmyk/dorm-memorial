package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dorm-memorial/internal/config"
	"dorm-memorial/internal/database"
	"dorm-memorial/internal/httpapi"
	"dorm-memorial/internal/identity"
	"dorm-memorial/internal/storage"
	"dorm-memorial/internal/storage/alist"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration_invalid", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		logger.Error("database_open_failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	identities := identity.NewStore(db)
	created, err := identities.BootstrapAdmin(ctx, cfg.BootstrapUsername, cfg.BootstrapEmail, cfg.BootstrapPassword, cfg.BootstrapNickname)
	if err != nil {
		logger.Error("bootstrap_admin_failed", "error", err)
		os.Exit(1)
	}
	if created {
		logger.Info("bootstrap_admin_created", "username", cfg.BootstrapUsername)
	}

	var objects storage.ObjectStorage
	if cfg.AListBaseURL != "" && (cfg.AListToken != "" || (cfg.AListUsername != "" && cfg.AListPassword != "")) {
		alistClient, err := alist.New(alist.Config{BaseURL: cfg.AListBaseURL, Token: cfg.AListToken, Username: cfg.AListUsername, Password: cfg.AListPassword, Root: cfg.AListRoot})
		if err != nil {
			logger.Error("storage_configuration_invalid", "error", err)
			os.Exit(1)
		}
		if cfg.AListUsername != "" && cfg.AListPassword != "" {
			authCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err = alistClient.Authenticate(authCtx)
			cancel()
			if err != nil {
				logger.Error("storage_authentication_failed", "error", err)
				os.Exit(1)
			}
		}
		objects = alistClient
		logger.Info("storage_ready", "provider", "alist", "root", cfg.AListRoot)
	} else {
		logger.Warn("storage_disabled", "reason", "AList credentials are not configured")
	}

	api := httpapi.New(cfg, db, identities, logger, objects)
	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	stopping, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		logger.Info("server_started", "address", cfg.Address, "environment", cfg.Environment)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server_failed", "error", err)
			os.Exit(1)
		}
	}()
	<-stopping.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server_shutdown_failed", "error", err)
	}
}
