package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"

	"pyrosync/internal/config"
	"pyrosync/internal/db"
	"pyrosync/internal/ingest"
)

const version = "0.1.0-phase1"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cfg, err := config.Load(".env")
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	slog.SetDefault(logger)

	ctx := context.Background()
	cmd := os.Args[1]

	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("pyrosync", version)
	case "migrate":
		if err := runMigrate(ctx, cfg); err != nil {
			fatal(cfg, logger, "migrate failed", err)
		}
		fmt.Println("migrations applied")
	case "ingest-file":
		if err := runIngestFile(ctx, cfg, logger, os.Args[2:]); err != nil {
			fatal(cfg, logger, "ingest-file failed", err)
		}
	case "ingest-dir":
		if err := runIngestDir(ctx, cfg, logger, os.Args[2:]); err != nil {
			fatal(cfg, logger, "ingest-dir failed", err)
		}
	case "serve", "ingest-api", "recompute", "calibrate":
		fmt.Fprintf(os.Stderr, "subcommand %q will be implemented in Phase 2-3\n", cmd)
		os.Exit(1)
	default:
		printUsage()
		os.Exit(1)
	}
}

func runMigrate(ctx context.Context, cfg config.Config) error {
	if err := cfg.RequireDB(); err != nil {
		return err
	}
	return db.Migrate(ctx, cfg.DatabaseURL)
}

func runIngestFile(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("ingest-file", flag.ContinueOnError)
	area := fs.String("area", "auto", "coverage area ID (auto, world, or preset region ID)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: pyrosync ingest-file [--area auto] <path/to/file.csv>")
	}
	if err := cfg.RequireDB(); err != nil {
		return err
	}
	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	rep, err := ingest.IngestFile(ctx, pool, fs.Arg(0), ingest.FileOptions{AreaID: *area})
	if rep != nil {
		printReport(logger, rep)
	}
	return err
}

func runIngestDir(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("ingest-dir", flag.ContinueOnError)
	area := fs.String("area", "auto", "coverage area ID (auto, world, or preset region ID)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: pyrosync ingest-dir [--area auto] <path/to/dir>")
	}
	if err := cfg.RequireDB(); err != nil {
		return err
	}
	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	reports, err := ingest.IngestDir(ctx, pool, fs.Arg(0), ingest.FileOptions{AreaID: *area})
	for _, rep := range reports {
		printReport(logger, rep)
	}
	return err
}

func printReport(logger *slog.Logger, rep *ingest.FileReport) {
	if rep.SkippedDuplicate {
		logger.Info("skipped duplicate file", "file", rep.FileName, "sha256", rep.SHA256)
		return
	}
	reasons := make([]string, 0, len(rep.RejectReasons))
	for k := range rep.RejectReasons {
		reasons = append(reasons, k)
	}
	sort.Strings(reasons)

	logger.Info("ingested file",
		"file", rep.FileName,
		"schema", rep.Schema,
		"area", rep.AreaID,
		"rows_read", rep.RowsRead,
		"rows_inserted", rep.RowsInserted,
		"rows_updated", rep.RowsUpdated,
		"rows_ignored", rep.RowsIgnored,
		"rows_rejected", rep.RowsRejected,
		"reject_reasons", rep.RejectReasons,
		"duration_ms", rep.Duration.Milliseconds(),
	)
}

func fatal(cfg config.Config, logger *slog.Logger, msg string, err error) {
	logger.Error(msg, "error", cfg.Redact(err.Error()))
	os.Exit(1)
}

func printUsage() {
	fmt.Println("Usage: pyrosync <command> [flags]")
	fmt.Println("Commands: migrate, ingest-file, ingest-dir, serve, ingest-api, recompute, calibrate, version")
}
