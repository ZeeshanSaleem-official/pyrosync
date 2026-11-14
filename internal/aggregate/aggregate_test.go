package aggregate

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pyrosync/internal/db"
	"pyrosync/internal/grid"
	"pyrosync/internal/ingest"
)

func openAggTestDB(t *testing.T) *pgxpool.Pool {
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

	if _, err := adminPool.Exec(ctx, `DROP DATABASE IF EXISTS pyrosync_agg_test WITH (FORCE)`); err != nil {
		adminPool.Close()
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `CREATE DATABASE pyrosync_agg_test`); err != nil {
		adminPool.Close()
		t.Fatalf("create test database: %v", err)
	}
	adminPool.Close()

	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse test dsn: %v", err)
	}
	u.Path = "/pyrosync_agg_test"
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

func assertWithinPct(t *testing.T, label string, got, want, maxPct float64) {
	t.Helper()
	if want == 0 {
		if got != 0 {
			t.Fatalf("%s: got %.4f, want 0", label, got)
		}
		return
	}
	pct := math.Abs(got-want) / math.Abs(want) * 100.0
	if pct > maxPct {
		t.Fatalf("%s: got %.4f, want %.4f (diff %.3f%% > %.2f%%)", label, got, want, pct, maxPct)
	}
}

func TestGridCellIDSQLParity(t *testing.T) {
	pool := openAggTestDB(t)
	ctx := context.Background()

	rng := rand.New(rand.NewSource(99))
	samples := [][2]float64{
		{0.0, 0.0},
		{89.99, 179.99},
		{-89.99, -179.99},
		{31.5204, 74.3587},
		{-15.1234, -55.4321},
	}
	for i := 0; i < 200; i++ {
		lat := -89.5 + rng.Float64()*179.0
		lon := -179.5 + rng.Float64()*359.0
		samples = append(samples, [2]float64{lat, lon})
	}

	for _, res := range []float64{grid.ResA1, grid.ResA10} {
		for _, pt := range samples {
			want := grid.CellID(pt[0], pt[1], res)
			var got int64
			if err := pool.QueryRow(ctx, `SELECT grid_cell_id($1, $2, $3)`, pt[0], pt[1], res).Scan(&got); err != nil {
				t.Fatalf("query grid_cell_id: %v", err)
			}
			if got != want {
				t.Fatalf("grid_cell_id(%.5f, %.5f, %.2f) = %d, want %d", pt[0], pt[1], res, got, want)
			}
		}
	}
}

func TestOverpassFloorAndZeroFireArchiveDays(t *testing.T) {
	pool := openAggTestDB(t)
	ctx := context.Background()

	tmp := t.TempDir()
	modisCSV := filepath.Join(tmp, "modis_2010_Pakistan.csv")
	modisContent := "" +
		"latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,instrument,confidence,version,bright_t31,frp,daynight,type\n" +
		"30.105,70.205,330.0,1.1,1.1,2010-05-10,0615,Terra,MODIS,85,6.03,295.0,20.0,D,0\n"
	if err := os.WriteFile(modisCSV, []byte(modisContent), 0o600); err != nil {
		t.Fatal(err)
	}

	viirsCSV := filepath.Join(tmp, "viirs-jpss1_2018_Pakistan.csv")
	viirsContent := "" +
		"latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,instrument,confidence,version,bright_ti5,frp,daynight,type\n" +
		// Day overpass below 6.2 MW floor
		"30.105,70.205,330.0,0.4,0.4,2018-05-10,0815,N20,VIIRS,n,2,295.0,3.0,D,0\n" +
		// Night overpass cluster summing to 6.5 MW >= 6.2 MW floor in the same cell
		"30.105,70.205,332.0,0.4,0.4,2018-05-10,2115,N20,VIIRS,h,2,296.0,3.0,N,0\n" +
		"30.106,70.206,333.0,0.4,0.4,2018-05-10,2115,N20,VIIRS,h,2,296.0,3.5,N,0\n"
	if err := os.WriteFile(viirsCSV, []byte(viirsContent), 0o600); err != nil {
		t.Fatal(err)
	}

	modis2018CSV := filepath.Join(tmp, "modis_2018_Pakistan.csv")
	modis2018Content := "" +
		"latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,instrument,confidence,version,bright_t31,frp,daynight,type\n" +
		"30.105,70.205,331.0,1.0,1.0,2018-05-10,0630,Aqua,MODIS,90,6.03,294.0,19.5,D,0\n"
	if err := os.WriteFile(modis2018CSV, []byte(modis2018Content), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{modisCSV, viirsCSV, modis2018CSV} {
		if _, err := ingest.IngestFile(ctx, pool, p, ingest.FileOptions{}); err != nil {
			t.Fatalf("ingest %s: %v", p, err)
		}
	}

	rep, err := RecomputeDirty(ctx, pool)
	if err != nil {
		t.Fatalf("RecomputeDirty: %v", err)
	}
	if rep.DatesProcessed < 730 {
		t.Fatalf("expected full 2010+2018 coverage dates processed, got %d", rep.DatesProcessed)
	}

	var dirtyCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM aggregate_dirty`).Scan(&dirtyCount); err != nil {
		t.Fatal(err)
	}
	if dirtyCount != 0 {
		t.Fatalf("expected aggregate_dirty drained, found %d", dirtyCount)
	}

	// Verify 2018-05-10 cell_day_a1 at conf_level = 2:
	// MODIS pass = 19.5 MW, VIIRS all passes = (3.0 + 6.5)/2 = 4.75 MW, VIIRS kept = 6.5 MW, harmonized = (19.5 + 6.5)/2 = 13.0 MW.
	cell := grid.CellID(30.105, 70.205, grid.ResA1)
	var mPx, vPx int
	var nOvM, nOvV int16
	var mFRP, vAllFRP, vMeqFRP, hFRP float64
	err = pool.QueryRow(ctx, `
		SELECT modis_px, viirs_px, n_ov_modis, n_ov_viirs_meq,
		       modis_frp::double precision, viirs_frp_all::double precision,
		       viirs_frp_meq::double precision, harmonized_frp::double precision
		FROM cell_day_a1
		WHERE acq_date = '2018-05-10' AND conf_level = 2 AND cell_a1 = $1
	`, cell).Scan(&mPx, &vPx, &nOvM, &nOvV, &mFRP, &vAllFRP, &vMeqFRP, &hFRP)
	if err != nil {
		t.Fatalf("query cell_day_a1: %v", err)
	}
	if mPx != 1 || vPx != 3 || nOvM != 1 || nOvV != 1 {
		t.Fatalf("unexpected counts: mPx=%d vPx=%d nOvM=%d nOvV=%d", mPx, vPx, nOvM, nOvV)
	}
	if math.Abs(mFRP-19.5) > 1e-4 || math.Abs(vAllFRP-4.75) > 1e-4 || math.Abs(vMeqFRP-6.5) > 1e-4 || math.Abs(hFRP-13.0) > 1e-4 {
		t.Fatalf("unexpected FRP rollup: m=%.3f vAll=%.3f vMeq=%.3f h=%.3f", mFRP, vAllFRP, vMeqFRP, hFRP)
	}

	// Zero-fire covered day in 2010 Pakistan: m_frp = 0, v_meq_frp = NULL.
	var zModis *float64
	var zViirs *float64
	err = pool.QueryRow(ctx, `
		SELECT m_frp::double precision, v_meq_frp::double precision
		FROM region_day
		WHERE region_id = 'pakistan-indus' AND conf_level = 2 AND acq_date = '2010-01-15'
	`).Scan(&zModis, &zViirs)
	if err != nil {
		t.Fatalf("query zero-fire 2010-01-15 row: %v", err)
	}
	if zModis == nil || *zModis != 0 || zViirs != nil {
		t.Fatalf("unexpected zero-fire MODIS-only coverage values: m=%v v=%v", zModis, zViirs)
	}

	// Non-covered region (global / california) must not get rows from Pakistan SP archives.
	var leaked int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM region_day WHERE region_id IN ('global', 'california')
	`).Scan(&leaked)
	if err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("expected 0 rows for uncovered regions, found %d", leaked)
	}
}

func TestGolden48hValues(t *testing.T) {
	modisPath := filepath.Join("..", "..", "data", "input", "MODIS_C6_1_Global_48h.csv")
	viirsPath := filepath.Join("..", "..", "data", "input", "J2_VIIRS_C2_Global_48h.csv")
	if _, err := os.Stat(modisPath); err != nil {
		t.Skipf("48h MODIS CSV missing (%v)", err)
	}
	if _, err := os.Stat(viirsPath); err != nil {
		t.Skipf("48h VIIRS CSV missing (%v)", err)
	}

	pool := openAggTestDB(t)
	ctx := context.Background()

	for _, p := range []string{modisPath, viirsPath} {
		if _, err := ingest.IngestFile(ctx, pool, p, ingest.FileOptions{AreaID: "world"}); err != nil {
			t.Fatalf("ingest %s: %v", p, err)
		}
	}

	rep, err := RecomputeDirty(ctx, pool)
	if err != nil {
		t.Fatalf("RecomputeDirty: %v", err)
	}
	if rep.DatesProcessed != 3 {
		t.Fatalf("DatesProcessed = %d, want 3", rep.DatesProcessed)
	}

	models, err := CalibrateAll(ctx, pool, CalibrateOptions{ConfLevel: 2, LiveDays: 30})
	if err != nil {
		t.Fatalf("CalibrateAll: %v", err)
	}

	var foundGlobalLive bool
	for _, m := range models {
		if m.RegionID == "global" && m.Scope == "live" {
			foundGlobalLive = true
			if m.NDays != 2 || m.Reliable || m.KApplied != 1.0 {
				t.Fatalf("unexpected global/live calibration: %+v", m)
			}
		}
	}
	if !foundGlobalLive {
		t.Fatalf("missing global/live calibration model")
	}

	var mPx, vPx, cellsM, cellsVAll, cellsVMeq, cellsUnion int
	var sumMFRP, sumVMeqFRP, sumHarmFRP float64
	err = pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(modis_px), 0),
			COALESCE(SUM(viirs_px), 0),
			COUNT(*) FILTER (WHERE modis_frp IS NOT NULL),
			COUNT(*) FILTER (WHERE viirs_frp_all IS NOT NULL),
			COUNT(*) FILTER (WHERE viirs_frp_meq IS NOT NULL),
			COUNT(*) FILTER (WHERE harmonized_frp IS NOT NULL),
			COALESCE(SUM(modis_frp), 0)::double precision,
			COALESCE(SUM(viirs_frp_meq), 0)::double precision,
			COALESCE(SUM(harmonized_frp), 0)::double precision
		FROM cell_day_a1
		WHERE conf_level = 2
	`).Scan(&mPx, &vPx, &cellsM, &cellsVAll, &cellsVMeq, &cellsUnion, &sumMFRP, &sumVMeqFRP, &sumHarmFRP)
	if err != nil {
		t.Fatalf("query golden totals: %v", err)
	}

	if mPx != 18306 || vPx != 87068 {
		t.Fatalf("conf>=2 pixels = (%d, %d), want (18306, 87068)", mPx, vPx)
	}
	assertWithinPct(t, "Step 1 pixel ratio", float64(vPx)/float64(mPx), 4.76, 1.5)
	assertWithinPct(t, "A1 MODIS cell-days", float64(cellsM), 16895, 1.5)
	assertWithinPct(t, "A1 VIIRS all cell-days", float64(cellsVAll), 55510, 1.5)
	assertWithinPct(t, "Step 2 cell ratio", float64(cellsVAll)/float64(cellsM), 3.29, 1.5)
	assertWithinPct(t, "A1 VIIRS >= 6.2 MW cell-days", float64(cellsVMeq), 29715, 1.5)
	assertWithinPct(t, "Step 3 aligned cell ratio", float64(cellsVMeq)/float64(cellsM), 1.76, 1.5)
	assertWithinPct(t, "MODIS FRP MW", sumMFRP, 550032, 1.5)
	assertWithinPct(t, "VIIRS >= 6.2 MW FRP", sumVMeqFRP, 765111, 1.5)
	assertWithinPct(t, "Step 4 FRP ratio", sumVMeqFRP/sumMFRP, 1.39, 1.5)
	assertWithinPct(t, "Union A1 cell-days", float64(cellsUnion), 43636, 1.5)
	assertWithinPct(t, "Union harmonized FRP", sumHarmFRP, 1214266, 1.5)

	if got := fmt.Sprintf("%.2fx -> %.2fx -> %.2fx -> %.2fx",
		float64(vPx)/float64(mPx),
		float64(cellsVAll)/float64(cellsM),
		float64(cellsVMeq)/float64(cellsM),
		sumVMeqFRP/sumMFRP,
	); got != "4.76x -> 3.29x -> 1.76x -> 1.39x" {
		t.Fatalf("proof ladder = %s, want 4.76x -> 3.29x -> 1.76x -> 1.39x", got)
	}

	// Verify daily breakdown and partial-day flag in region_day.
	wantDays := map[string]struct {
		cells   int
		frp     float64
		partial bool
	}{
		"2026-11-12": {cells: 21754, frp: 608878, partial: false},
		"2026-11-13": {cells: 20078, frp: 551149, partial: false},
		"2026-11-14": {cells: 1804, frp: 54240, partial: true},
	}

	for dayStr, want := range wantDays {
		var cellsUnionDay int
		var harmSumDay float64
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FILTER (WHERE harmonized_frp IS NOT NULL),
			       COALESCE(SUM(harmonized_frp), 0)::double precision
			FROM cell_day_a1
			WHERE acq_date = $1::date AND conf_level = 2
		`, dayStr).Scan(&cellsUnionDay, &harmSumDay); err != nil {
			t.Fatalf("query day %s: %v", dayStr, err)
		}
		assertWithinPct(t, dayStr+" cells_union", float64(cellsUnionDay), float64(want.cells), 1.5)
		assertWithinPct(t, dayStr+" harmonized_frp", harmSumDay, want.frp, 1.5)

		var regCells int
		var isPartial bool
		if err := pool.QueryRow(ctx, `
			SELECT cells_union, is_partial
			FROM region_day
			WHERE region_id = 'global' AND conf_level = 2 AND acq_date = $1::date
		`, dayStr).Scan(&regCells, &isPartial); err != nil {
			t.Fatalf("query region_day %s: %v", dayStr, err)
		}
		if regCells != cellsUnionDay {
			t.Fatalf("region_day %s cells_union = %d, cell_day_a1 = %d", dayStr, regCells, cellsUnionDay)
		}
		if isPartial != want.partial {
			t.Fatalf("region_day %s is_partial = %v, want %v", dayStr, isPartial, want.partial)
		}
	}

	// Verify regional A10 cross-sensor co-detection (~55.1% overall, ~57.3% full days).
	var mA10, bothA10, mA10Full, bothA10Full int
	err = pool.QueryRow(ctx, `
		WITH a10 AS (
			SELECT
				acq_date,
				cell_a10,
				BOOL_OR(modis_px > 0) AS has_m,
				BOOL_OR(viirs_px > 0) AS has_v
			FROM cell_day_a10
			WHERE conf_level = 2
			GROUP BY acq_date, cell_a10
		)
		SELECT
			COUNT(*) FILTER (WHERE has_m),
			COUNT(*) FILTER (WHERE has_m AND has_v),
			COUNT(*) FILTER (WHERE has_m AND acq_date < '2026-11-14'),
			COUNT(*) FILTER (WHERE has_m AND has_v AND acq_date < '2026-11-14')
		FROM a10
	`).Scan(&mA10, &bothA10, &mA10Full, &bothA10Full)
	if err != nil {
		t.Fatalf("query a10 agreement: %v", err)
	}
	if mA10 == 0 || mA10Full == 0 {
		t.Fatalf("unexpected empty A10 totals: %d / %d", mA10, mA10Full)
	}
	overallPct := 100.0 * float64(bothA10) / float64(mA10)
	fullPct := 100.0 * float64(bothA10Full) / float64(mA10Full)
	if math.Abs(overallPct-55.1) > 1.5 {
		t.Fatalf("A10 overall cross-sensor agreement = %.2f%%, want ~55.1%%", overallPct)
	}
	if math.Abs(fullPct-57.3) > 1.5 {
		t.Fatalf("A10 full-day cross-sensor agreement = %.2f%%, want ~57.3%%", fullPct)
	}
}
