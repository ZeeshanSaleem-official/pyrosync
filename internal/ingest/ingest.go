package ingest

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"pyrosync/internal/normalize"
)

const (
	batchSize        = 20000
	csvBufferBytes   = 1 << 20 // 1 MiB streaming buffer
	maxStoredRejects = 1000
)

var archiveYearPattern = regexp.MustCompile(`_(20\d{2})_([A-Za-z-]+)\.csv$`)

// FileOptions controls ingestion behavior for one CSV file.
type FileOptions struct {
	AreaID string // "auto", "world", or preset region ID (e.g. "pakistan-indus")
}

// FileReport summarizes the outcome of ingesting one CSV file.
type FileReport struct {
	FileName         string
	SHA256           string
	Schema           string
	AreaID           string
	SkippedDuplicate bool
	RowsRead         int
	RowsInserted     int
	RowsUpdated      int
	RowsIgnored      int
	RowsRejected     int
	RejectReasons    map[string]int
	Duration         time.Duration
}

type rejectSample struct {
	LineNo  int
	Reason  string
	RawLine string
}

// IngestFile hashes, streams, validates, and upserts one FIRMS CSV file inside a transaction.
func IngestFile(ctx context.Context, pool *pgxpool.Pool, path string, opts FileOptions) (*FileReport, error) {
	start := time.Now().UTC()
	baseName := filepath.Base(path)

	hashHex, err := fileSHA256(path)
	if err != nil {
		return nil, fmt.Errorf("hash %s: %w", path, err)
	}

	var existingID int64
	err = pool.QueryRow(ctx, `SELECT id FROM ingest_files WHERE sha256 = $1`, hashHex).Scan(&existingID)
	if err == nil {
		return &FileReport{
			FileName:         baseName,
			SHA256:           hashHex,
			SkippedDuplicate: true,
			RejectReasons:    map[string]int{},
			Duration:         time.Since(start),
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check ingest hash: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	cr := csv.NewReader(bufio.NewReaderSize(f, csvBufferBytes))
	cr.ReuseRecord = true

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header of %s: %w", path, err)
	}
	schema, err := normalize.DetectSchema(header)
	if err != nil {
		return nil, fmt.Errorf("detect schema in %s: %w", path, err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin ingest tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var ingestFileID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO ingest_files (
			sha256, file_name, schema_detected,
			rows_read, rows_inserted, rows_updated, rows_ignored, rows_rejected,
			started_at, finished_at
		) VALUES ($1, $2, $3, 0, 0, 0, 0, 0, $4, $4)
		RETURNING id
	`, hashHex, baseName, string(schema.Sensor), start).Scan(&ingestFileID)
	if err != nil {
		return nil, fmt.Errorf("reserve ingest_files row: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE staging_detections (
			sensor      TEXT NOT NULL,
			satellite   TEXT NOT NULL,
			lat_e5      INTEGER NOT NULL,
			lon_e5      INTEGER NOT NULL,
			bt_mwir_k   REAL NOT NULL,
			bt_lwir_k   REAL NOT NULL,
			scan_km     REAL NOT NULL,
			track_km    REAL NOT NULL,
			acq_ts      TIMESTAMPTZ NOT NULL,
			acq_date    DATE NOT NULL,
			conf_rank   SMALLINT NOT NULL,
			conf_raw    TEXT NOT NULL,
			frp_mw      REAL NOT NULL,
			daynight    CHAR(1) NOT NULL,
			version     TEXT NOT NULL,
			status      TEXT NOT NULL,
			source_type SMALLINT NOT NULL,
			cell_a1     BIGINT NOT NULL,
			cell_a10    BIGINT NOT NULL
		) ON COMMIT DROP
	`); err != nil {
		return nil, fmt.Errorf("create staging table: %w", err)
	}

	report := &FileReport{
		FileName:      baseName,
		SHA256:        hashHex,
		Schema:        string(schema.Sensor),
		AreaID:        resolveAreaID(baseName, opts.AreaID),
		RejectReasons: make(map[string]int),
	}

	batch := make([]normalize.Detection, 0, batchSize)
	rejects := make([]rejectSample, 0, 64)
	touchedDates := make(map[time.Time]struct{})

	var minTS, maxTS time.Time
	var spRows int

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		ins, upd, ign, err := flushBatch(ctx, tx, ingestFileID, batch)
		if err != nil {
			return err
		}
		report.RowsInserted += ins
		report.RowsUpdated += upd
		report.RowsIgnored += ign
		batch = batch[:0]
		return nil
	}

	lineNo := 1
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		lineNo++
		if err != nil {
			report.RowsRead++
			report.RowsRejected++
			report.RejectReasons["csv_parse_error"]++
			if len(rejects) < maxStoredRejects {
				rejects = append(rejects, rejectSample{LineNo: lineNo, Reason: "csv_parse_error", RawLine: err.Error()})
			}
			continue
		}

		report.RowsRead++
		det, err := normalize.ParseRow(schema, rec)
		if err != nil {
			report.RowsRejected++
			reason := "validation_error"
			var rerr *normalize.RejectError
			if errors.As(err, &rerr) {
				reason = rerr.Reason
			}
			report.RejectReasons[reason]++
			if len(rejects) < maxStoredRejects {
				rejects = append(rejects, rejectSample{
					LineNo:  lineNo,
					Reason:  reason,
					RawLine: strings.Join(rec, ","),
				})
			}
			continue
		}

		if minTS.IsZero() || det.AcqTS.Before(minTS) {
			minTS = det.AcqTS
		}
		if maxTS.IsZero() || det.AcqTS.After(maxTS) {
			maxTS = det.AcqTS
		}
		if det.Status == normalize.StatusSP {
			spRows++
		}
		touchedDates[det.AcqDate] = struct{}{}

		batch = append(batch, det)
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}

	if err := flush(); err != nil {
		return nil, err
	}

	if len(rejects) > 0 {
		rejRows := make([][]any, len(rejects))
		for i, r := range rejects {
			rejRows[i] = []any{ingestFileID, r.LineNo, r.Reason, r.RawLine}
		}
		if _, err := tx.CopyFrom(
			ctx,
			pgx.Identifier{"ingest_rejects"},
			[]string{"ingest_file_id", "line_no", "reason", "raw_line"},
			pgx.CopyFromRows(rejRows),
		); err != nil {
			return nil, fmt.Errorf("store reject samples: %w", err)
		}
	}

	if err := recordCoverageAndDirty(ctx, tx, baseName, report.AreaID, schema.Sensor, minTS, maxTS, spRows, touchedDates); err != nil {
		return nil, err
	}

	if err := refreshRegionHistoryFlags(ctx, tx); err != nil {
		return nil, err
	}

	finished := time.Now().UTC()
	report.Duration = finished.Sub(start)

	if _, err := tx.Exec(ctx, `
		UPDATE ingest_files
		SET rows_read = $2, rows_inserted = $3, rows_updated = $4,
		    rows_ignored = $5, rows_rejected = $6, finished_at = $7
		WHERE id = $1
	`, ingestFileID, report.RowsRead, report.RowsInserted, report.RowsUpdated,
		report.RowsIgnored, report.RowsRejected, finished); err != nil {
		return nil, fmt.Errorf("finalize ingest_files: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO ingest_runs (kind, source, area, started_at, finished_at, rows_in, status)
		VALUES ('file', $1, $2, $3, $4, $5, 'ok')
	`, baseName, report.AreaID, start, finished, report.RowsInserted+report.RowsUpdated); err != nil {
		return nil, fmt.Errorf("record ingest_runs: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit ingest tx: %w", err)
	}

	if report.RowsRead > 0 && report.RowsRejected*100 > report.RowsRead {
		return report, fmt.Errorf("rejected %d of %d rows (>1%% threshold)", report.RowsRejected, report.RowsRead)
	}
	return report, nil
}

// IngestDir scans dir non-recursively for CSV files sorted alphabetically and ingests each.
func IngestDir(ctx context.Context, pool *pgxpool.Pool, dir string, opts FileOptions) ([]*FileReport, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", dir, err)
	}
	var csvPaths []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".csv") {
			csvPaths = append(csvPaths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(csvPaths)

	reports := make([]*FileReport, 0, len(csvPaths))
	for _, p := range csvPaths {
		rep, err := IngestFile(ctx, pool, p, opts)
		if rep != nil {
			reports = append(reports, rep)
		}
		if err != nil {
			return reports, err
		}
	}
	return reports, nil
}

func flushBatch(ctx context.Context, tx pgx.Tx, ingestFileID int64, batch []normalize.Detection) (inserted, updated, ignored int, err error) {
	if _, err := tx.Exec(ctx, `TRUNCATE TABLE staging_detections`); err != nil {
		return 0, 0, 0, fmt.Errorf("truncate staging: %w", err)
	}

	cols := []string{
		"sensor", "satellite", "lat_e5", "lon_e5",
		"bt_mwir_k", "bt_lwir_k", "scan_km", "track_km",
		"acq_ts", "acq_date", "conf_rank", "conf_raw", "frp_mw",
		"daynight", "version", "status", "source_type",
		"cell_a1", "cell_a10",
	}

	rows := make([][]any, len(batch))
	for i, d := range batch {
		rows[i] = []any{
			string(d.Sensor), string(d.Satellite), d.LatE5, d.LonE5,
			d.BTMWIRK, d.BTLWIRK, d.ScanKm, d.TrackKm,
			d.AcqTS, d.AcqDate, d.ConfRank, d.ConfRaw, d.FRPMW,
			d.DayNight, d.Version, string(d.Status), d.SourceType,
			d.CellA1, d.CellA10,
		}
	}

	copied, err := tx.CopyFrom(ctx, pgx.Identifier{"staging_detections"}, cols, pgx.CopyFromRows(rows))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("copy staging batch: %w", err)
	}

	upsertRows, err := tx.Query(ctx, `
		WITH deduped AS (
			SELECT DISTINCT ON (satellite, lat_e5, lon_e5, acq_ts)
				sensor::sensor_family AS sensor,
				satellite::satellite_code AS satellite,
				lat_e5,
				lon_e5,
				bt_mwir_k,
				bt_lwir_k,
				scan_km,
				track_km,
				acq_ts,
				acq_date,
				conf_rank,
				conf_raw,
				frp_mw,
				daynight,
				version,
				status::product_status AS status,
				source_type,
				cell_a1,
				cell_a10
			FROM staging_detections
			ORDER BY satellite, lat_e5, lon_e5, acq_ts, (status = 'SP') DESC
		)
		INSERT INTO raw_detections (
			sensor, satellite, lat_e5, lon_e5, geom,
			bt_mwir_k, bt_lwir_k, scan_km, track_km,
			acq_ts, acq_date, conf_rank, conf_raw, frp_mw,
			daynight, version, status, source_type,
			cell_a1, cell_a10, ingest_file_id
		)
		SELECT
			sensor, satellite, lat_e5, lon_e5,
			ST_SetSRID(ST_MakePoint(lon_e5::double precision / 100000.0, lat_e5::double precision / 100000.0), 4326),
			bt_mwir_k, bt_lwir_k, scan_km, track_km,
			acq_ts, acq_date, conf_rank, conf_raw, frp_mw,
			daynight, version, status, source_type,
			cell_a1, cell_a10, $1
		FROM deduped
		ON CONFLICT (satellite, lat_e5, lon_e5, acq_ts) DO UPDATE SET
			bt_mwir_k      = EXCLUDED.bt_mwir_k,
			bt_lwir_k      = EXCLUDED.bt_lwir_k,
			scan_km        = EXCLUDED.scan_km,
			track_km       = EXCLUDED.track_km,
			conf_rank      = EXCLUDED.conf_rank,
			conf_raw       = EXCLUDED.conf_raw,
			frp_mw         = EXCLUDED.frp_mw,
			version        = EXCLUDED.version,
			status         = EXCLUDED.status,
			source_type    = EXCLUDED.source_type,
			ingest_file_id = EXCLUDED.ingest_file_id
		WHERE raw_detections.status = 'NRT' AND EXCLUDED.status = 'SP'
		RETURNING (xmax = 0) AS is_insert
	`, ingestFileID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("upsert raw_detections: %w", err)
	}
	defer upsertRows.Close()

	for upsertRows.Next() {
		var isInsert bool
		if err := upsertRows.Scan(&isInsert); err != nil {
			return 0, 0, 0, fmt.Errorf("scan upsert returning: %w", err)
		}
		if isInsert {
			inserted++
		} else {
			updated++
		}
	}
	if err := upsertRows.Err(); err != nil {
		return 0, 0, 0, fmt.Errorf("iterate upsert returning: %w", err)
	}

	ignored = int(copied) - inserted - updated
	return inserted, updated, ignored, nil
}

func recordCoverageAndDirty(
	ctx context.Context,
	tx pgx.Tx,
	fileName string,
	areaID string,
	sensor normalize.SensorFamily,
	minTS, maxTS time.Time,
	spRows int,
	touchedDates map[time.Time]struct{},
) error {
	if len(touchedDates) == 0 {
		return nil
	}

	// Mark touched dates dirty for downstream aggregation.
	dirtyList := make([]time.Time, 0, len(touchedDates))
	for d := range touchedDates {
		dirtyList = append(dirtyList, d)
	}
	sort.Slice(dirtyList, func(i, j int) bool { return dirtyList[i].Before(dirtyList[j]) })
	if err := markAggregateDirty(ctx, tx, dirtyList); err != nil {
		return err
	}

	// Complete yearly country SP archive CSVs cover every day of that year so zero-fire days stay distinct from missing years.
	if m := archiveYearPattern.FindStringSubmatch(fileName); m != nil && areaID != "world" && spRows > 0 {
		year, err := strconv.Atoi(m[1])
		if err == nil {
			startDay := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
			endDay := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
			todayUTC := time.Now().UTC().Truncate(24 * time.Hour)
			if !endDay.Before(todayUTC) {
				endDay = todayUTC.AddDate(0, 0, -1)
			}
			var covDates []time.Time
			var covPartial []bool
			for d := startDay; !d.After(endDay); d = d.AddDate(0, 0, 1) {
				covDates = append(covDates, d)
				covPartial = append(covPartial, false)
			}
			if err := markAggregateDirty(ctx, tx, covDates); err != nil {
				return err
			}
			return upsertCoverageBatch(ctx, tx, areaID, sensor, covDates, covPartial)
		}
	}

	// Rolling snapshot files (e.g. Global 48h): mark full span and flag partial boundary days.
	minDay := time.Date(minTS.Year(), minTS.Month(), minTS.Day(), 0, 0, 0, 0, time.UTC)
	maxDay := time.Date(maxTS.Year(), maxTS.Month(), maxTS.Day(), 0, 0, 0, 0, time.UTC)
	todayUTC := time.Now().UTC().Truncate(24 * time.Hour)

	var covDates []time.Time
	var covPartial []bool
	for d := minDay; !d.After(maxDay); d = d.AddDate(0, 0, 1) {
		partial := false
		if d.Equal(minDay) && minTS.Hour() >= 2 {
			partial = true
		}
		if d.Equal(maxDay) && maxTS.Hour() < 22 {
			partial = true
		}
		if d.Equal(todayUTC) {
			partial = true
		}
		covDates = append(covDates, d)
		covPartial = append(covPartial, partial)
	}
	return upsertCoverageBatch(ctx, tx, areaID, sensor, covDates, covPartial)
}

func markAggregateDirty(ctx context.Context, tx pgx.Tx, dates []time.Time) error {
	if len(dates) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO aggregate_dirty (acq_date)
		SELECT UNNEST($1::date[])
		ON CONFLICT (acq_date) DO UPDATE SET marked_at = now()
	`, dates)
	if err != nil {
		return fmt.Errorf("mark aggregate_dirty: %w", err)
	}
	return nil
}

func upsertCoverageBatch(
	ctx context.Context,
	tx pgx.Tx,
	areaID string,
	sensor normalize.SensorFamily,
	dates []time.Time,
	partial []bool,
) error {
	if len(dates) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO coverage_days (area_id, family, acq_date, is_partial)
		SELECT $1, $2::sensor_family, u.d, u.p
		FROM UNNEST($3::date[], $4::boolean[]) AS u(d, p)
		ON CONFLICT (area_id, family, acq_date) DO UPDATE
		SET is_partial = coverage_days.is_partial AND EXCLUDED.is_partial
	`, areaID, string(sensor), dates, partial)
	if err != nil {
		return fmt.Errorf("upsert coverage_days: %w", err)
	}
	return nil
}

func refreshRegionHistoryFlags(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `
		WITH cov AS (
			SELECT
				r.id,
				COUNT(DISTINCT EXTRACT(YEAR FROM c.acq_date)) FILTER (WHERE c.family = 'MODIS') AS modis_years,
				COUNT(DISTINCT EXTRACT(YEAR FROM c.acq_date)) FILTER (WHERE c.family = 'VIIRS') AS viirs_years,
				MIN(c.acq_date) AS min_d,
				MAX(c.acq_date) AS max_d
			FROM regions r
			LEFT JOIN coverage_days c ON c.area_id = r.id
			WHERE r.id <> 'global'
			GROUP BY r.id
		)
		UPDATE regions r
		SET
			has_history  = (cov.modis_years >= 3 AND cov.viirs_years >= 3),
			history_from = CASE WHEN cov.modis_years >= 3 AND cov.viirs_years >= 3 THEN cov.min_d ELSE NULL END,
			history_to   = CASE WHEN cov.modis_years >= 3 AND cov.viirs_years >= 3 THEN cov.max_d ELSE NULL END
		FROM cov
		WHERE r.id = cov.id
	`)
	if err != nil {
		return fmt.Errorf("refresh region history flags: %w", err)
	}
	return nil
}

func resolveAreaID(fileName, override string) string {
	trimmed := strings.TrimSpace(override)
	if trimmed != "" && trimmed != "auto" {
		return trimmed
	}
	lower := strings.ToLower(fileName)
	switch {
	case strings.Contains(lower, "pakistan"):
		return "pakistan-indus"
	case strings.Contains(lower, "california"):
		return "california"
	case strings.Contains(lower, "amazon") || strings.Contains(lower, "brazil"):
		return "amazon"
	default:
		return "world"
	}
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
