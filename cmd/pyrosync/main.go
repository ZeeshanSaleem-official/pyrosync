package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pyrosync/internal/aggregate"
	"pyrosync/internal/api"
	"pyrosync/internal/config"
	"pyrosync/internal/db"
	"pyrosync/internal/ingest"
	"pyrosync/internal/sched"
)

const version = "0.3.0"

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
	case "recompute":
		if err := runRecompute(ctx, cfg, logger, os.Args[2:]); err != nil {
			fatal(cfg, logger, "recompute failed", err)
		}
	case "calibrate":
		if err := runCalibrate(ctx, cfg, logger, os.Args[2:]); err != nil {
			fatal(cfg, logger, "calibrate failed", err)
		}
	case "serve":
		if err := runServe(ctx, cfg, logger, os.Args[2:]); err != nil {
			fatal(cfg, logger, "serve failed", err)
		}
	case "ingest-api":
		if err := runIngestAPI(ctx, cfg, logger, os.Args[2:]); err != nil {
			fatal(cfg, logger, "ingest-api failed", err)
		}
	default:
		printUsage()
		os.Exit(1)
	}
}

func openMigratedPool(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	if err := cfg.RequireDB(); err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return nil, err
	}
	return db.Open(ctx, cfg.DatabaseURL)
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
	doRecompute := fs.Bool("recompute", false, "recompute dirty dates and recalibrate after ingest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: pyrosync ingest-file [--area auto] [--recompute] <path/to/file.csv>")
	}
	pool, err := openMigratedPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	rep, err := ingest.IngestFile(ctx, pool, fs.Arg(0), ingest.FileOptions{AreaID: *area})
	if rep != nil {
		printIngestReport(logger, rep)
	}
	if err != nil {
		return err
	}
	if *doRecompute {
		return recomputeAndCalibrate(ctx, pool, logger)
	}
	return nil
}

func runIngestDir(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("ingest-dir", flag.ContinueOnError)
	area := fs.String("area", "auto", "coverage area ID (auto, world, or preset region ID)")
	doRecompute := fs.Bool("recompute", false, "recompute dirty dates and recalibrate after ingest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: pyrosync ingest-dir [--area auto] [--recompute] <path/to/dir>")
	}
	pool, err := openMigratedPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	reports, err := ingest.IngestDir(ctx, pool, fs.Arg(0), ingest.FileOptions{AreaID: *area})
	for _, rep := range reports {
		printIngestReport(logger, rep)
	}
	if err != nil {
		return err
	}
	if *doRecompute {
		return recomputeAndCalibrate(ctx, pool, logger)
	}
	return nil
}

func runRecompute(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("recompute", flag.ContinueOnError)
	allFlag := fs.Bool("all", false, "recompute all covered and active dates")
	dirtyFlag := fs.Bool("dirty", false, "recompute dates queued in aggregate_dirty")
	dateStr := fs.String("date", "", "recompute a single UTC date (YYYY-MM-DD)")
	sinceStr := fs.String("since", "", "recompute dates on or after YYYY-MM-DD (UTC)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	modeCount := 0
	if *allFlag {
		modeCount++
	}
	if *dirtyFlag {
		modeCount++
	}
	if *dateStr != "" {
		modeCount++
	}
	if *sinceStr != "" {
		modeCount++
	}
	if modeCount > 1 {
		return fmt.Errorf("specify at most one of --all, --dirty, --date, or --since")
	}

	pool, err := openMigratedPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	var rep *aggregate.RecomputeReport
	switch {
	case *allFlag:
		rep, err = aggregate.RecomputeAll(ctx, pool)
	case *dateStr != "":
		d, parseErr := time.Parse("2006-01-02", *dateStr)
		if parseErr != nil {
			return fmt.Errorf("invalid --date %q: use YYYY-MM-DD", *dateStr)
		}
		rep, err = aggregate.RecomputeDates(ctx, pool, []time.Time{d})
	case *sinceStr != "":
		d, parseErr := time.Parse("2006-01-02", *sinceStr)
		if parseErr != nil {
			return fmt.Errorf("invalid --since %q: use YYYY-MM-DD", *sinceStr)
		}
		rep, err = aggregate.RecomputeSince(ctx, pool, d)
	default:
		rep, err = aggregate.RecomputeDirty(ctx, pool)
	}
	if rep != nil {
		logger.Info("recompute complete",
			"dates_processed", rep.DatesProcessed,
			"cell_a1_rows", rep.CellA1Rows,
			"cell_a10_rows", rep.CellA10Rows,
			"region_day_rows", rep.RegionDayRows,
			"duration_ms", rep.Duration.Milliseconds(),
		)
	}
	return err
}

func runCalibrate(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("calibrate", flag.ContinueOnError)
	conf := fs.Int("conf", 2, "minimum confidence level (1=all, 2=nominal+high, 3=high)")
	liveDays := fs.Int("live-days", 30, "trailing window in days for live calibration scope")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *conf < 1 || *conf > 3 {
		return fmt.Errorf("invalid --conf %d: must be 1, 2, or 3", *conf)
	}
	if *liveDays <= 0 {
		return fmt.Errorf("invalid --live-days %d: must be positive", *liveDays)
	}

	pool, err := openMigratedPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	models, err := aggregate.CalibrateAll(ctx, pool, aggregate.CalibrateOptions{
		ConfLevel: *conf,
		LiveDays:  *liveDays,
	})
	if err != nil {
		return err
	}
	for _, m := range models {
		logger.Info("calibrated region",
			"region_id", m.RegionID,
			"scope", m.Scope,
			"n_days", m.NDays,
			"k", fmt.Sprintf("%.4f", m.K),
			"ci_low", fmt.Sprintf("%.4f", m.CILow),
			"ci_high", fmt.Sprintf("%.4f", m.CIHigh),
			"reliable", m.Reliable,
			"k_applied", fmt.Sprintf("%.4f", m.KApplied),
		)
	}
	return nil
}

func runServe(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	noPoller := fs.Bool("no-poller", false, "disable the background NRT poller")
	addr := fs.String("addr", "", "override HTTP listen address (default from HTTP_ADDR)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *addr != "" {
		cfg.HTTPAddr = *addr
	}

	pool, err := openMigratedPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	interval := time.Duration(cfg.IngestIntervalMin) * time.Minute
	poller := sched.NewPoller(pool, cfg.FirmsMapKey, interval, sched.WithLogger(logger))

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *noPoller {
		logger.Info("background NRT poller disabled via --no-poller")
	} else if cfg.FirmsMapKey == "" {
		logger.Info("background NRT poller disabled (FIRMS_MAP_KEY not set)")
	} else {
		go poller.Start(sigCtx)
	}

	srv := api.New(pool, cfg, poller, logger)
	return srv.ListenAndServe(sigCtx)
}

func runIngestAPI(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("ingest-api", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if cfg.FirmsMapKey == "" {
		return fmt.Errorf("FIRMS_MAP_KEY is not configured")
	}

	pool, err := openMigratedPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	poller := sched.NewPoller(pool, cfg.FirmsMapKey, 0, sched.WithLogger(logger))
	rep, err := poller.RunOnce(ctx)
	if rep != nil {
		logger.Info("api ingest complete",
			"run_id", rep.RunID,
			"rows_in", rep.RowsIn,
			"recomputed_dates", rep.RecomputedDates,
			"duration_ms", rep.DurationMS,
			"feeds", len(rep.Feeds),
		)
	}
	return err
}

func recomputeAndCalibrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	rep, err := aggregate.RecomputeDirty(ctx, pool)
	if rep != nil {
		logger.Info("recompute complete",
			"dates_processed", rep.DatesProcessed,
			"cell_a1_rows", rep.CellA1Rows,
			"cell_a10_rows", rep.CellA10Rows,
			"region_day_rows", rep.RegionDayRows,
			"duration_ms", rep.Duration.Milliseconds(),
		)
	}
	if err != nil {
		return err
	}
	models, err := aggregate.CalibrateAll(ctx, pool, aggregate.CalibrateOptions{ConfLevel: 2, LiveDays: 30})
	if err != nil {
		return err
	}
	logger.Info("calibration refreshed", "models", len(models))
	return nil
}

func printIngestReport(logger *slog.Logger, rep *ingest.FileReport) {
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
	fmt.Println("Commands: migrate, ingest-file, ingest-dir, recompute, calibrate, serve, ingest-api, version")
}
