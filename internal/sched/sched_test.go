package sched

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pyrosync/internal/db"
)

func openSchedTestDB(t *testing.T) *pgxpool.Pool {
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

	if _, err := adminPool.Exec(ctx, `DROP DATABASE IF EXISTS pyrosync_sched_test WITH (FORCE)`); err != nil {
		adminPool.Close()
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `CREATE DATABASE pyrosync_sched_test`); err != nil {
		adminPool.Close()
		t.Fatalf("create test database: %v", err)
	}
	adminPool.Close()

	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse test dsn: %v", err)
	}
	u.Path = "/pyrosync_sched_test"
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

func TestPollerRunOnceSuccessAndIdempotency(t *testing.T) {
	pool := openSchedTestDB(t)
	ctx := context.Background()

	const secretKey = "SUPER_SECRET_FIRMS_KEY_9988"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := fmt.Sprintf("/api/area/csv/%s/", secretKey)
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		switch rest {
		case "MODIS_NRT/world/1":
			fmt.Fprintln(w, "latitude,longitude,brightness,scan,track,acq_date,acq_time,satellite,confidence,version,bright_t31,frp,daynight")
			fmt.Fprintln(w, "31.500,74.300,320.0,1.0,1.0,2026-11-14,0815,T,80,6.1NRT,295.0,22.0,D")
		case "VIIRS_SNPP_NRT/world/1":
			fmt.Fprintln(w, "latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,confidence,version,bright_ti5,frp,daynight")
			fmt.Fprintln(w, "31.502,74.302,335.0,0.4,0.4,2026-11-14,0825,N,n,2.0NRT,296.0,8.5,D")
		case "VIIRS_NOAA20_NRT/world/1":
			fmt.Fprintln(w, "latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,confidence,version,bright_ti5,frp,daynight")
			fmt.Fprintln(w, "31.504,74.304,338.0,0.4,0.4,2026-11-14,0910,N20,h,2.0NRT,297.0,10.2,D")
		case "VIIRS_NOAA21_NRT/world/1":
			fmt.Fprintln(w, "latitude,longitude,bright_ti4,scan,track,acq_date,acq_time,satellite,confidence,version,bright_ti5,frp,daynight")
			fmt.Fprintln(w, "31.506,74.306,331.0,0.4,0.4,2026-11-14,1005,N21,l,2.0NRT,294.0,2.5,D")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	poller := NewPoller(pool, secretKey, time.Hour, WithBaseURL(srv.URL))
	rep, err := poller.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if rep.RowsIn != 4 || rep.RecomputedDates != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}

	var status string
	var rowsIn int
	if err := pool.QueryRow(ctx, `SELECT status, rows_in FROM ingest_runs WHERE id = $1`, rep.RunID).Scan(&status, &rowsIn); err != nil {
		t.Fatal(err)
	}
	if status != "ok" || rowsIn != 4 {
		t.Fatalf("unexpected ingest_runs row: status=%s rows_in=%d", status, rowsIn)
	}

	// Second run sees identical SHA-256 CSVs and ingests zero new rows.
	rep2, err := poller.RunOnce(ctx)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if rep2.RowsIn != 0 {
		t.Fatalf("expected 0 new rows on duplicate poll, got %d", rep2.RowsIn)
	}
}

func TestPollerRedactsMapKeyOnFailure(t *testing.T) {
	pool := openSchedTestDB(t)
	ctx := context.Background()

	const secretKey = "TOP_SECRET_MAP_KEY_12345"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Invalid MAP_KEY %s\n", secretKey)
	}))
	defer srv.Close()

	poller := NewPoller(pool, secretKey, time.Hour, WithBaseURL(srv.URL))
	rep, err := poller.RunOnce(ctx)
	if err == nil {
		t.Fatalf("expected error on invalid response, got nil (rep=%+v)", rep)
	}
	if strings.Contains(err.Error(), secretKey) {
		t.Fatalf("error leaked secret key: %q", err.Error())
	}

	var storedErr string
	if err := pool.QueryRow(ctx, `SELECT error FROM ingest_runs WHERE id = $1`, rep.RunID).Scan(&storedErr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedErr, secretKey) || !strings.Contains(storedErr, "[REDACTED]") {
		t.Fatalf("stored error missing redaction or leaked secret: %q", storedErr)
	}
}
