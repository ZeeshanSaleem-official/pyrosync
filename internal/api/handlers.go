package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"pyrosync/internal/grid"
	"pyrosync/internal/sched"
	"pyrosync/internal/series"
)

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

type readyResponse struct {
	Status  string `json:"status"`
	Regions int    `json:"regions"`
	RawRows int64  `json:"raw_rows"`
	Seeded  bool   `json:"seeded"`
}

type metaSatelliteCount struct {
	Sensor    string `json:"sensor"`
	Satellite string `json:"satellite"`
	Status    string `json:"status"`
	Count     int64  `json:"count"`
}

type metaIngestRun struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	Source     string     `json:"source,omitempty"`
	Area       string     `json:"area,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	RowsIn     int        `json:"rows_in"`
	Status     string     `json:"status"`
}

type metaResponse struct {
	Version           string               `json:"version"`
	ParamsVersion     int                  `json:"params_version"`
	FloorMW           float64              `json:"floor_mw"`
	Grid              map[string]float64   `json:"grid"`
	LiveMinDate       *string              `json:"live_min_date,omitempty"`
	LiveMaxDate       *string              `json:"live_max_date,omitempty"`
	ArchiveMinDate    *string              `json:"archive_min_date,omitempty"`
	ArchiveMaxDate    *string              `json:"archive_max_date,omitempty"`
	TotalDetections   int64                `json:"total_detections"`
	Satellites        []metaSatelliteCount `json:"satellites"`
	LastIngest        *metaIngestRun       `json:"last_ingest,omitempty"`
	NRTPollingEnabled bool                 `json:"nrt_polling_enabled"`
}

type regionDTO struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	BBox        [4]float64 `json:"bbox"`
	HasHistory  bool       `json:"has_history"`
	HistoryFrom *string    `json:"history_from,omitempty"`
	HistoryTo   *string    `json:"history_to,omitempty"`
}

type calibrationDTO struct {
	Scope         string  `json:"scope"`
	K             float64 `json:"k"`
	CILow         float64 `json:"ci_low"`
	CIHigh        float64 `json:"ci_high"`
	NDays         int     `json:"n_days"`
	Reliable      bool    `json:"reliable"`
	KApplied      float64 `json:"k_applied"`
	ParamsVersion int     `json:"params_version"`
	Label         string  `json:"label"`
}

type ladderStepDTO struct {
	Step       int     `json:"step"`
	Key        string  `json:"key"`
	Label      string  `json:"label"`
	ModisValue float64 `json:"modis_value"`
	ViirsValue float64 `json:"viirs_value"`
	Unit       string  `json:"unit"`
	Ratio      float64 `json:"ratio"`
}

type agreementDTO struct {
	ModisCellDays       int     `json:"modis_cell_days"`
	BothCellDays        int     `json:"both_cell_days"`
	OverallPct          float64 `json:"overall_pct"`
	FullDayModisCells   int     `json:"full_day_modis_cell_days"`
	FullDayBothCells    int     `json:"full_day_both_cell_days"`
	FullDayPct          float64 `json:"full_day_pct"`
}

type dailySummaryDTO struct {
	Date          string   `json:"date"`
	ModisFRP      *float64 `json:"modis_frp,omitempty"`
	ViirsAllFRP   *float64 `json:"viirs_all_frp,omitempty"`
	ViirsMeqFRP   *float64 `json:"viirs_meq_frp,omitempty"`
	HarmonizedFRP float64  `json:"harmonized_frp"`
	CellUnionFRP  float64  `json:"cell_union_frp"`
	RobustZ       *float64 `json:"robust_z,omitempty"`
	ModisPx       int      `json:"modis_px"`
	ViirsPx       int      `json:"viirs_px"`
	CellsModis    int      `json:"cells_modis"`
	CellsViirsMeq int      `json:"cells_viirs_meq"`
	CellsUnion    int      `json:"cells_union"`
	SatsModis     int16    `json:"sats_modis"`
	SatsViirs     int16    `json:"sats_viirs"`
	IsPartial     bool     `json:"is_partial"`
}

type summaryResponse struct {
	Region      string            `json:"region"`
	RegionName  string            `json:"region_name"`
	From        string            `json:"from"`
	To          string            `json:"to"`
	ConfLevel   int               `json:"conf_level"`
	FloorMW     float64           `json:"floor_mw"`
	KPIs        map[string]any    `json:"kpis"`
	Ladder      []ladderStepDTO   `json:"ladder"`
	Agreement   agreementDTO      `json:"a10_agreement"`
	Calibration calibrationDTO    `json:"calibration"`
	Daily       []dailySummaryDTO `json:"daily"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:  "ok",
		Version: apiVersion,
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := s.pool.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unreachable")
		return
	}

	var regionsCount int
	var rawRows int64
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM regions`).Scan(&regionsCount); err != nil {
		writeError(w, http.StatusServiceUnavailable, "regions table unavailable")
		return
	}
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM raw_detections`).Scan(&rawRows); err != nil {
		writeError(w, http.StatusServiceUnavailable, "raw_detections table unavailable")
		return
	}
	writeJSON(w, http.StatusOK, readyResponse{
		Status:  "ready",
		Regions: regionsCount,
		RawRows: rawRows,
		Seeded:  rawRows > 0,
	})
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var paramsVersion int
	var floorMW float64
	if err := s.pool.QueryRow(ctx, `
		SELECT version, floor_mw::double precision
		FROM harmonization_params
		ORDER BY version DESC
		LIMIT 1
	`).Scan(&paramsVersion, &floorMW); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load harmonization parameters")
		return
	}

	satRows, err := s.pool.Query(ctx, `
		SELECT sensor::text, satellite::text, status::text, COUNT(*)
		FROM raw_detections
		GROUP BY sensor, satellite, status
		ORDER BY sensor, satellite, status
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to summarize satellites")
		return
	}
	defer satRows.Close()

	var total int64
	sats := make([]metaSatelliteCount, 0, 8)
	for satRows.Next() {
		var item metaSatelliteCount
		if err := satRows.Scan(&item.Sensor, &item.Satellite, &item.Status, &item.Count); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read satellite summary")
			return
		}
		total += item.Count
		sats = append(sats, item)
	}
	if err := satRows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to iterate satellite summary")
		return
	}

	var liveMin, liveMax, archMin, archMax *time.Time
	_ = s.pool.QueryRow(ctx, `
		SELECT MIN(acq_date), MAX(acq_date)
		FROM coverage_days
		WHERE area_id = 'world'
	`).Scan(&liveMin, &liveMax)
	_ = s.pool.QueryRow(ctx, `
		SELECT MIN(acq_date), MAX(acq_date)
		FROM coverage_days
		WHERE area_id <> 'world'
	`).Scan(&archMin, &archMax)

	var lastRun *metaIngestRun
	var run metaIngestRun
	var src, area *string
	err = s.pool.QueryRow(ctx, `
		SELECT id, kind, source, area, started_at, finished_at, rows_in, status
		FROM ingest_runs
		ORDER BY id DESC
		LIMIT 1
	`).Scan(&run.ID, &run.Kind, &src, &area, &run.StartedAt, &run.FinishedAt, &run.RowsIn, &run.Status)
	if err == nil {
		if src != nil {
			run.Source = *src
		}
		if area != nil {
			run.Area = *area
		}
		lastRun = &run
	}

	writeJSON(w, http.StatusOK, metaResponse{
		Version:       apiVersion,
		ParamsVersion: paramsVersion,
		FloorMW:       round2(floorMW),
		Grid: map[string]float64{
			"a1_deg":        grid.ResA1,
			"a10_deg":       grid.ResA10,
			"a1_km2_approx": 1.23,
			"a10_km2_approx": 123.0,
		},
		LiveMinDate:       formatOptDate(liveMin),
		LiveMaxDate:       formatOptDate(liveMax),
		ArchiveMinDate:    formatOptDate(archMin),
		ArchiveMaxDate:    formatOptDate(archMax),
		TotalDetections:   total,
		Satellites:        sats,
		LastIngest:        lastRun,
		NRTPollingEnabled: s.cfg.FirmsMapKey != "",
	})
}

func (s *Server) handleRegions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, west, south, east, north, has_history, history_from, history_to
		FROM regions
		ORDER BY sort_order
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load regions")
		return
	}
	defer rows.Close()

	out := make([]regionDTO, 0, 8)
	for rows.Next() {
		var item regionDTO
		var west, south, east, north float64
		var hFrom, hTo *time.Time
		if err := rows.Scan(&item.ID, &item.Name, &west, &south, &east, &north, &item.HasHistory, &hFrom, &hTo); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read region")
			return
		}
		item.BBox = [4]float64{west, south, east, north}
		item.HistoryFrom = formatOptDate(hFrom)
		item.HistoryTo = formatOptDate(hTo)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to iterate regions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"regions": out})
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, err := s.parseFilter(ctx, r, "live", 2)
	if err != nil {
		writeHTTPError(w, err)
		return
	}

	floorMW, paramsVersion := s.activeFloor(ctx)

	bboxExpr, bboxArgs := f.bboxSQL("pt", 4)
	args := append([]any{f.From, f.To, f.ConfLevel}, bboxArgs...)

	var modisPx, viirsPx int64
	var cellsModis, cellsVAll, cellsVMeq, cellsUnion int64
	var sumMFRP, sumVAllFRP, sumVMeqFRP, sumHarmFRP float64

	cellSQL := fmt.Sprintf(`
		WITH slice AS MATERIALIZED (
			SELECT
				modis_px, viirs_px,
				modis_frp, viirs_frp_all, viirs_frp_meq, harmonized_frp,
				pt
			FROM cell_day_a1
			WHERE acq_date BETWEEN $1::date AND $2::date
				AND conf_level = $3
		)
		SELECT
			COALESCE(SUM(modis_px), 0),
			COALESCE(SUM(viirs_px), 0),
			COUNT(*) FILTER (WHERE modis_frp IS NOT NULL),
			COUNT(*) FILTER (WHERE viirs_frp_all IS NOT NULL),
			COUNT(*) FILTER (WHERE viirs_frp_meq IS NOT NULL),
			COUNT(*) FILTER (WHERE harmonized_frp IS NOT NULL),
			COALESCE(SUM(modis_frp), 0)::double precision,
			COALESCE(SUM(viirs_frp_all), 0)::double precision,
			COALESCE(SUM(viirs_frp_meq), 0)::double precision,
			COALESCE(SUM(harmonized_frp), 0)::double precision
		FROM slice
		WHERE %s
	`, bboxExpr)

	if err := s.pool.QueryRow(ctx, cellSQL, args...).Scan(
		&modisPx, &viirsPx,
		&cellsModis, &cellsVAll, &cellsVMeq, &cellsUnion,
		&sumMFRP, &sumVAllFRP, &sumVMeqFRP, &sumHarmFRP,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to aggregate cell summary")
		return
	}

	agree, err := s.queryA10Agreement(ctx, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute A10 agreement")
		return
	}

	rawDaily, unionByDay, err := s.queryDailyBreakdown(ctx, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query daily breakdown")
		return
	}

	calModel, err := s.loadOrFitCalibration(ctx, f, rawDaily, paramsVersion)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load calibration model")
		return
	}

	seriesPts := series.BuildDailySeries(rawDaily, calModel.KApplied, false)
	dailyOut := make([]dailySummaryDTO, 0, len(seriesPts))
	var anyPartial bool
	for _, pt := range seriesPts {
		if pt.IsPartial {
			anyPartial = true
		}
		dKey := pt.Date.Format("2006-01-02")
		unionVal := unionByDay[dKey]
		if len(unionByDay) == 0 {
			unionVal = pt.HarmonizedFRP
		}
		dailyOut = append(dailyOut, dailySummaryDTO{
			Date:          dKey,
			ModisFRP:      pt.ModisFRP,
			ViirsAllFRP:   pt.ViirsAllFRP,
			ViirsMeqFRP:   pt.ViirsMeqFRP,
			HarmonizedFRP: round2(pt.HarmonizedFRP),
			CellUnionFRP:  round2(unionVal),
			ModisPx:       pt.ModisPx,
			ViirsPx:       pt.ViirsPx,
			CellsModis:    pt.CellsModis,
			CellsViirsMeq: pt.CellsViirsMeq,
			CellsUnion:    pt.CellsUnion,
			SatsModis:     pt.SatsModis,
			SatsViirs:     pt.SatsViirs,
			IsPartial:     pt.IsPartial,
		})
	}

	ladder := []ladderStepDTO{
		{
			Step:       1,
			Key:        "raw_pixels",
			Label:      "Raw Pixels",
			ModisValue: float64(modisPx),
			ViirsValue: float64(viirsPx),
			Unit:       "px",
			Ratio:      round2(safeRatio(float64(viirsPx), float64(modisPx))),
		},
		{
			Step:       2,
			Key:        "equal_area_cells",
			Label:      "Equal-Area Cells (A1)",
			ModisValue: float64(cellsModis),
			ViirsValue: float64(cellsVAll),
			Unit:       "cells",
			Ratio:      round2(safeRatio(float64(cellsVAll), float64(cellsModis))),
		},
		{
			Step:       3,
			Key:        "modis_floor_cells",
			Label:      "Floor-Aligned Cells (>= 6.2 MW)",
			ModisValue: float64(cellsModis),
			ViirsValue: float64(cellsVMeq),
			Unit:       "cells",
			Ratio:      round2(safeRatio(float64(cellsVMeq), float64(cellsModis))),
		},
		{
			Step:       4,
			Key:        "radiative_power",
			Label:      "Floor-Aligned FRP",
			ModisValue: round1(sumMFRP),
			ViirsValue: round1(sumVMeqFRP),
			Unit:       "MW",
			Ratio:      round2(safeRatio(sumVMeqFRP, sumMFRP)),
		},
	}

	kpis := map[string]any{
		"raw_pixels_modis":       modisPx,
		"raw_pixels_viirs":       viirsPx,
		"raw_pixels_total":       modisPx + viirsPx,
		"harmonized_cells_union": cellsUnion,
		"harmonized_frp_mw":      round1(sumHarmFRP),
		"modis_frp_mw":           round1(sumMFRP),
		"viirs_all_frp_mw":       round1(sumVAllFRP),
		"viirs_meq_frp_mw":       round1(sumVMeqFRP),
		"days":                   len(dailyOut),
		"any_partial":            anyPartial,
	}

	writeJSON(w, http.StatusOK, summaryResponse{
		Region:      f.RegionID,
		RegionName:  f.RegionName,
		From:        f.From.Format("2006-01-02"),
		To:          f.To.Format("2006-01-02"),
		ConfLevel:   f.ConfLevel,
		FloorMW:     floorMW,
		KPIs:        kpis,
		Ladder:      ladder,
		Agreement:   agree,
		Calibration: toCalibrationDTO(calModel),
		Daily:       dailyOut,
	})
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, err := s.parseFilter(ctx, r, "live", 2)
	if err != nil {
		writeHTTPError(w, err)
		return
	}

	bucket := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("bucket")))
	if bucket == "" {
		bucket = "day"
	}

	_, paramsVersion := s.activeFloor(ctx)

	switch bucket {
	case "day":
		rawDaily, unionByDay, err := s.queryDailyBreakdown(ctx, f)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load daily timeline")
			return
		}
		calModel, err := s.loadOrFitCalibration(ctx, f, rawDaily, paramsVersion)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load calibration")
			return
		}
		pts := series.BuildDailySeries(rawDaily, calModel.KApplied, false)
		out := make([]dailySummaryDTO, 0, len(pts))
		for _, pt := range pts {
			dKey := pt.Date.Format("2006-01-02")
			out = append(out, dailySummaryDTO{
				Date:          dKey,
				ModisFRP:      pt.ModisFRP,
				ViirsAllFRP:   pt.ViirsAllFRP,
				ViirsMeqFRP:   pt.ViirsMeqFRP,
				HarmonizedFRP: round2(pt.HarmonizedFRP),
				CellUnionFRP:  round2(func() float64 { if len(unionByDay) == 0 { return pt.HarmonizedFRP }; return unionByDay[dKey] }()),
				ModisPx:       pt.ModisPx,
				ViirsPx:       pt.ViirsPx,
				CellsModis:    pt.CellsModis,
				CellsViirsMeq: pt.CellsViirsMeq,
				CellsUnion:    pt.CellsUnion,
				SatsModis:     pt.SatsModis,
				SatsViirs:     pt.SatsViirs,
				IsPartial:     pt.IsPartial,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"region":      f.RegionID,
			"from":        f.From.Format("2006-01-02"),
			"to":          f.To.Format("2006-01-02"),
			"bucket":      "day",
			"calibration": toCalibrationDTO(calModel),
			"series":      out,
		})

	case "6h":
		if f.To.Sub(f.From) > 31*24*time.Hour {
			writeError(w, http.StatusBadRequest, "6h timeline supports windows up to 31 days")
			return
		}
		floorMW, _ := s.activeFloor(ctx)
		bboxExpr, bboxArgs := f.bboxSQL("r.geom", 5)
		args := append([]any{f.From, f.To, f.ConfLevel, floorMW}, bboxArgs...)

		sql := fmt.Sprintf(`
			WITH ov AS (
				SELECT
					(date_trunc('day', r.acq_ts AT TIME ZONE 'UTC') +
						(FLOOR(EXTRACT(HOUR FROM r.acq_ts AT TIME ZONE 'UTC') / 6.0) * INTERVAL '6 hours')) AT TIME ZONE 'UTC' AS bucket_ts,
					r.cell_a1,
					r.satellite,
					r.sensor,
					COUNT(*)::int AS px,
					SUM(r.frp_mw)::double precision AS frp_sum
				FROM raw_detections r
				WHERE r.acq_date BETWEEN $1::date AND $2::date
					AND r.conf_rank >= $3
					AND %s
				GROUP BY 1, 2, 3, 4
			),
			c6 AS (
				SELECT
					bucket_ts,
					cell_a1,
					COALESCE(SUM(px) FILTER (WHERE sensor = 'MODIS'), 0)::int AS m_px,
					COALESCE(SUM(px) FILTER (WHERE sensor = 'VIIRS'), 0)::int AS v_px,
					AVG(frp_sum) FILTER (WHERE sensor = 'MODIS') AS m_frp,
					AVG(frp_sum) FILTER (WHERE sensor = 'VIIRS' AND frp_sum >= $4) AS v_meq_frp,
					AVG(frp_sum) FILTER (WHERE sensor = 'MODIS' OR frp_sum >= $4) AS h_frp
				FROM ov
				GROUP BY bucket_ts, cell_a1
			)
			SELECT
				bucket_ts,
				COALESCE(SUM(m_px), 0)::int,
				COALESCE(SUM(v_px), 0)::int,
				COUNT(*) FILTER (WHERE h_frp IS NOT NULL)::int,
				COALESCE(SUM(m_frp), 0)::double precision,
				COALESCE(SUM(v_meq_frp), 0)::double precision,
				COALESCE(SUM(h_frp), 0)::double precision
			FROM c6
			GROUP BY bucket_ts
			ORDER BY bucket_ts
		`, bboxExpr)

		rows, err := s.pool.Query(ctx, sql, args...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load 6h timeline")
			return
		}
		defer rows.Close()

		type bucket6h struct {
			BucketTS      string  `json:"bucket_ts"`
			ModisPx       int     `json:"modis_px"`
			ViirsPx       int     `json:"viirs_px"`
			CellsUnion    int     `json:"cells_union"`
			ModisFRP      float64 `json:"modis_frp"`
			ViirsMeqFRP   float64 `json:"viirs_meq_frp"`
			HarmonizedFRP float64 `json:"harmonized_frp"`
		}
		var out []bucket6h
		for rows.Next() {
			var bts time.Time
			var b bucket6h
			if err := rows.Scan(&bts, &b.ModisPx, &b.ViirsPx, &b.CellsUnion, &b.ModisFRP, &b.ViirsMeqFRP, &b.HarmonizedFRP); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to scan 6h bucket")
				return
			}
			b.BucketTS = bts.UTC().Format(time.RFC3339)
			b.ModisFRP = round2(b.ModisFRP)
			b.ViirsMeqFRP = round2(b.ViirsMeqFRP)
			b.HarmonizedFRP = round2(b.HarmonizedFRP)
			out = append(out, b)
		}
		if err := rows.Err(); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to iterate 6h timeline")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"region": f.RegionID,
			"from":   f.From.Format("2006-01-02"),
			"to":     f.To.Format("2006-01-02"),
			"bucket": "6h",
			"series": out,
		})

	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid bucket %q: expected day or 6h", bucket))
	}
}

func (s *Server) handleHourly(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, err := s.parseFilter(ctx, r, "live", 2)
	if err != nil {
		writeHTTPError(w, err)
		return
	}

	bboxExpr, bboxArgs := f.bboxSQL("geom", 4)
	args := append([]any{f.From, f.To, f.ConfLevel}, bboxArgs...)

	sql := fmt.Sprintf(`
		SELECT
			EXTRACT(HOUR FROM acq_ts AT TIME ZONE 'UTC')::int AS hr,
			COUNT(*) FILTER (WHERE sensor = 'MODIS')::int AS modis_px,
			COUNT(*) FILTER (WHERE sensor = 'VIIRS')::int AS viirs_px,
			COALESCE(SUM(frp_mw) FILTER (WHERE sensor = 'MODIS'), 0)::double precision AS modis_frp,
			COALESCE(SUM(frp_mw) FILTER (WHERE sensor = 'VIIRS'), 0)::double precision AS viirs_frp,
			COUNT(*) FILTER (WHERE sensor = 'MODIS' AND daynight = 'D')::int AS modis_day_px,
			COUNT(*) FILTER (WHERE sensor = 'MODIS' AND daynight = 'N')::int AS modis_night_px,
			COUNT(*) FILTER (WHERE sensor = 'VIIRS' AND daynight = 'D')::int AS viirs_day_px,
			COUNT(*) FILTER (WHERE sensor = 'VIIRS' AND daynight = 'N')::int AS viirs_night_px
		FROM raw_detections
		WHERE acq_date BETWEEN $1::date AND $2::date
			AND conf_rank >= $3
			AND %s
		GROUP BY hr
		ORDER BY hr
	`, bboxExpr)

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query hourly cycle")
		return
	}
	defer rows.Close()

	type hourBucket struct {
		Hour          int     `json:"hour"`
		ModisPx       int     `json:"modis_px"`
		ViirsPx       int     `json:"viirs_px"`
		ModisFRP      float64 `json:"modis_frp"`
		ViirsFRP      float64 `json:"viirs_frp"`
		ModisDayPx    int     `json:"modis_day_px"`
		ModisNightPx  int     `json:"modis_night_px"`
		ViirsDayPx    int     `json:"viirs_day_px"`
		ViirsNightPx  int     `json:"viirs_night_px"`
	}

	buckets := make([]hourBucket, 24)
	for h := 0; h < 24; h++ {
		buckets[h].Hour = h
	}
	for rows.Next() {
		var b hourBucket
		if err := rows.Scan(
			&b.Hour, &b.ModisPx, &b.ViirsPx, &b.ModisFRP, &b.ViirsFRP,
			&b.ModisDayPx, &b.ModisNightPx, &b.ViirsDayPx, &b.ViirsNightPx,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan hourly bucket")
			return
		}
		if b.Hour >= 0 && b.Hour < 24 {
			b.ModisFRP = round1(b.ModisFRP)
			b.ViirsFRP = round1(b.ViirsFRP)
			buckets[b.Hour] = b
		}
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to iterate hourly buckets")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"region":     f.RegionID,
		"from":       f.From.Format("2006-01-02"),
		"to":         f.To.Format("2006-01-02"),
		"conf_level": f.ConfLevel,
		"hours":      buckets,
	})
}

func (s *Server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("mode")))
	if mode != "" && mode != "live" && mode != "history" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid mode %q: expected live or history", mode))
		return
	}
	defaultScope := "live"
	if mode == "history" {
		defaultScope = "history"
	}

	f, err := s.parseFilter(ctx, r, defaultScope, 2)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	if mode == "" {
		mode = f.Scope
	}

	// For history calendar without explicit from/to, include all covered archive + live dates in the region.
	q := r.URL.Query()
	if mode == "history" && strings.TrimSpace(q.Get("from")) == "" && strings.TrimSpace(q.Get("to")) == "" && f.RegionID != "custom" {
		var minD, maxD *time.Time
		if err := s.pool.QueryRow(ctx, `
			SELECT MIN(acq_date), MAX(acq_date)
			FROM region_day
			WHERE region_id = $1 AND conf_level = $2
		`, f.RegionID, f.ConfLevel).Scan(&minD, &maxD); err == nil && minD != nil && maxD != nil {
			f.From = truncUTC(*minD)
			f.To = truncUTC(*maxD)
		}
	} else if mode == "live" && strings.TrimSpace(q.Get("from")) == "" && strings.TrimSpace(q.Get("to")) == "" {
		// In live strip mode show up to trailing 30 days available in the region.
		f.From = f.To.AddDate(0, 0, -29)
	}

	_, paramsVersion := s.activeFloor(ctx)
	rawDaily, unionByDay, err := s.queryDailyBreakdown(ctx, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load calendar series")
		return
	}
	calModel, err := s.loadOrFitCalibration(ctx, f, rawDaily, paramsVersion)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load calibration")
		return
	}

	computeZ := mode == "history" || f.HasHistory
	pts := series.BuildDailySeries(rawDaily, calModel.KApplied, computeZ)

	type yearSummary struct {
		Year     int     `json:"year"`
		Days     int     `json:"days"`
		TotalFRP float64 `json:"total_frp_mw"`
		MaxZ     float64 `json:"max_z"`
		Era      string  `json:"era"`
	}
	type eraBracket struct {
		Era      string `json:"era"`
		FromYear int    `json:"from_year"`
		ToYear   int    `json:"to_year"`
	}

	yearMap := make(map[int]*yearSummary)
	yearSensors := make(map[int]struct{ hasM, hasV bool; maxVSats int16 })
	daysOut := make([]dailySummaryDTO, 0, len(pts))

	for _, pt := range pts {
		dKey := pt.Date.Format("2006-01-02")
		var zRounded *float64
		if pt.RobustZ != nil && !math.IsNaN(*pt.RobustZ) && !math.IsInf(*pt.RobustZ, 0) {
			zv := round2(*pt.RobustZ)
			zRounded = &zv
		}

		unionVal := unionByDay[dKey]
		if len(unionByDay) == 0 {
			unionVal = pt.HarmonizedFRP
		}

		daysOut = append(daysOut, dailySummaryDTO{
			Date:          dKey,
			ModisFRP:      pt.ModisFRP,
			ViirsAllFRP:   pt.ViirsAllFRP,
			ViirsMeqFRP:   pt.ViirsMeqFRP,
			HarmonizedFRP: round2(pt.HarmonizedFRP),
			CellUnionFRP:  round2(unionVal),
			RobustZ:       zRounded,
			ModisPx:       pt.ModisPx,
			ViirsPx:       pt.ViirsPx,
			CellsModis:    pt.CellsModis,
			CellsViirsMeq: pt.CellsViirsMeq,
			CellsUnion:    pt.CellsUnion,
			SatsModis:     pt.SatsModis,
			SatsViirs:     pt.SatsViirs,
			IsPartial:     pt.IsPartial,
		})

		y := pt.Date.Year()
		ys, ok := yearMap[y]
		if !ok {
			ys = &yearSummary{Year: y}
			yearMap[y] = ys
		}
		ys.Days++
		ys.TotalFRP += pt.HarmonizedFRP
		if zRounded != nil && *zRounded > ys.MaxZ {
			ys.MaxZ = *zRounded
		}

		st := yearSensors[y]
		if pt.ModisFRP != nil {
			st.hasM = true
		}
		if pt.ViirsMeqFRP != nil {
			st.hasV = true
		}
		if pt.SatsViirs > st.maxVSats {
			st.maxVSats = pt.SatsViirs
		}
		yearSensors[y] = st
	}

	years := make([]int, 0, len(yearMap))
	for y := range yearMap {
		years = append(years, y)
	}
	sort.Ints(years)

	yearList := make([]yearSummary, 0, len(years))
	var brackets []eraBracket
	for _, y := range years {
		ys := yearMap[y]
		ys.TotalFRP = round1(ys.TotalFRP)
		ys.MaxZ = round2(ys.MaxZ)

		st := yearSensors[y]
		switch {
		case st.hasM && !st.hasV:
			ys.Era = "MODIS Only"
		case !st.hasM && st.hasV:
			ys.Era = "VIIRS Only"
		case st.maxVSats >= 2 || y >= 2018:
			ys.Era = "MODIS + VIIRS Constellation"
		default:
			ys.Era = "MODIS + S-NPP"
		}
		yearList = append(yearList, *ys)

		if len(brackets) > 0 && brackets[len(brackets)-1].Era == ys.Era {
			brackets[len(brackets)-1].ToYear = y
		} else {
			brackets = append(brackets, eraBracket{Era: ys.Era, FromYear: y, ToYear: y})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"region":       f.RegionID,
		"region_name":  f.RegionName,
		"mode":         mode,
		"has_history":  f.HasHistory,
		"from":         f.From.Format("2006-01-02"),
		"to":           f.To.Format("2006-01-02"),
		"conf_level":   f.ConfLevel,
		"calibration":  toCalibrationDTO(calModel),
		"era_brackets": brackets,
		"years":        yearList,
		"days":         daysOut,
	})
}

func (s *Server) handleCell(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rawID := strings.TrimSpace(chi.URLParam(r, "id"))
	cellID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || cellID < 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid cell id %q", rawID))
		return
	}

	f, err := s.parseFilter(ctx, r, "live", 1)
	if err != nil {
		writeHTTPError(w, err)
		return
	}

	floorMW, _ := s.activeFloor(ctx)
	lat0, lon0, lat1, lon1 := grid.Bounds(cellID, grid.ResA1)
	clat, clon := grid.Center(cellID, grid.ResA1)
	cellA10 := grid.CellID(clat, clon, grid.ResA10)
	areaKm2 := grid.AreaKm2(cellID, grid.ResA1)

	type rawDetRow struct {
		ID            int64     `json:"id"`
		Sensor        string    `json:"sensor"`
		Satellite     string    `json:"satellite"`
		Lat           float64   `json:"lat"`
		Lon           float64   `json:"lon"`
		BTMwirK       float64   `json:"bt_mwir_k"`
		BTLwirK       float64   `json:"bt_lwir_k"`
		ScanKm        float64   `json:"scan_km"`
		TrackKm       float64   `json:"track_km"`
		AcqTS         time.Time `json:"acq_ts"`
		AcqDate       string    `json:"acq_date"`
		ConfRank      int16     `json:"conf_rank"`
		ConfRaw       string    `json:"conf_raw"`
		FRPMW         float64   `json:"frp_mw"`
		DayNight      string    `json:"daynight"`
		Status        string    `json:"status"`
		Version       string    `json:"version"`
		OverpassFRPMW float64   `json:"overpass_frp_mw"`
		Kept          bool      `json:"kept"`
	}

	rows, err := s.pool.Query(ctx, `
		SELECT
			id,
			sensor::text,
			satellite::text,
			(lat_e5::double precision / 100000.0),
			(lon_e5::double precision / 100000.0),
			bt_mwir_k::double precision,
			bt_lwir_k::double precision,
			scan_km::double precision,
			track_km::double precision,
			acq_ts,
			acq_date,
			conf_rank,
			conf_raw,
			frp_mw::double precision,
			daynight::text,
			status::text,
			version
		FROM raw_detections
		WHERE cell_a1 = $1
			AND acq_date BETWEEN $2::date AND $3::date
			AND conf_rank >= $4
		ORDER BY acq_ts DESC, frp_mw DESC
		LIMIT 200
	`, cellID, f.From, f.To, f.ConfLevel)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cell detections")
		return
	}
	defer rows.Close()

	type ovKey struct {
		date      string
		satellite string
		sensor    string
		daynight  string
	}
	ovSums := make(map[ovKey]float64)
	dets := make([]rawDetRow, 0, 32)

	for rows.Next() {
		var d rawDetRow
		var acqDate time.Time
		if err := rows.Scan(
			&d.ID, &d.Sensor, &d.Satellite, &d.Lat, &d.Lon,
			&d.BTMwirK, &d.BTLwirK, &d.ScanKm, &d.TrackKm,
			&d.AcqTS, &acqDate, &d.ConfRank, &d.ConfRaw,
			&d.FRPMW, &d.DayNight, &d.Status, &d.Version,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan detection")
			return
		}
		d.AcqDate = acqDate.UTC().Format("2006-01-02")
		d.FRPMW = round2(d.FRPMW)
		key := ovKey{d.AcqDate, d.Satellite, d.Sensor, d.DayNight}
		ovSums[key] += d.FRPMW
		dets = append(dets, d)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to iterate detections")
		return
	}

	for i := range dets {
		d := &dets[i]
		key := ovKey{d.AcqDate, d.Satellite, d.Sensor, d.DayNight}
		d.OverpassFRPMW = round2(ovSums[key])
		d.Kept = d.Sensor == "MODIS" || ovSums[key] >= floorMW
	}

	var modisPx, viirsPx int
	var avgModisFRP, avgViirsAll, avgViirsMeq, avgHarm *float64
	_ = s.pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(modis_px), 0)::int,
			COALESCE(SUM(viirs_px), 0)::int,
			AVG(modis_frp)::double precision,
			AVG(viirs_frp_all)::double precision,
			AVG(viirs_frp_meq)::double precision,
			AVG(harmonized_frp)::double precision
		FROM cell_day_a1
		WHERE cell_a1 = $1
			AND acq_date BETWEEN $2::date AND $3::date
			AND conf_level = $4
	`, cellID, f.From, f.To, f.ConfLevel).Scan(&modisPx, &viirsPx, &avgModisFRP, &avgViirsAll, &avgViirsMeq, &avgHarm)

	writeJSON(w, http.StatusOK, map[string]any{
		"cell_a1":   cellID,
		"cell_a10":  cellA10,
		"center":    [2]float64{round4(clat), round4(clon)},
		"bounds":    [4]float64{round4(lon0), round4(lat0), round4(lon1), round4(lat1)},
		"area_km2":  round2(areaKm2),
		"from":      f.From.Format("2006-01-02"),
		"to":        f.To.Format("2006-01-02"),
		"conf_level": f.ConfLevel,
		"floor_mw":  floorMW,
		"summary": map[string]any{
			"modis_px":       modisPx,
			"viirs_px":       viirsPx,
			"modis_frp":      avgModisFRP,
			"viirs_frp_all":  avgViirsAll,
			"viirs_frp_meq":  avgViirsMeq,
			"harmonized_frp": avgHarm,
		},
		"detections": dets,
	})
}

func (s *Server) handleAdminIngest(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminToken == "" {
		writeError(w, http.StatusForbidden, "admin ingest endpoint is disabled")
		return
	}

	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AdminToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid bearer token")
		return
	}

	if !s.adminLimiter.Allow() {
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded: retry in 1 minute")
		return
	}
	if s.poller == nil || s.cfg.FirmsMapKey == "" {
		writeError(w, http.StatusBadRequest, "FIRMS_MAP_KEY is not configured")
		return
	}

	rep, err := s.poller.RunOnce(r.Context())
	if err != nil {
		if errors.Is(err, sched.ErrPollInProgress) {
			writeError(w, http.StatusConflict, "NRT poll already in progress")
			return
		}
		writeError(w, http.StatusBadGateway, s.cfg.Redact(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) activeFloor(ctx context.Context) (float64, int) {
	var version int
	var floor float64
	if err := s.pool.QueryRow(ctx, `
		SELECT version, floor_mw::double precision
		FROM harmonization_params
		ORDER BY version DESC
		LIMIT 1
	`).Scan(&version, &floor); err != nil {
		return 6.2, 1
	}
	return round2(floor), version
}

func (s *Server) queryA10Agreement(ctx context.Context, f queryFilter) (agreementDTO, error) {
	bboxExpr, bboxArgs := f.bboxSQL("a.pt", 4)
	args := append([]any{f.From, f.To, f.ConfLevel}, bboxArgs...)

	partialJoin := `
		LEFT JOIN (
			SELECT acq_date, BOOL_OR(is_partial) AS is_partial
			FROM coverage_days
			WHERE area_id = 'world'
			GROUP BY acq_date
		) cov ON cov.acq_date = a.acq_date
	`
	partialCol := "COALESCE(cov.is_partial, FALSE)"
	if f.RegionID != "custom" {
		partialJoin = fmt.Sprintf(`
			LEFT JOIN region_day rd
				ON rd.region_id = '%s'
				AND rd.acq_date = a.acq_date
				AND rd.conf_level = $3
		`, strings.ReplaceAll(f.RegionID, "'", "''"))
		partialCol = "COALESCE(rd.is_partial, FALSE)"
	}

	sql := fmt.Sprintf(`
		WITH slice AS MATERIALIZED (
			SELECT acq_date, modis_px, viirs_px, pt
			FROM cell_day_a10
			WHERE acq_date BETWEEN $1::date AND $2::date
				AND conf_level = $3
		),
		base AS (
			SELECT
				a.acq_date,
				(a.modis_px > 0) AS has_m,
				(a.viirs_px > 0) AS has_v,
				%s AS is_partial
			FROM slice a
			%s
			WHERE %s
		)
		SELECT
			COUNT(*) FILTER (WHERE has_m)::int,
			COUNT(*) FILTER (WHERE has_m AND has_v)::int,
			COUNT(*) FILTER (WHERE has_m AND NOT is_partial)::int,
			COUNT(*) FILTER (WHERE has_m AND has_v AND NOT is_partial)::int
		FROM base
	`, partialCol, partialJoin, bboxExpr)

	var res agreementDTO
	if err := s.pool.QueryRow(ctx, sql, args...).Scan(
		&res.ModisCellDays, &res.BothCellDays,
		&res.FullDayModisCells, &res.FullDayBothCells,
	); err != nil {
		return res, err
	}
	res.OverallPct = round1(100.0 * safeRatio(float64(res.BothCellDays), float64(res.ModisCellDays)))
	res.FullDayPct = round1(100.0 * safeRatio(float64(res.FullDayBothCells), float64(res.FullDayModisCells)))
	return res, nil
}

func (s *Server) queryDailyBreakdown(
	ctx context.Context,
	f queryFilter,
) ([]series.RegionDayInput, map[string]float64, error) {
	unionByDay := make(map[string]float64)
	bboxExpr, bboxArgs := f.bboxSQL("pt", 4)
	unionArgs := append([]any{f.From, f.To, f.ConfLevel}, bboxArgs...)

	if f.RegionID == "custom" || f.To.Sub(f.From) <= 31*24*time.Hour {
		unionSQL := fmt.Sprintf(`
			WITH slice AS MATERIALIZED (
				SELECT acq_date, harmonized_frp, pt
				FROM cell_day_a1
				WHERE acq_date BETWEEN $1::date AND $2::date
					AND conf_level = $3
			)
			SELECT acq_date, COALESCE(SUM(harmonized_frp), 0)::double precision
			FROM slice
			WHERE %s
			GROUP BY acq_date
		`, bboxExpr)

		uRows, err := s.pool.Query(ctx, unionSQL, unionArgs...)
		if err != nil {
			return nil, nil, err
		}
		for uRows.Next() {
			var d time.Time
			var v float64
			if err := uRows.Scan(&d, &v); err != nil {
				uRows.Close()
				return nil, nil, err
			}
			unionByDay[d.UTC().Format("2006-01-02")] = v
		}
		uRows.Close()
		if err := uRows.Err(); err != nil {
			return nil, nil, err
		}
	}

	if f.RegionID != "custom" {
		rows, err := s.pool.Query(ctx, `
			SELECT
				acq_date,
				m_frp::double precision,
				v_all_frp::double precision,
				v_meq_frp::double precision,
				modis_px,
				viirs_px,
				cells_modis,
				cells_viirs_meq,
				cells_union,
				sats_modis,
				sats_viirs,
				is_partial
			FROM region_day
			WHERE region_id = $1
				AND conf_level = $2
				AND acq_date BETWEEN $3::date AND $4::date
			ORDER BY acq_date
		`, f.RegionID, f.ConfLevel, f.From, f.To)
		if err != nil {
			return nil, nil, err
		}
		defer rows.Close()

		var out []series.RegionDayInput
		for rows.Next() {
			var item series.RegionDayInput
			var d time.Time
			if err := rows.Scan(
				&d,
				&item.ModisFRP, &item.ViirsAllFRP, &item.ViirsMeqFRP,
				&item.ModisPx, &item.ViirsPx,
				&item.CellsModis, &item.CellsViirsMeq, &item.CellsUnion,
				&item.SatsModis, &item.SatsViirs, &item.IsPartial,
			); err != nil {
				return nil, nil, err
			}
			item.Date = truncUTC(d)
			out = append(out, item)
		}
		return out, unionByDay, rows.Err()
	}

	// Custom bbox fallback aggregated dynamically from cell_day_a1.
	customSQL := fmt.Sprintf(`
		WITH slice AS MATERIALIZED (
			SELECT acq_date, modis_frp, viirs_frp_all, viirs_frp_meq, harmonized_frp, modis_px, viirs_px, pt
			FROM cell_day_a1
			WHERE acq_date BETWEEN $1::date AND $2::date
				AND conf_level = $3
		)
		SELECT
			c.acq_date,
			SUM(c.modis_frp)::double precision,
			SUM(c.viirs_frp_all)::double precision,
			SUM(c.viirs_frp_meq)::double precision,
			COALESCE(SUM(c.modis_px), 0)::int,
			COALESCE(SUM(c.viirs_px), 0)::int,
			COUNT(*) FILTER (WHERE c.modis_frp IS NOT NULL)::int,
			COUNT(*) FILTER (WHERE c.viirs_frp_meq IS NOT NULL)::int,
			COUNT(*) FILTER (WHERE c.harmonized_frp IS NOT NULL)::int,
			COALESCE(cov.is_partial, FALSE)
		FROM slice c
		LEFT JOIN (
			SELECT acq_date, BOOL_OR(is_partial) AS is_partial
			FROM coverage_days
			WHERE area_id = 'world'
			GROUP BY acq_date
		) cov ON cov.acq_date = c.acq_date
		WHERE %s
		GROUP BY c.acq_date, cov.is_partial
		ORDER BY c.acq_date
	`, bboxExpr)

	cRows, err := s.pool.Query(ctx, customSQL, unionArgs...)
	if err != nil {
		return nil, nil, err
	}
	defer cRows.Close()

	var out []series.RegionDayInput
	for cRows.Next() {
		var item series.RegionDayInput
		var d time.Time
		if err := cRows.Scan(
			&d,
			&item.ModisFRP, &item.ViirsAllFRP, &item.ViirsMeqFRP,
			&item.ModisPx, &item.ViirsPx,
			&item.CellsModis, &item.CellsViirsMeq, &item.CellsUnion,
			&item.IsPartial,
		); err != nil {
			return nil, nil, err
		}
		item.Date = truncUTC(d)
		out = append(out, item)
	}
	return out, unionByDay, cRows.Err()
}

func (s *Server) loadOrFitCalibration(
	ctx context.Context,
	f queryFilter,
	daily []series.RegionDayInput,
	paramsVersion int,
) (series.CalibrationModel, error) {
	if f.RegionID != "custom" {
		var m series.CalibrationModel
		err := s.pool.QueryRow(ctx, `
			SELECT scope, k, ci_low, ci_high, n_days, reliable, params_version
			FROM calibration_models
			WHERE region_id = $1 AND (scope = $2 OR reliable = TRUE)
			ORDER BY (scope = $2 AND reliable) DESC, reliable DESC, (scope = $2) DESC
			LIMIT 1
		`, f.RegionID, f.Scope).Scan(&m.Scope, &m.K, &m.CILow, &m.CIHigh, &m.NDays, &m.Reliable, &m.ParamsVersion)
		if err == nil {
			m.RegionID = f.RegionID
			if m.Reliable && m.K > 0 {
				m.KApplied = m.K
			} else {
				m.KApplied = 1.0
			}
			return m, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return m, err
		}
	}

	var samples []series.OverlapSample
	for _, d := range daily {
		if !d.IsPartial && d.ModisFRP != nil && d.ViirsMeqFRP != nil && *d.ModisFRP > 0 && *d.ViirsMeqFRP > 0 {
			samples = append(samples, series.OverlapSample{
				Date:        d.Date,
				ModisFRP:    *d.ModisFRP,
				ViirsMeqFRP: *d.ViirsMeqFRP,
			})
		}
	}
	return series.FitCalibration(f.RegionID, f.Scope, paramsVersion, samples), nil
}

func toCalibrationDTO(m series.CalibrationModel) calibrationDTO {
	label := fmt.Sprintf("Gain: 1.00x (Unverified - %d overlap days < %d)", m.NDays, series.MinCalibrationDays)
	if m.Reliable {
		label = fmt.Sprintf("Gain: %.2fx (95%% CI %.2f-%.2f, n=%d days)", m.K, m.CILow, m.CIHigh, m.NDays)
	} else if m.NDays >= series.MinCalibrationDays {
		label = fmt.Sprintf("Gain: 1.00x (Wide CI %.2f-%.2f, n=%d days)", m.CILow, m.CIHigh, m.NDays)
	}
	return calibrationDTO{
		Scope:         m.Scope,
		K:             round4(m.K),
		CILow:         round4(m.CILow),
		CIHigh:        round4(m.CIHigh),
		NDays:         m.NDays,
		Reliable:      m.Reliable,
		KApplied:      round4(m.KApplied),
		ParamsVersion: m.ParamsVersion,
		Label:         label,
	}
}

func formatOptDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format("2006-01-02")
	return &s
}

func safeRatio(num, den float64) float64 {
	if den <= 0 || math.IsNaN(num) || math.IsNaN(den) || math.IsInf(num, 0) || math.IsInf(den, 0) {
		return 0
	}
	r := num / den
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return 0
	}
	return r
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
