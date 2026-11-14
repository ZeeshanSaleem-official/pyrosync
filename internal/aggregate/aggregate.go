package aggregate

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"pyrosync/internal/series"
)

const (
	maxRawRowsPerChunk = 75000
	maxDatesPerChunk   = 100
)

// RecomputeReport summarizes an aggregation recompute pass.
type RecomputeReport struct {
	DatesProcessed int
	CellA1Rows     int64
	CellA10Rows    int64
	RegionDayRows  int64
	Duration       time.Duration
}

// CalibrateOptions configures cross-sensor gain calibration across regions.
type CalibrateOptions struct {
	ConfLevel int
	LiveDays  int
}

// RecomputeDirty aggregates all dates currently queued in aggregate_dirty and drains the queue.
func RecomputeDirty(ctx context.Context, pool *pgxpool.Pool) (*RecomputeReport, error) {
	dates, err := queryDates(ctx, pool, `SELECT acq_date FROM aggregate_dirty ORDER BY acq_date`)
	if err != nil {
		return nil, fmt.Errorf("list dirty dates: %w", err)
	}
	return RecomputeDates(ctx, pool, dates)
}

// RecomputeAll recomputes aggregations across every date in raw_detections, coverage_days, or derived tables.
func RecomputeAll(ctx context.Context, pool *pgxpool.Pool) (*RecomputeReport, error) {
	dates, err := queryDates(ctx, pool, `
		SELECT d FROM (
			SELECT DISTINCT acq_date AS d FROM raw_detections
			UNION
			SELECT DISTINCT acq_date AS d FROM coverage_days
			UNION
			SELECT DISTINCT acq_date AS d FROM cell_day_a1
			UNION
			SELECT DISTINCT acq_date AS d FROM region_day
		) t
		ORDER BY d
	`)
	if err != nil {
		return nil, fmt.Errorf("list all dates: %w", err)
	}
	return RecomputeDates(ctx, pool, dates)
}

// RecomputeSince recomputes aggregations for every covered or active date >= since (UTC date).
func RecomputeSince(ctx context.Context, pool *pgxpool.Pool, since time.Time) (*RecomputeReport, error) {
	sinceDay := time.Date(since.UTC().Year(), since.UTC().Month(), since.UTC().Day(), 0, 0, 0, 0, time.UTC)
	dates, err := queryDates(ctx, pool, `
		SELECT d FROM (
			SELECT DISTINCT acq_date AS d FROM raw_detections WHERE acq_date >= $1::date
			UNION
			SELECT DISTINCT acq_date AS d FROM coverage_days WHERE acq_date >= $1::date
			UNION
			SELECT DISTINCT acq_date AS d FROM cell_day_a1 WHERE acq_date >= $1::date
			UNION
			SELECT DISTINCT acq_date AS d FROM region_day WHERE acq_date >= $1::date
		) t
		ORDER BY d
	`, sinceDay)
	if err != nil {
		return nil, fmt.Errorf("list dates since %s: %w", sinceDay.Format("2006-01-02"), err)
	}
	return RecomputeDates(ctx, pool, dates)
}

// RecomputeDates recomputes cell_day_a1, cell_day_a10, and region_day for the given UTC dates.
func RecomputeDates(ctx context.Context, pool *pgxpool.Pool, dates []time.Time) (*RecomputeReport, error) {
	start := time.Now()
	report := &RecomputeReport{}
	unique := dedupeSortDates(dates)
	if len(unique) == 0 {
		report.Duration = time.Since(start)
		return report, nil
	}

	paramsVersion, floorMW, err := loadActiveParams(ctx, pool)
	if err != nil {
		return nil, err
	}

	chunks, err := buildDateChunks(ctx, pool, unique)
	if err != nil {
		return nil, err
	}

	for _, chunk := range chunks {
		a1, a10, reg, err := recomputeChunk(ctx, pool, chunk, paramsVersion, floorMW)
		if err != nil {
			return report, err
		}
		report.DatesProcessed += len(chunk)
		report.CellA1Rows += a1
		report.CellA10Rows += a10
		report.RegionDayRows += reg
	}

	report.Duration = time.Since(start)
	return report, nil
}

// CalibrateAll fits 'history' and 'live' MODIS/VIIRS gain models for all preset regions.
func CalibrateAll(ctx context.Context, pool *pgxpool.Pool, opts CalibrateOptions) ([]series.CalibrationModel, error) {
	confLevel := opts.ConfLevel
	if confLevel <= 0 || confLevel > 3 {
		confLevel = 2
	}
	liveDays := opts.LiveDays
	if liveDays <= 0 {
		liveDays = 30
	}

	paramsVersion, _, err := loadActiveParams(ctx, pool)
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, `SELECT id FROM regions ORDER BY sort_order`)
	if err != nil {
		return nil, fmt.Errorf("list regions: %w", err)
	}
	var regionIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan region id: %w", err)
		}
		regionIDs = append(regionIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate regions: %w", err)
	}

	models := make([]series.CalibrationModel, 0, len(regionIDs)*2)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin calibration tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	for _, regionID := range regionIDs {
		samples, maxDate, err := queryOverlapSamples(ctx, tx, regionID, confLevel)
		if err != nil {
			return nil, err
		}

		histModel := series.FitCalibration(regionID, "history", paramsVersion, samples)
		if err := upsertCalibrationModel(ctx, tx, histModel); err != nil {
			return nil, err
		}
		models = append(models, histModel)

		var liveSamples []series.OverlapSample
		if maxDate != nil {
			cutoff := maxDate.AddDate(0, 0, -liveDays)
			for _, s := range samples {
				if !s.Date.Before(cutoff) {
					liveSamples = append(liveSamples, s)
				}
			}
		}
		liveModel := series.FitCalibration(regionID, "live", paramsVersion, liveSamples)
		if err := upsertCalibrationModel(ctx, tx, liveModel); err != nil {
			return nil, err
		}
		models = append(models, liveModel)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit calibration tx: %w", err)
	}
	return models, nil
}

func loadActiveParams(ctx context.Context, pool *pgxpool.Pool) (int, float64, error) {
	var version int
	var floorMW float64
	err := pool.QueryRow(ctx, `
		SELECT version, floor_mw::double precision
		FROM harmonization_params
		ORDER BY version DESC
		LIMIT 1
	`).Scan(&version, &floorMW)
	if err != nil {
		return 0, 0, fmt.Errorf("load harmonization_params: %w", err)
	}
	return version, floorMW, nil
}

func queryDates(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) ([]time.Time, error) {
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, time.Date(d.UTC().Year(), d.UTC().Month(), d.UTC().Day(), 0, 0, 0, 0, time.UTC))
	}
	return out, rows.Err()
}

func dedupeSortDates(dates []time.Time) []time.Time {
	seen := make(map[string]time.Time, len(dates))
	for _, d := range dates {
		u := time.Date(d.UTC().Year(), d.UTC().Month(), d.UTC().Day(), 0, 0, 0, 0, time.UTC)
		seen[u.Format("2006-01-02")] = u
	}
	out := make([]time.Time, 0, len(seen))
	for _, d := range seen {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Before(out[j])
	})
	return out
}

func buildDateChunks(ctx context.Context, pool *pgxpool.Pool, dates []time.Time) ([][]time.Time, error) {
	rows, err := pool.Query(ctx, `
		SELECT acq_date, COUNT(*)
		FROM raw_detections
		WHERE acq_date = ANY($1::date[])
		GROUP BY acq_date
	`, dates)
	if err != nil {
		return nil, fmt.Errorf("count raw rows per date: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int64, len(dates))
	for rows.Next() {
		var d time.Time
		var c int64
		if err := rows.Scan(&d, &c); err != nil {
			return nil, fmt.Errorf("scan row count: %w", err)
		}
		counts[d.UTC().Format("2006-01-02")] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate row counts: %w", err)
	}

	var chunks [][]time.Time
	var cur []time.Time
	var curRows int64

	for _, d := range dates {
		c := counts[d.Format("2006-01-02")]
		if len(cur) > 0 && (len(cur) >= maxDatesPerChunk || curRows+c > maxRawRowsPerChunk) {
			chunks = append(chunks, cur)
			cur = nil
			curRows = 0
		}
		cur = append(cur, d)
		curRows += c
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks, nil
}

func recomputeChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	dates []time.Time,
	paramsVersion int,
	floorMW float64,
) (int64, int64, int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("begin recompute tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `DELETE FROM cell_day_a1 WHERE acq_date = ANY($1::date[])`, dates); err != nil {
		return 0, 0, 0, fmt.Errorf("delete cell_day_a1: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM cell_day_a10 WHERE acq_date = ANY($1::date[])`, dates); err != nil {
		return 0, 0, 0, fmt.Errorf("delete cell_day_a10: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM region_day WHERE acq_date = ANY($1::date[])`, dates); err != nil {
		return 0, 0, 0, fmt.Errorf("delete region_day: %w", err)
	}

	tagA1, err := tx.Exec(ctx, `
		WITH ov AS (
			SELECT
				l.conf_level::smallint AS conf_level,
				r.acq_date,
				r.cell_a1,
				r.satellite,
				r.sensor,
				r.daynight,
				COUNT(*)::int AS px,
				SUM(r.frp_mw)::double precision AS frp_sum
			FROM raw_detections r
			CROSS JOIN (VALUES (1), (2), (3)) AS l(conf_level)
			WHERE r.acq_date = ANY($1::date[])
				AND r.conf_rank >= l.conf_level
			GROUP BY l.conf_level, r.acq_date, r.cell_a1, r.satellite, r.sensor, r.daynight
		),
		rolled AS (
			SELECT
				ov.acq_date,
				ov.cell_a1,
				ov.conf_level,
				COALESCE(SUM(ov.px) FILTER (WHERE ov.sensor = 'MODIS'), 0)::int AS modis_px,
				COALESCE(SUM(ov.px) FILTER (WHERE ov.sensor = 'VIIRS'), 0)::int AS viirs_px,
				COUNT(*) FILTER (WHERE ov.sensor = 'MODIS')::smallint AS n_ov_modis,
				COUNT(*) FILTER (WHERE ov.sensor = 'VIIRS' AND ov.frp_sum >= $2)::smallint AS n_ov_viirs_meq,
				(AVG(ov.frp_sum) FILTER (WHERE ov.sensor = 'MODIS'))::real AS modis_frp,
				(AVG(ov.frp_sum) FILTER (WHERE ov.sensor = 'VIIRS'))::real AS viirs_frp_all,
				(AVG(ov.frp_sum) FILTER (WHERE ov.sensor = 'VIIRS' AND ov.frp_sum >= $2))::real AS viirs_frp_meq,
				(AVG(ov.frp_sum) FILTER (WHERE ov.sensor = 'MODIS' OR ov.frp_sum >= $2))::real AS harmonized_frp
			FROM ov
			GROUP BY ov.acq_date, ov.cell_a1, ov.conf_level
		),
		cells AS (
			SELECT DISTINCT cell_a1 FROM rolled
		),
		geo AS (
			SELECT
				c.cell_a1,
				gc.clat::real AS lat,
				gc.clon::real AS lon,
				grid_cell_id(gc.clat, gc.clon, 0.10) AS cell_a10,
				ST_SetSRID(ST_MakePoint(gc.clon, gc.clat), 4326) AS pt
			FROM cells c
			CROSS JOIN LATERAL grid_center(c.cell_a1, 0.01) gc
		)
		INSERT INTO cell_day_a1 (
			acq_date, cell_a1, conf_level, cell_a10, lat, lon, pt,
			modis_px, viirs_px, n_ov_modis, n_ov_viirs_meq,
			modis_frp, viirs_frp_all, viirs_frp_meq, harmonized_frp, params_version
		)
		SELECT
			r.acq_date, r.cell_a1, r.conf_level, g.cell_a10, g.lat, g.lon, g.pt,
			r.modis_px, r.viirs_px, r.n_ov_modis, r.n_ov_viirs_meq,
			r.modis_frp, r.viirs_frp_all, r.viirs_frp_meq, r.harmonized_frp, $3
		FROM rolled r
		JOIN geo g ON g.cell_a1 = r.cell_a1
	`, dates, floorMW, paramsVersion)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("insert cell_day_a1: %w", err)
	}

	tagA10, err := tx.Exec(ctx, `
		WITH grp AS (
			SELECT
				acq_date,
				cell_a10,
				conf_level,
				COALESCE(SUM(modis_px), 0)::int AS modis_px,
				COALESCE(SUM(viirs_px), 0)::int AS viirs_px,
				COUNT(*) FILTER (WHERE harmonized_frp IS NOT NULL)::int AS cells_harmonized,
				COALESCE(SUM(harmonized_frp), 0)::real AS harmonized_frp
			FROM cell_day_a1
			WHERE acq_date = ANY($1::date[])
			GROUP BY acq_date, cell_a10, conf_level
		),
		cells10 AS (
			SELECT DISTINCT cell_a10 FROM grp
		),
		geo10 AS (
			SELECT
				c.cell_a10,
				gc.clat::real AS lat,
				gc.clon::real AS lon,
				ST_SetSRID(ST_MakePoint(gc.clon, gc.clat), 4326) AS pt
			FROM cells10 c
			CROSS JOIN LATERAL grid_center(c.cell_a10, 0.10) gc
		)
		INSERT INTO cell_day_a10 (
			acq_date, cell_a10, conf_level, lat, lon, pt,
			modis_px, viirs_px, cells_harmonized, harmonized_frp
		)
		SELECT
			g.acq_date, g.cell_a10, g.conf_level, geo10.lat, geo10.lon, geo10.pt,
			g.modis_px, g.viirs_px, g.cells_harmonized, g.harmonized_frp
		FROM grp g
		JOIN geo10 ON geo10.cell_a10 = g.cell_a10
	`, dates)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("insert cell_day_a10: %w", err)
	}

	tagReg, err := tx.Exec(ctx, `
		WITH cov_pair AS (
			SELECT
				r.id AS region_id,
				cov.acq_date,
				BOOL_OR(cov.family = 'MODIS') AS has_modis_cov,
				BOOL_OR(cov.family = 'VIIRS') AS has_viirs_cov,
				BOOL_OR(cov.is_partial) AS is_partial
			FROM regions r
			JOIN coverage_days cov
				ON cov.acq_date = ANY($1::date[])
				AND (
					(r.id = 'global' AND cov.area_id = 'world') OR
					(r.id <> 'global' AND (cov.area_id = r.id OR cov.area_id = 'world'))
				)
			GROUP BY r.id, cov.acq_date
		),
		raw_fallback AS (
			SELECT
				reg.id AS region_id,
				raw.acq_date,
				BOOL_OR(raw.sensor = 'MODIS') AS has_modis_cov,
				BOOL_OR(raw.sensor = 'VIIRS') AS has_viirs_cov,
				FALSE AS is_partial
			FROM raw_detections raw
			JOIN regions reg
				ON reg.id = 'global' OR ST_Covers(reg.geom, raw.geom)
			WHERE raw.acq_date = ANY($1::date[])
				AND NOT EXISTS (
					SELECT 1 FROM coverage_days cd WHERE cd.acq_date = raw.acq_date
				)
			GROUP BY reg.id, raw.acq_date
		),
		covered AS (
			SELECT * FROM cov_pair
			UNION ALL
			SELECT * FROM raw_fallback
		),
		lvls(conf_level) AS (
			VALUES (1::smallint), (2::smallint), (3::smallint)
		),
		cell_agg AS (
			SELECT
				reg.id AS region_id,
				c.acq_date,
				c.conf_level,
				SUM(c.modis_frp)::real AS sum_m_frp,
				SUM(c.viirs_frp_all)::real AS sum_v_all_frp,
				SUM(c.viirs_frp_meq)::real AS sum_v_meq_frp,
				COALESCE(SUM(c.modis_px), 0)::int AS modis_px,
				COALESCE(SUM(c.viirs_px), 0)::int AS viirs_px,
				COUNT(*) FILTER (WHERE c.modis_frp IS NOT NULL)::int AS cells_modis,
				COUNT(*) FILTER (WHERE c.viirs_frp_meq IS NOT NULL)::int AS cells_viirs_meq,
				COUNT(*) FILTER (WHERE c.harmonized_frp IS NOT NULL)::int AS cells_union
			FROM cell_day_a1 c
			JOIN regions reg
				ON reg.id = 'global' OR ST_Covers(reg.geom, c.pt)
			WHERE c.acq_date = ANY($1::date[])
			GROUP BY reg.id, c.acq_date, c.conf_level
		),
		cell_sat AS (
			SELECT DISTINCT
				raw.acq_date,
				raw.cell_a1,
				raw.satellite,
				raw.sensor,
				raw.conf_rank,
				c.pt
			FROM raw_detections raw
			JOIN cell_day_a1 c
				ON c.acq_date = raw.acq_date
				AND c.cell_a1 = raw.cell_a1
				AND c.conf_level = 1
			WHERE raw.acq_date = ANY($1::date[])
		),
		sat_agg AS (
			SELECT
				reg.id AS region_id,
				cs.acq_date,
				l.conf_level,
				COUNT(DISTINCT cs.satellite) FILTER (WHERE cs.sensor = 'MODIS')::smallint AS sats_modis,
				COUNT(DISTINCT cs.satellite) FILTER (WHERE cs.sensor = 'VIIRS')::smallint AS sats_viirs
			FROM cell_sat cs
			CROSS JOIN lvls l
			JOIN regions reg
				ON reg.id = 'global' OR ST_Covers(reg.geom, cs.pt)
			WHERE cs.conf_rank >= l.conf_level
			GROUP BY reg.id, cs.acq_date, l.conf_level
		)
		INSERT INTO region_day (
			region_id, acq_date, conf_level,
			m_frp, v_all_frp, v_meq_frp,
			modis_px, viirs_px,
			cells_modis, cells_viirs_meq, cells_union,
			sats_modis, sats_viirs, is_partial
		)
		SELECT
			cov.region_id,
			cov.acq_date,
			l.conf_level,
			CASE WHEN cov.has_modis_cov THEN COALESCE(ca.sum_m_frp, 0)::real ELSE NULL END,
			CASE WHEN cov.has_viirs_cov THEN COALESCE(ca.sum_v_all_frp, 0)::real ELSE NULL END,
			CASE WHEN cov.has_viirs_cov THEN COALESCE(ca.sum_v_meq_frp, 0)::real ELSE NULL END,
			COALESCE(ca.modis_px, 0),
			COALESCE(ca.viirs_px, 0),
			COALESCE(ca.cells_modis, 0),
			COALESCE(ca.cells_viirs_meq, 0),
			COALESCE(ca.cells_union, 0),
			COALESCE(sa.sats_modis, 0),
			COALESCE(sa.sats_viirs, 0),
			cov.is_partial
		FROM covered cov
		CROSS JOIN lvls l
		LEFT JOIN cell_agg ca
			ON ca.region_id = cov.region_id
			AND ca.acq_date = cov.acq_date
			AND ca.conf_level = l.conf_level
		LEFT JOIN sat_agg sa
			ON sa.region_id = cov.region_id
			AND sa.acq_date = cov.acq_date
			AND sa.conf_level = l.conf_level
	`, dates)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("insert region_day: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM aggregate_dirty WHERE acq_date = ANY($1::date[])`, dates); err != nil {
		return 0, 0, 0, fmt.Errorf("clear aggregate_dirty: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, 0, fmt.Errorf("commit recompute tx: %w", err)
	}
	return tagA1.RowsAffected(), tagA10.RowsAffected(), tagReg.RowsAffected(), nil
}

func queryOverlapSamples(
	ctx context.Context,
	tx pgx.Tx,
	regionID string,
	confLevel int,
) ([]series.OverlapSample, *time.Time, error) {
	var maxDate *time.Time
	err := tx.QueryRow(ctx, `
		SELECT MAX(acq_date)
		FROM region_day
		WHERE region_id = $1 AND conf_level = $2
	`, regionID, confLevel).Scan(&maxDate)
	if err != nil {
		return nil, nil, fmt.Errorf("query max region date for %s: %w", regionID, err)
	}

	rows, err := tx.Query(ctx, `
		SELECT acq_date, m_frp::double precision, v_meq_frp::double precision
		FROM region_day
		WHERE region_id = $1
			AND conf_level = $2
			AND NOT is_partial
			AND m_frp > 0
			AND v_meq_frp > 0
		ORDER BY acq_date
	`, regionID, confLevel)
	if err != nil {
		return nil, nil, fmt.Errorf("query overlap days for %s: %w", regionID, err)
	}
	defer rows.Close()

	var samples []series.OverlapSample
	for rows.Next() {
		var d time.Time
		var m, v float64
		if err := rows.Scan(&d, &m, &v); err != nil {
			return nil, nil, fmt.Errorf("scan overlap day for %s: %w", regionID, err)
		}
		samples = append(samples, series.OverlapSample{
			Date:        time.Date(d.UTC().Year(), d.UTC().Month(), d.UTC().Day(), 0, 0, 0, 0, time.UTC),
			ModisFRP:    m,
			ViirsMeqFRP: v,
		})
	}
	return samples, maxDate, rows.Err()
}

func upsertCalibrationModel(ctx context.Context, tx pgx.Tx, m series.CalibrationModel) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO calibration_models (
			region_id, scope, k, ci_low, ci_high, n_days, reliable, params_version, fitted_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (region_id, scope) DO UPDATE SET
			k = EXCLUDED.k,
			ci_low = EXCLUDED.ci_low,
			ci_high = EXCLUDED.ci_high,
			n_days = EXCLUDED.n_days,
			reliable = EXCLUDED.reliable,
			params_version = EXCLUDED.params_version,
			fitted_at = now()
	`, m.RegionID, m.Scope, m.K, m.CILow, m.CIHigh, m.NDays, m.Reliable, m.ParamsVersion)
	if err != nil {
		return fmt.Errorf("upsert calibration_models (%s/%s): %w", m.RegionID, m.Scope, err)
	}
	return nil
}
