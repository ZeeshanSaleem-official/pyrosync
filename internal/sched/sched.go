package sched

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pyrosync/internal/aggregate"
	"pyrosync/internal/ingest"
)

const (
	defaultBaseURL    = "https://firms.modaps.eosdis.nasa.gov"
	advisoryLockKey   = int64(0x5059524F53594E43) // "PYROSYNC"
	maxResponseBytes  = 128 << 20                 // 128 MiB guard per feed
	defaultHTTPTimeout = 2 * time.Minute
)

// DefaultNRTSources lists the active MODIS and VIIRS near-real-time FIRMS feeds.
var DefaultNRTSources = []string{
	"MODIS_NRT",
	"VIIRS_SNPP_NRT",
	"VIIRS_NOAA20_NRT",
	"VIIRS_NOAA21_NRT",
}

// ErrPollInProgress is returned when a poll cycle is already running.
var ErrPollInProgress = errors.New("NRT poll already in progress")

// FeedResult summarizes one NRT feed fetch and ingest pass.
type FeedResult struct {
	Source           string `json:"source"`
	RowsRead         int    `json:"rows_read"`
	RowsInserted     int    `json:"rows_inserted"`
	RowsUpdated      int    `json:"rows_updated"`
	SkippedDuplicate bool   `json:"skipped_duplicate"`
	Error            string `json:"error,omitempty"`
}

// PollReport summarizes a complete NRT polling cycle across all configured feeds.
type PollReport struct {
	RunID           int64        `json:"run_id"`
	Feeds           []FeedResult `json:"feeds"`
	RowsIn          int          `json:"rows_in"`
	RecomputedDates int          `json:"recomputed_dates"`
	DurationMS      int64        `json:"duration_ms"`
}

// Poller fetches active 24-hour FIRMS NRT CSV feeds and updates PostGIS aggregations.
type Poller struct {
	pool       *pgxpool.Pool
	mapKey     string
	baseURL    string
	sources    []string
	interval   time.Duration
	httpClient *http.Client
	logger     *slog.Logger
	mu         sync.Mutex
}

// Option customizes Poller construction cleanly for production and tests.
type Option func(*Poller)

// WithBaseURL overrides the FIRMS API origin (used in tests).
func WithBaseURL(u string) Option {
	return func(p *Poller) {
		if trimmed := strings.TrimRight(strings.TrimSpace(u), "/"); trimmed != "" {
			p.baseURL = trimmed
		}
	}
}

// WithHTTPClient injects a custom HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Poller) {
		if c != nil {
			p.httpClient = c
		}
	}
}

// WithLogger sets the structured logger for poll runs.
func WithLogger(l *slog.Logger) Option {
	return func(p *Poller) {
		if l != nil {
			p.logger = l
		}
	}
}

// NewPoller constructs an NRT polling coordinator.
func NewPoller(pool *pgxpool.Pool, mapKey string, interval time.Duration, opts ...Option) *Poller {
	if interval <= 0 {
		interval = 60 * time.Minute
	}
	p := &Poller{
		pool:       pool,
		mapKey:     strings.TrimSpace(mapKey),
		baseURL:    defaultBaseURL,
		sources:    append([]string(nil), DefaultNRTSources...),
		interval:   interval,
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
		logger:     slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Start runs the periodic NRT scheduler until ctx is canceled.
func (p *Poller) Start(ctx context.Context) {
	if p.mapKey == "" {
		p.logger.Info("NRT scheduler disabled because FIRMS_MAP_KEY is unset")
		return
	}

	p.logger.Info("NRT scheduler started", "interval", p.interval.String(), "sources", len(p.sources))
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("NRT scheduler stopping")
			return
		case <-ticker.C:
			rep, err := p.RunOnce(ctx)
			if err != nil {
				if errors.Is(err, ErrPollInProgress) {
					continue
				}
				p.logger.Error("scheduled NRT poll failed", "error", p.redact(err.Error()))
				continue
			}
			p.logger.Info("scheduled NRT poll complete",
				"run_id", rep.RunID,
				"rows_in", rep.RowsIn,
				"recomputed_dates", rep.RecomputedDates,
				"duration_ms", rep.DurationMS,
			)
		}
	}
}

// RunOnce executes one serialized poll pass across all four NRT feeds and triggers recompute/calibration when needed.
func (p *Poller) RunOnce(ctx context.Context) (*PollReport, error) {
	if p.mapKey == "" {
		return nil, errors.New("FIRMS_MAP_KEY is required")
	}
	if !p.mu.TryLock() {
		return nil, ErrPollInProgress
	}
	defer p.mu.Unlock()

	start := time.Now().UTC()

	lockConn, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire poll lock connection: %w", err)
	}
	defer lockConn.Release()

	var locked bool
	if err := lockConn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, advisoryLockKey).Scan(&locked); err != nil {
		return nil, fmt.Errorf("acquire poll advisory lock: %w", err)
	}
	if !locked {
		return nil, ErrPollInProgress
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = lockConn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, advisoryLockKey)
	}()

	var runID int64
	if err := p.pool.QueryRow(ctx, `
		INSERT INTO ingest_runs (kind, source, area, started_at, rows_in, status)
		VALUES ('nrt_poll', $1, 'world', $2, 0, 'running')
		RETURNING id
	`, strings.Join(p.sources, ","), start).Scan(&runID); err != nil {
		return nil, fmt.Errorf("create ingest_runs row: %w", err)
	}

	report := &PollReport{
		RunID: runID,
		Feeds: make([]FeedResult, 0, len(p.sources)),
	}

	var feedErrors []string
	for _, src := range p.sources {
		res := p.pollOneFeed(ctx, src)
		report.Feeds = append(report.Feeds, res)
		report.RowsIn += res.RowsInserted + res.RowsUpdated
		if res.Error != "" {
			feedErrors = append(feedErrors, fmt.Sprintf("%s: %s", src, res.Error))
		}
	}

	if report.RowsIn > 0 {
		recomp, err := aggregate.RecomputeDirty(ctx, p.pool)
		if err != nil {
			feedErrors = append(feedErrors, fmt.Sprintf("recompute: %s", p.redact(err.Error())))
		} else if recomp != nil {
			report.RecomputedDates = recomp.DatesProcessed
		}
		if _, err := aggregate.CalibrateAll(ctx, p.pool, aggregate.CalibrateOptions{ConfLevel: 2, LiveDays: 30}); err != nil {
			feedErrors = append(feedErrors, fmt.Sprintf("calibrate: %s", p.redact(err.Error())))
		}
	}

	report.DurationMS = time.Since(start).Milliseconds()

	status := "ok"
	var errText *string
	if len(feedErrors) > 0 {
		status = "error"
		joined := p.redact(strings.Join(feedErrors, "; "))
		errText = &joined
	}

	if _, err := p.pool.Exec(ctx, `
		UPDATE ingest_runs
		SET finished_at = now(), rows_in = $2, status = $3, error = $4
		WHERE id = $1
	`, runID, report.RowsIn, status, errText); err != nil {
		return report, fmt.Errorf("finalize ingest_runs %d: %w", runID, err)
	}

	if len(feedErrors) > 0 {
		return report, errors.New(*errText)
	}
	return report, nil
}

func (p *Poller) pollOneFeed(ctx context.Context, source string) FeedResult {
	res := FeedResult{Source: source}
	reqURL := fmt.Sprintf("%s/api/area/csv/%s/%s/world/1", p.baseURL, p.mapKey, source)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		res.Error = p.redact(err.Error())
		return res
	}
	req.Header.Set("User-Agent", "PyroSync/0.3")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		res.Error = p.redact(err.Error())
		return res
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		res.Error = p.redact(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodySnippet))))
		return res
	}

	tmp, err := os.CreateTemp("", "pyrosync-nrt-*.csv")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, maxResponseBytes)); err != nil {
		res.Error = p.redact(err.Error())
		return res
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		res.Error = err.Error()
		return res
	}

	firstLine, err := bufio.NewReader(tmp).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		res.Error = err.Error()
		return res
	}
	trimmedFirst := strings.TrimSpace(strings.TrimPrefix(firstLine, "\uFEFF"))
	if trimmedFirst == "" {
		res.Error = "empty response body"
		return res
	}
	if !strings.Contains(strings.ToLower(trimmedFirst), "latitude") {
		if len(trimmedFirst) > 160 {
			trimmedFirst = trimmedFirst[:160]
		}
		res.Error = p.redact(fmt.Sprintf("unexpected feed response: %s", trimmedFirst))
		return res
	}
	if err := tmp.Close(); err != nil {
		res.Error = err.Error()
		return res
	}

	rep, err := ingest.IngestFile(ctx, p.pool, tmpPath, ingest.FileOptions{AreaID: "world"})
	if rep != nil {
		res.RowsRead = rep.RowsRead
		res.RowsInserted = rep.RowsInserted
		res.RowsUpdated = rep.RowsUpdated
		res.SkippedDuplicate = rep.SkippedDuplicate
	}
	if err != nil {
		res.Error = p.redact(err.Error())
	}
	return res
}

func (p *Poller) redact(s string) string {
	if p.mapKey == "" {
		return s
	}
	return strings.ReplaceAll(s, p.mapKey, "[REDACTED]")
}
