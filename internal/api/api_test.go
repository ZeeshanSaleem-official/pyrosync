package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pyrosync/internal/aggregate"
	"pyrosync/internal/config"
	"pyrosync/internal/db"
	"pyrosync/internal/grid"
	"pyrosync/internal/ingest"
	"pyrosync/internal/sched"
)

func openAPITestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping database integration test in -short mode")
	}

	baseDSN := os.Getenv("PYROSYNC_TEST_DSN")
	if baseDSN == "" {
		baseDSN = "postgres://pyrosync:devpassword@localhost:5433/pyrosync?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Skipf("database unreachable: %v", err)
	}

	if _, err := adminPool.Exec(ctx, `DROP DATABASE IF EXISTS pyrosync_api_test WITH (FORCE)`); err != nil {
		adminPool.Close()
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `CREATE DATABASE pyrosync_api_test`); err != nil {
		adminPool.Close()
		t.Fatalf("create test database: %v", err)
	}
	adminPool.Close()

	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse test dsn: %v", err)
	}
	u.Path = "/pyrosync_api_test"
	testDSN := u.String()

	if err := db.Migrate(ctx, testDSN); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	pool, err := db.Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func writeTestCSV(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write csv %s: %v", name, err)
	}
	return p
}

func seedAPITestDataset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()

	// Historical baseline across distinct years (DOY 318 +/- 7 days) so
	// Pakistan qualifies for multi-era history and circular DOY z-scores.
	modis2010 := []string{
		"latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,confidence,version,bright_t31,frp,daynight,type",
		"31.500,74.300,318.0,1.0,1.0,2010-11-12,0800,A,82,6.1,294.0,24.0,D,0",
		"31.500,74.300,320.0,1.0,1.0,2010-11-14,0805,A,85,6.1,295.0,26.0,D,0",
		"31.500,74.300,317.0,1.0,1.0,2010-11-16,0810,T,80,6.1,293.0,22.0,D,0",
	}
	modis2015 := []string{
		"latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,confidence,version,bright_t31,frp,daynight,type",
		"31.500,74.300,319.0,1.0,1.0,2015-11-12,0800,A,84,6.1,294.0,25.0,D,0",
		"31.500,74.300,321.0,1.0,1.0,2015-11-14,0805,A,86,6.1,295.0,27.0,D,0",
		"31.500,74.300,316.0,1.0,1.0,2015-11-16,0810,T,79,6.1,293.0,21.0,D,0",
	}
	viirs2015 := []string{
		"latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,confidence,version,bright_ti5,frp,daynight,type",
		"31.502,74.302,336.0,0.4,0.4,2015-11-12,0810,N,n,1,295.0,23.5,D,0",
		"31.502,74.302,338.0,0.4,0.4,2015-11-14,0815,N,h,1,296.0,25.5,D,0",
		"31.502,74.302,334.0,0.4,0.4,2015-11-16,0820,N,n,1,294.0,20.5,D,0",
	}
	modis2020 := []string{
		"latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,confidence,version,bright_t31,frp,daynight,type",
		"31.500,74.300,320.0,1.0,1.0,2020-11-12,0800,A,85,6.1,294.0,26.0,D,0",
		"31.500,74.300,321.0,1.0,1.0,2020-11-14,0805,A,88,6.1,295.0,28.0,D,0",
		"31.500,74.300,318.0,1.0,1.0,2020-11-16,0810,T,80,6.1,293.0,23.0,D,0",
	}
	viirs2020 := []string{
		"latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,confidence,version,bright_ti5,frp,daynight,type",
		"31.502,74.302,336.0,0.4,0.4,2020-11-12,0815,N,n,1,295.0,25.0,D,0",
		"31.502,74.302,337.0,0.4,0.4,2020-11-14,0820,N20,h,2,296.0,27.0,D,0",
		"31.502,74.302,334.0,0.4,0.4,2020-11-16,0825,N,n,1,294.0,22.0,D,0",
	}
	modis2024 := []string{
		"latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,confidence,version,bright_t31,frp,daynight,type",
		"31.500,74.300,322.0,1.0,1.0,2024-11-12,0800,A,84,6.1,294.0,27.0,D,0",
		"31.500,74.300,320.0,1.0,1.0,2024-11-14,0805,A,85,6.1,295.0,24.5,D,0",
	}
	viirs2024 := []string{
		"latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,confidence,version,bright_ti5,frp,daynight,type",
		"31.502,74.302,336.0,0.4,0.4,2024-11-12,0810,N,h,2,295.0,26.0,D,0",
		"31.502,74.302,335.0,0.4,0.4,2024-11-14,0815,N20,h,2,296.0,23.5,D,0",
	}

	// Live 2026-11-13..2026-11-14 snapshot:
	// - Cell A (31.500, 74.300): intense daytime fire on 2026-11-14 (320 MW MODIS, 305 MW VIIRS) -> high robust_z!
	// - Cell B (31.100, 74.100): multiple weak VIIRS-only micro-fires (< 6.2 MW floor) to verify ladder compression & floor filtering!
	// - Nighttime fire on 2026-11-13 at 21:10Z (daynight='N').
	modisLive := []string{
		"latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,confidence,version,bright_t31,frp,daynight",
		"31.500,74.300,322.0,1.0,1.0,2026-11-13,2110,T,85,6.1NRT,290.0,30.0,N",
		"31.500,74.300,365.0,1.0,1.0,2026-11-14,0815,A,95,6.1NRT,301.0,320.0,D",
	}
	viirsLive := []string{
		"latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,confidence,version,bright_ti5,frp,daynight",
		"31.501,74.301,340.0,0.4,0.4,2026-11-13,2115,N,h,2.0NRT,291.0,14.5,N",
		"31.502,74.302,341.0,0.4,0.4,2026-11-13,2115,N,h,2.0NRT,291.0,14.5,N",
		"31.503,74.303,345.0,0.4,0.4,2026-11-13,2145,N20,h,2.0NRT,292.0,28.0,N",
		"31.501,74.301,367.0,0.4,0.4,2026-11-14,0820,N,h,2.0NRT,302.0,152.5,D",
		"31.502,74.302,367.0,0.4,0.4,2026-11-14,0820,N,h,2.0NRT,302.0,152.5,D",
		"31.503,74.303,366.0,0.4,0.4,2026-11-14,0850,N20,h,2.0NRT,301.0,300.0,D",
		"31.100,74.100,326.0,0.4,0.4,2026-11-14,0825,N,n,2.0NRT,292.0,2.1,D",
		"31.120,74.120,325.0,0.4,0.4,2026-11-14,0825,N,n,2.0NRT,292.0,1.9,D",
		"31.140,74.140,327.0,0.4,0.4,2026-11-14,0826,N20,n,2.0NRT,292.0,2.4,D",
		"31.160,74.160,324.0,0.4,0.4,2026-11-14,0826,N20,n,2.0NRT,292.0,1.8,D",
	}

	files := []struct {
		name  string
		area  string
		lines []string
	}{
		{"modis_2010_Pakistan.csv", "pakistan-indus", modis2010},
		{"modis_2015_Pakistan.csv", "pakistan-indus", modis2015},
		{"viirs_2015_Pakistan.csv", "pakistan-indus", viirs2015},
		{"modis_2020_Pakistan.csv", "pakistan-indus", modis2020},
		{"viirs_2020_Pakistan.csv", "pakistan-indus", viirs2020},
		{"modis_2024_Pakistan.csv", "pakistan-indus", modis2024},
		{"viirs_2024_Pakistan.csv", "pakistan-indus", viirs2024},
		{"MODIS_C6_1_Global_48h.csv", "world", modisLive},
		{"J1_VIIRS_C2_Global_48h.csv", "world", viirsLive},
	}

	for _, f := range files {
		p := writeTestCSV(t, tmp, f.name, f.lines)
		if _, err := ingest.IngestFile(ctx, pool, p, ingest.FileOptions{AreaID: f.area}); err != nil {
			t.Fatalf("ingest %s: %v", f.name, err)
		}
	}

	if _, err := aggregate.RecomputeDirty(ctx, pool); err != nil {
		t.Fatalf("recompute dirty: %v", err)
	}
	if _, err := aggregate.CalibrateAll(ctx, pool, aggregate.CalibrateOptions{ConfLevel: 2, LiveDays: 30}); err != nil {
		t.Fatalf("calibrate all: %v", err)
	}
}

func mustGet(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestAPIEndpointsAndValidation(t *testing.T) {
	pool := openAPITestDB(t)
	seedAPITestDataset(t, pool)

	cfg := config.Config{
		HTTPAddr:          ":0",
		AllowedOrigins:    []string{"http://localhost:3000"},
		IngestIntervalMin: 60,
	}
	srv := New(pool, cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := srv.Router()

	t.Run("health", func(t *testing.T) {
		rec := mustGet(t, h, "/api/v1/health")
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out healthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Status != "ok" || out.Version != apiVersion {
			t.Fatalf("unexpected health response: %+v", out)
		}
	})

	t.Run("ready", func(t *testing.T) {
		rec := mustGet(t, h, "/api/v1/ready")
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out readyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Status != "ready" || !out.Seeded || out.Regions < 6 || out.RawRows == 0 {
			t.Fatalf("unexpected ready response: %+v", out)
		}
	})

	t.Run("meta_and_regions", func(t *testing.T) {
		rec := mustGet(t, h, "/api/v1/meta")
		if rec.Code != http.StatusOK {
			t.Fatalf("meta status=%d body=%s", rec.Code, rec.Body.String())
		}
		var meta metaResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
			t.Fatalf("decode meta: %v", err)
		}
		if meta.FloorMW != 6.2 || meta.Grid["a1_deg"] != 0.01 || meta.Grid["a10_deg"] != 0.1 || meta.TotalDetections == 0 {
			t.Fatalf("unexpected meta: %+v", meta)
		}
		if meta.LiveMaxDate == nil || *meta.LiveMaxDate != "2026-11-14" {
			t.Fatalf("unexpected live_max_date: %+v", meta.LiveMaxDate)
		}

		regRec := mustGet(t, h, "/api/v1/regions")
		if regRec.Code != http.StatusOK {
			t.Fatalf("regions status=%d body=%s", regRec.Code, regRec.Body.String())
		}
		var regPayload struct {
			Regions []regionDTO `json:"regions"`
		}
		if err := json.Unmarshal(regRec.Body.Bytes(), &regPayload); err != nil {
			t.Fatalf("decode regions: %v", err)
		}
		if len(regPayload.Regions) < 6 {
			t.Fatalf("expected >= 6 regions, got %d", len(regPayload.Regions))
		}
		var foundPak bool
		for _, rg := range regPayload.Regions {
			if rg.ID == "pakistan-indus" {
				foundPak = true
				if !rg.HasHistory || rg.HistoryFrom == nil || rg.HistoryTo == nil {
					t.Fatalf("expected pakistan-indus has_history=true with bounds, got %+v", rg)
				}
			}
		}
		if !foundPak {
			t.Fatalf("pakistan-indus region not found")
		}
	})

	t.Run("summary_proof_ladder", func(t *testing.T) {
		rec := mustGet(t, h, "/api/v1/summary?region=pakistan-indus&from=2026-11-13&to=2026-11-14&conf=2")
		if rec.Code != http.StatusOK {
			t.Fatalf("summary status=%d body=%s", rec.Code, rec.Body.String())
		}
		var sum summaryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
			t.Fatalf("decode summary: %v", err)
		}
		if len(sum.Ladder) != 4 {
			t.Fatalf("expected 4 ladder steps, got %d", len(sum.Ladder))
		}
		// Raw Pixels (10/2=5.0x) > Equal-Area Cells (5/1=5.0x or 6/2) > Floor Cells (1/1=1.0x) ~ FRP Ratio
		if !(sum.Ladder[0].Ratio > sum.Ladder[2].Ratio) {
			t.Fatalf("expected step 1 ratio (%.2f) > step 3 ratio (%.2f)", sum.Ladder[0].Ratio, sum.Ladder[2].Ratio)
		}
		if !(sum.Ladder[1].Ratio >= sum.Ladder[2].Ratio) {
			t.Fatalf("expected step 2 ratio (%.2f) >= step 3 ratio (%.2f)", sum.Ladder[1].Ratio, sum.Ladder[2].Ratio)
		}
		if sum.Ladder[3].Ratio < 0.8 || sum.Ladder[3].Ratio > 1.25 {
			t.Fatalf("expected step 4 FRP ratio near 1.0x, got %.2f", sum.Ladder[3].Ratio)
		}
		if sum.Agreement.OverallPct <= 0 {
			t.Fatalf("expected positive a10 agreement, got %+v", sum.Agreement)
		}
		if len(sum.Daily) != 2 {
			t.Fatalf("expected 2 daily rows, got %d", len(sum.Daily))
		}
	})

	t.Run("timeline_day_and_6h", func(t *testing.T) {
		dayRec := mustGet(t, h, "/api/v1/timeline?region=pakistan-indus&from=2026-11-13&to=2026-11-14&bucket=day")
		if dayRec.Code != http.StatusOK {
			t.Fatalf("day timeline status=%d body=%s", dayRec.Code, dayRec.Body.String())
		}
		var dayPayload struct {
			Bucket string            `json:"bucket"`
			Series []dailySummaryDTO `json:"series"`
		}
		if err := json.Unmarshal(dayRec.Body.Bytes(), &dayPayload); err != nil {
			t.Fatalf("decode day timeline: %v", err)
		}
		if dayPayload.Bucket != "day" || len(dayPayload.Series) != 2 {
			t.Fatalf("unexpected day timeline: %+v", dayPayload)
		}

		sixRec := mustGet(t, h, "/api/v1/timeline?region=pakistan-indus&from=2026-11-13&to=2026-11-14&bucket=6h")
		if sixRec.Code != http.StatusOK {
			t.Fatalf("6h timeline status=%d body=%s", sixRec.Code, sixRec.Body.String())
		}
		var sixPayload struct {
			Bucket string `json:"bucket"`
			Series []struct {
				BucketTS      string  `json:"bucket_ts"`
				ModisPx       int     `json:"modis_px"`
				ViirsPx       int     `json:"viirs_px"`
				HarmonizedFRP float64 `json:"harmonized_frp"`
			} `json:"series"`
		}
		if err := json.Unmarshal(sixRec.Body.Bytes(), &sixPayload); err != nil {
			t.Fatalf("decode 6h timeline: %v", err)
		}
		if sixPayload.Bucket != "6h" || len(sixPayload.Series) < 2 {
			t.Fatalf("expected >= 2 6h buckets (21:00Z + 06:00Z), got %+v", sixPayload)
		}
		if _, err := time.Parse(time.RFC3339, sixPayload.Series[0].BucketTS); err != nil {
			t.Fatalf("invalid RFC3339 bucket_ts %q: %v", sixPayload.Series[0].BucketTS, err)
		}
	})

	t.Run("hourly_buckets", func(t *testing.T) {
		rec := mustGet(t, h, "/api/v1/hourly?region=pakistan-indus&from=2026-11-13&to=2026-11-14")
		if rec.Code != http.StatusOK {
			t.Fatalf("hourly status=%d body=%s", rec.Code, rec.Body.String())
		}
		var payload struct {
			Hours []struct {
				Hour       int `json:"hour"`
				ModisDayPx int `json:"modis_day_px"`
				ViirsDayPx int `json:"viirs_day_px"`
				ModisNight int `json:"modis_night_px"`
				ViirsNight int `json:"viirs_night_px"`
			} `json:"hours"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode hourly: %v", err)
		}
		if len(payload.Hours) != 24 {
			t.Fatalf("expected 24 hours, got %d", len(payload.Hours))
		}
		if payload.Hours[8].ModisDayPx == 0 || payload.Hours[8].ViirsDayPx == 0 {
			t.Fatalf("expected daytime counts at hour 08Z, got %+v", payload.Hours[8])
		}
		if payload.Hours[21].ModisNight == 0 || payload.Hours[21].ViirsNight == 0 {
			t.Fatalf("expected nighttime counts at hour 21Z, got %+v", payload.Hours[21])
		}
	})

	t.Run("calendar_history_and_live", func(t *testing.T) {
		histRec := mustGet(t, h, "/api/v1/calendar?region=pakistan-indus&mode=history")
		if histRec.Code != http.StatusOK {
			t.Fatalf("calendar history status=%d body=%s", histRec.Code, histRec.Body.String())
		}
		var cal struct {
			Mode        string `json:"mode"`
			HasHistory  bool   `json:"has_history"`
			EraBrackets []struct {
				Era string `json:"era"`
			} `json:"era_brackets"`
			Days []dailySummaryDTO `json:"days"`
		}
		if err := json.Unmarshal(histRec.Body.Bytes(), &cal); err != nil {
			t.Fatalf("decode calendar: %v", err)
		}
		if cal.Mode != "history" || !cal.HasHistory || len(cal.EraBrackets) < 3 {
			t.Fatalf("expected >= 3 era brackets (MODIS Only, MODIS+S-NPP, Constellation), got %+v", cal.EraBrackets)
		}

		var foundAnomaly bool
		for _, d := range cal.Days {
			if d.Date == "2026-11-14" {
				if d.RobustZ == nil || *d.RobustZ <= 2.0 {
					t.Fatalf("expected strong positive robust_z (>2.0) on spike day 2026-11-14, got %v", d.RobustZ)
				}
				foundAnomaly = true
			}
		}
		if !foundAnomaly {
			t.Fatalf("expected 2026-11-14 in history calendar days")
		}

		liveRec := mustGet(t, h, "/api/v1/calendar?region=pakistan-indus&mode=live")
		if liveRec.Code != http.StatusOK {
			t.Fatalf("calendar live status=%d body=%s", liveRec.Code, liveRec.Body.String())
		}
	})

	t.Run("cell_inspector", func(t *testing.T) {
		keptCell := grid.CellID(31.500, 74.300, grid.ResA1)
		rec := mustGet(t, h, fmt.Sprintf("/api/v1/cell/%d?from=2026-11-13&to=2026-11-14", keptCell))
		if rec.Code != http.StatusOK {
			t.Fatalf("cell status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out struct {
			CellA1     int64   `json:"cell_a1"`
			AreaKm2    float64 `json:"area_km2"`
			FloorMW    float64 `json:"floor_mw"`
			Detections []struct {
				Sensor        string  `json:"sensor"`
				FRPMW         float64 `json:"frp_mw"`
				OverpassFRPMW float64 `json:"overpass_frp_mw"`
				Kept          bool    `json:"kept"`
			} `json:"detections"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode cell: %v", err)
		}
		if out.CellA1 != keptCell || out.AreaKm2 <= 0 || out.FloorMW != 6.2 || len(out.Detections) == 0 {
			t.Fatalf("unexpected cell inspector response: %+v", out)
		}
		for _, d := range out.Detections {
			if !d.Kept {
				t.Fatalf("expected all intense-cell detections kept=true, got %+v", d)
			}
		}

		subCell := grid.CellID(31.100, 74.100, grid.ResA1)
		subRec := mustGet(t, h, fmt.Sprintf("/api/v1/cell/%d?from=2026-11-14&to=2026-11-14", subCell))
		if subRec.Code != http.StatusOK {
			t.Fatalf("sub cell status=%d body=%s", subRec.Code, subRec.Body.String())
		}
		if err := json.Unmarshal(subRec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode sub cell: %v", err)
		}
		if len(out.Detections) != 1 || out.Detections[0].Kept {
			t.Fatalf("expected 1 dropped detection (kept=false), got %+v", out.Detections)
		}
	})

	t.Run("validation_and_cors", func(t *testing.T) {
		badPaths := []string{
			"/api/v1/summary?region=mars",
			"/api/v1/summary?from=not-a-date",
			"/api/v1/summary?from=2026-11-15&to=2026-11-14",
			"/api/v1/summary?bbox=bad",
			"/api/v1/summary?conf=9",
			"/api/v1/timeline?bucket=1h",
			"/api/v1/calendar?mode=yearly",
			"/api/v1/cell/notanid",
		}
		for _, p := range badPaths {
			rec := mustGet(t, h, p)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("GET %s: expected 400, got %d (%s)", p, rec.Code, rec.Body.String())
			}
		}

		// Allowed CORS origin preflight.
		optReq := httptest.NewRequest(http.MethodOptions, "/api/v1/meta", nil)
		optReq.Header.Set("Origin", "http://localhost:3000")
		optRec := httptest.NewRecorder()
		h.ServeHTTP(optRec, optReq)
		if optRec.Code != http.StatusNoContent {
			t.Fatalf("OPTIONS status=%d", optRec.Code)
		}
		if got := optRec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
			t.Fatalf("unexpected Allow-Origin %q", got)
		}

		// Disallowed origin omits Access-Control-Allow-Origin.
		badReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		badReq.Header.Set("Origin", "http://evil.example")
		badRec := httptest.NewRecorder()
		h.ServeHTTP(badRec, badReq)
		if got := badRec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("expected empty Allow-Origin for disallowed origin, got %q", got)
		}
	})
}

func TestTileEndpointsMVT(t *testing.T) {
	pool := openAPITestDB(t)
	seedAPITestDataset(t, pool)

	cfg := config.Config{HTTPAddr: ":0", IngestIntervalMin: 60}
	srv := New(pool, cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := srv.Router()

	const ctMVT = "application/vnd.mapbox-vector-tile"

	cases := []struct {
		name      string
		path      string
		wantCode  int
		wantCT    string
		wantBytes string // "nonempty" or "empty"
	}{
		{
			name:      "cells10_global_z0",
			path:      "/api/v1/tiles/cells10/0/0/0.mvt?from=2026-11-13&to=2026-11-14",
			wantCode:  http.StatusOK,
			wantCT:    ctMVT,
			wantBytes: "nonempty",
		},
		{
			name:      "cells_lahore_z7_harmonized",
			path:      "/api/v1/tiles/cells/7/90/52.mvt?from=2026-11-13&to=2026-11-14",
			wantCode:  http.StatusOK,
			wantCT:    ctMVT,
			wantBytes: "nonempty",
		},
		{
			name:      "cells_subthreshold_z12_harmonized_empty",
			path:      "/api/v1/tiles/cells/12/2891/1675.mvt?from=2026-11-14&to=2026-11-14&mode=harmonized",
			wantCode:  http.StatusOK,
			wantCT:    ctMVT,
			wantBytes: "empty",
		},
		{
			name:      "cells_subthreshold_z12_raw_nonempty",
			path:      "/api/v1/tiles/cells/12/2891/1675.mvt?from=2026-11-14&to=2026-11-14&mode=raw",
			wantCode:  http.StatusOK,
			wantCT:    ctMVT,
			wantBytes: "nonempty",
		},
		{
			name:      "det_lahore_z12_nonempty",
			path:      "/api/v1/tiles/det/12/2893/1670.mvt?from=2026-11-13&to=2026-11-14",
			wantCode:  http.StatusOK,
			wantCT:    ctMVT,
			wantBytes: "nonempty",
		},
		{
			name:      "ocean_tile_empty",
			path:      "/api/v1/tiles/cells/7/0/64.mvt?from=2026-11-13&to=2026-11-14",
			wantCode:  http.StatusOK,
			wantCT:    ctMVT,
			wantBytes: "empty",
		},
		{
			name:      "cells_below_minzoom",
			path:      "/api/v1/tiles/cells/2/0/0.mvt?from=2026-11-13&to=2026-11-14",
			wantCode:  http.StatusOK,
			wantCT:    ctMVT,
			wantBytes: "empty",
		},
		{
			name:      "det_below_minzoom",
			path:      "/api/v1/tiles/det/7/90/52.mvt?from=2026-11-13&to=2026-11-14",
			wantCode:  http.StatusOK,
			wantCT:    ctMVT,
			wantBytes: "empty",
		},
		{
			name:     "invalid_z",
			path:     "/api/v1/tiles/cells10/25/0/0.mvt",
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "out_of_range_x",
			path:     "/api/v1/tiles/cells10/2/999/0.mvt",
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid_mode",
			path:     "/api/v1/tiles/cells10/0/0/0.mvt?mode=bogus",
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := mustGet(t, h, tc.path)
			if rec.Code != tc.wantCode {
				t.Fatalf("status=%d (want %d) body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCT != "" && rec.Header().Get("Content-Type") != tc.wantCT {
				t.Fatalf("Content-Type=%q, want %q", rec.Header().Get("Content-Type"), tc.wantCT)
			}
			switch tc.wantBytes {
			case "nonempty":
				if rec.Body.Len() == 0 {
					t.Fatalf("expected non-empty MVT payload")
				}
			case "empty":
				if rec.Body.Len() != 0 {
					t.Fatalf("expected empty MVT payload, got %d bytes", rec.Body.Len())
				}
			}
		})
	}
}

func TestAdminIngestEndpoint(t *testing.T) {
	pool := openAPITestDB(t)

	// 1. Disabled when ADMIN_TOKEN is unset.
	disabledSrv := New(pool, config.Config{HTTPAddr: ":0"}, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ingest", nil)
	rec := httptest.NewRecorder()
	disabledSrv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when ADMIN_TOKEN unset, got %d", rec.Code)
	}

	// 2. Mock FIRMS NRT API for authorized trigger + rate limit check.
	const mapKey = "TEST_MAP_KEY"
	firmsMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "MODIS_NRT"):
			fmt.Fprintln(w, "latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,confidence,version,bright_t31,frp,daynight")
			fmt.Fprintln(w, "31.500,74.300,320.0,1.0,1.0,2026-11-14,0815,T,80,6.1NRT,295.0,22.0,D")
		default:
			fmt.Fprintln(w, "latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,confidence,version,bright_ti5,frp,daynight")
		}
	}))
	defer firmsMock.Close()

	cfg := config.Config{
		HTTPAddr:    ":0",
		AdminToken:  "admin-secret-token",
		FirmsMapKey: mapKey,
	}
	poller := sched.NewPoller(pool, mapKey, time.Hour, sched.WithBaseURL(firmsMock.URL))
	srv := New(pool, cfg, poller, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := srv.Router()

	// Missing bearer token -> 401.
	unauthReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ingest", nil)
	unauthRec := httptest.NewRecorder()
	h.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 missing token, got %d", unauthRec.Code)
	}

	// Wrong bearer token -> 401.
	badAuthReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ingest", nil)
	badAuthReq.Header.Set("Authorization", "Bearer wrong-token")
	badAuthRec := httptest.NewRecorder()
	h.ServeHTTP(badAuthRec, badAuthReq)
	if badAuthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 wrong token, got %d", badAuthRec.Code)
	}

	// Valid bearer token -> 200 OK with PollReport.
	okReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ingest", nil)
	okReq.Header.Set("Authorization", "Bearer admin-secret-token")
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", okRec.Code, okRec.Body.String())
	}
	var rep sched.PollReport
	if err := json.Unmarshal(okRec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode poll report: %v", err)
	}
	if rep.RowsIn != 1 || rep.RunID == 0 {
		t.Fatalf("unexpected PollReport: %+v", rep)
	}

	// Immediate retry -> 429 Too Many Requests.
	rateReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ingest", nil)
	rateReq.Header.Set("Authorization", "Bearer admin-secret-token")
	rateRec := httptest.NewRecorder()
	h.ServeHTTP(rateRec, rateReq)
	if rateRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 rate limited, got %d: %s", rateRec.Code, rateRec.Body.String())
	}
}
