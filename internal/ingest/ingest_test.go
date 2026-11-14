package ingest

import (
	"context"
	"math"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pyrosync/internal/db"
	"pyrosync/internal/grid"
)

func openTestDB(t *testing.T) *pgxpool.Pool {
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

	if _, err := adminPool.Exec(ctx, `DROP DATABASE IF EXISTS pyrosync_test WITH (FORCE)`); err != nil {
		adminPool.Close()
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `CREATE DATABASE pyrosync_test`); err != nil {
		adminPool.Close()
		t.Fatalf("create test database: %v", err)
	}
	adminPool.Close()

	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse test dsn: %v", err)
	}
	u.Path = "/pyrosync_test"
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

func TestGridParityWithPostGIS(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()

	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		lat := rng.Float64()*179.98 - 89.99
		lon := rng.Float64()*359.98 - 179.99
		id := grid.CellID(lat, lon, grid.ResA1)
		lat0, lon0, lat1, lon1 := grid.Bounds(id, grid.ResA1)

		var sqlLat0, sqlLon0, sqlLat1, sqlLon1 float64
		err := pool.QueryRow(ctx, `SELECT lat0, lon0, lat1, lon1 FROM grid_bounds($1, $2)`, id, grid.ResA1).
			Scan(&sqlLat0, &sqlLon0, &sqlLat1, &sqlLon1)
		if err != nil {
			t.Fatalf("grid_bounds query failed: %v", err)
		}

		if math.Abs(lat0-sqlLat0) > 1e-9 || math.Abs(lon0-sqlLon0) > 1e-9 ||
			math.Abs(lat1-sqlLat1) > 1e-9 || math.Abs(lon1-sqlLon1) > 1e-9 {
			t.Fatalf("Go/SQL bounds mismatch for id=%d: go=(%v,%v,%v,%v) sql=(%v,%v,%v,%v)",
				id, lat0, lon0, lat1, lon1, sqlLat0, sqlLon0, sqlLat1, sqlLon1)
		}
	}
}

func TestUpsertNRTToSPAndRejects(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	nrtPath := filepath.Join(dir, "nrt.csv")
	nrtCSV := strings.Join([]string{
		"latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,confidence,version,bright_t31,frp,daynight",
		"31.50000,74.30000,320.0,1.0,1.0,2026-11-12,34,T,70,6.1NRT,290.0,12.5,D",
		// Duplicate coordinate+time inside same file with SP should resolve inside batch cleanly.
		"31.50000,74.30000,320.0,1.0,1.0,2026-11-12,34,T,70,6.1NRT,290.0,12.5,D",
	}, "\n")
	if err := os.WriteFile(nrtPath, []byte(nrtCSV), 0o600); err != nil {
		t.Fatal(err)
	}

	rep1, err := IngestFile(ctx, pool, nrtPath, FileOptions{AreaID: "world"})
	if err != nil {
		t.Fatalf("ingest nrt failed: %v", err)
	}
	if rep1.RowsInserted != 1 || rep1.RowsIgnored != 1 {
		t.Fatalf("unexpected NRT counts: %+v", rep1)
	}

	// SP version of the same detection must replace NRT.
	spPath := filepath.Join(dir, "sp.csv")
	spCSV := strings.Join([]string{
		"latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,instrument,confidence,version,bright_t31,frp,daynight,type",
		"31.50000,74.30000,325.0,1.0,1.0,2026-11-12,34,Terra,MODIS,85,6.1,291.0,18.0,D,0",
	}, "\n")
	if err := os.WriteFile(spPath, []byte(spCSV), 0o600); err != nil {
		t.Fatal(err)
	}

	rep2, err := IngestFile(ctx, pool, spPath, FileOptions{AreaID: "world"})
	if err != nil {
		t.Fatalf("ingest sp failed: %v", err)
	}
	if rep2.RowsUpdated != 1 || rep2.RowsInserted != 0 {
		t.Fatalf("expected SP to update NRT row, got %+v", rep2)
	}

	// Subsequent NRT file with different FRP must NOT overwrite stored SP.
	nrt2Path := filepath.Join(dir, "nrt2.csv")
	nrt2CSV := strings.Join([]string{
		"latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,confidence,version,bright_t31,frp,daynight",
		"31.50000,74.30000,330.0,1.0,1.0,2026-11-12,34,T,70,6.1NRT,290.0,99.0,D",
	}, "\n")
	if err := os.WriteFile(nrt2Path, []byte(nrt2CSV), 0o600); err != nil {
		t.Fatal(err)
	}

	rep3, err := IngestFile(ctx, pool, nrt2Path, FileOptions{AreaID: "world"})
	if err != nil {
		t.Fatalf("ingest nrt2 failed: %v", err)
	}
	if rep3.RowsUpdated != 0 || rep3.RowsIgnored != 1 {
		t.Fatalf("expected NRT not to overwrite SP, got %+v", rep3)
	}

	var status string
	var frp float64
	if err := pool.QueryRow(ctx, `SELECT status::text, frp_mw FROM raw_detections`).Scan(&status, &frp); err != nil {
		t.Fatal(err)
	}
	if status != "SP" || math.Abs(frp-18.0) > 1e-4 {
		t.Fatalf("got status=%s frp=%v, want SP and 18.0", status, frp)
	}
}

func TestReal48hCSVIngestion(t *testing.T) {
	modisPath := filepath.Join("..", "..", "data", "input", "MODIS_C6_1_Global_48h.csv")
	viirsPath := filepath.Join("..", "..", "data", "input", "J2_VIIRS_C2_Global_48h.csv")
	if _, err := os.Stat(modisPath); err != nil {
		t.Skip("real 48h CSV inputs not present")
	}

	pool := openTestDB(t)
	ctx := context.Background()

	repM, err := IngestFile(ctx, pool, modisPath, FileOptions{AreaID: "world"})
	if err != nil {
		t.Fatalf("MODIS ingest failed: %v", err)
	}
	if repM.RowsInserted != 19145 || repM.RowsRejected != 0 {
		t.Fatalf("unexpected MODIS report: %+v", repM)
	}

	repV, err := IngestFile(ctx, pool, viirsPath, FileOptions{AreaID: "world"})
	if err != nil {
		t.Fatalf("VIIRS ingest failed: %v", err)
	}
	if repV.RowsInserted != 99736 || repV.RowsRejected != 0 {
		t.Fatalf("unexpected VIIRS report: %+v", repV)
	}

	// Re-ingesting must skip immediately by SHA-256 and change zero rows.
	repDup, err := IngestFile(ctx, pool, viirsPath, FileOptions{AreaID: "world"})
	if err != nil {
		t.Fatalf("duplicate ingest failed: %v", err)
	}
	if !repDup.SkippedDuplicate {
		t.Fatalf("expected SkippedDuplicate=true, got %+v", repDup)
	}

	var total int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM raw_detections`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 19145+99736 {
		t.Fatalf("raw_detections count = %d, want %d", total, 19145+99736)
	}

	var partialNov14 bool
	if err := pool.QueryRow(ctx, `
		SELECT is_partial FROM coverage_days
		WHERE area_id = 'world' AND family = 'MODIS' AND acq_date = '2026-11-14'
	`).Scan(&partialNov14); err != nil {
		t.Fatal(err)
	}
	if !partialNov14 {
		t.Fatal("expected 2026-11-14 to be flagged is_partial=true")
	}
}

func TestRejectThresholdAndLogging(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	dir := t.TempDir()

	badPath := filepath.Join(dir, "bad.csv")
	badCSV := strings.Join([]string{
		"latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,confidence,version,bright_ti5,frp,daynight",
		"31.50000,74.30000,320.0,0.4,0.4,2026-11-12,34,N21,nominal,2.0NRT,290.0,2.5,D",
		"31.50000,74.30000,320.0,0.4,0.4,2026-11-12,2460,N21,nominal,2.0NRT,290.0,2.5,D",
	}, "\n")
	if err := os.WriteFile(badPath, []byte(badCSV), 0o600); err != nil {
		t.Fatal(err)
	}

	rep, err := IngestFile(ctx, pool, badPath, FileOptions{AreaID: "world"})
	if err == nil {
		t.Fatalf("expected >1%% reject error, got nil (report=%+v)", rep)
	}
	if rep == nil || rep.RowsInserted != 1 || rep.RowsRejected != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}

	var storedReason string
	if err := pool.QueryRow(ctx, `SELECT reason FROM ingest_rejects`).Scan(&storedReason); err != nil {
		t.Fatalf("query ingest_rejects: %v", err)
	}
	if storedReason != "invalid_acq_time" {
		t.Fatalf("stored reject reason = %q, want invalid_acq_time", storedReason)
	}
}
