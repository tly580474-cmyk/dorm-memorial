package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"dorm-memorial/internal/database"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) < 2 {
		fatal(logger, "usage: dbtool backup|restore [options]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	switch os.Args[1] {
	case "backup":
		set := flag.NewFlagSet("backup", flag.ExitOnError)
		source := set.String("source", "data/dorm-memorial.db", "source SQLite database")
		destination := set.String("destination", "", "new backup file")
		_ = set.Parse(os.Args[2:])
		if *destination == "" {
			*destination = fmt.Sprintf("backups/dorm-memorial-%s.db", time.Now().UTC().Format("20060102T150405Z"))
		}
		db, err := database.Open(ctx, *source)
		if err != nil {
			fatal(logger, err.Error())
		}
		defer db.Close()
		if err := database.Backup(ctx, db, *destination); err != nil {
			fatal(logger, err.Error())
		}
		logger.Info("backup_completed", "destination", *destination)
	case "restore":
		set := flag.NewFlagSet("restore", flag.ExitOnError)
		source := set.String("source", "", "verified backup file")
		destination := set.String("destination", "", "new database file")
		_ = set.Parse(os.Args[2:])
		if *source == "" || *destination == "" {
			fatal(logger, "restore requires -source and -destination")
		}
		if err := database.Restore(ctx, *source, *destination); err != nil {
			fatal(logger, err.Error())
		}
		logger.Info("restore_completed", "destination", *destination)
	default:
		fatal(logger, "unknown command: "+os.Args[1])
	}
}

func fatal(logger *slog.Logger, message string) {
	logger.Error("dbtool_failed", "error", message)
	os.Exit(1)
}
