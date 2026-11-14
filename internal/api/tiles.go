package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

const (
	cellsMinZoom      = 4
	detectionsMinZoom = 9
	maxTileZoom       = 22
	mvtExtent         = 4096
	mvtBuffer         = 64
)

func (s *Server) handleTilesCells10(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	z, x, y, mode, err := parseTileParams(r)
	if err != nil {
		writeHTTPError(w, err)
		return
	}

	f, err := s.parseFilter(ctx, r, "live", 2)
	if err != nil {
		writeHTTPError(w, err)
		return
	}

	release, ok := s.acquireTileSlot(ctx)
	if !ok {
		writeError(w, http.StatusRequestTimeout, "request cancelled while waiting for tile slot")
		return
	}
	defer release()

	bboxExpr, bboxArgs := f.bboxSQL("c.pt", 8)
	args := append([]any{z, x, y, f.From, f.To, f.ConfLevel, mode}, bboxArgs...)

	sql := fmt.Sprintf(`
		WITH bounds AS MATERIALIZED (
			SELECT
				ST_TileEnvelope($1::int, $2::int, $3::int) AS env3857,
				ST_Transform(ST_TileEnvelope($1::int, $2::int, $3::int), 4326) AS env4326
		),
		slice AS MATERIALIZED (
			SELECT cell_a10, harmonized_frp, modis_px, viirs_px, cells_harmonized, pt
			FROM cell_day_a10
			WHERE acq_date BETWEEN $4::date AND $5::date
				AND conf_level = $6
		),
		agg AS (
			SELECT
				c.cell_a10,
				COALESCE(SUM(c.harmonized_frp), 0)::double precision AS harmonized_frp,
				COALESCE(SUM(c.modis_px), 0)::int AS modis_px,
				COALESCE(SUM(c.viirs_px), 0)::int AS viirs_px,
				COALESCE(SUM(c.modis_px + c.viirs_px), 0)::int AS raw_px,
				COALESCE(MAX(c.cells_harmonized), 0)::int AS cells_harmonized
			FROM slice c
			CROSS JOIN bounds b
			WHERE c.pt && ST_Expand(b.env4326, 0.15)
				AND %s
			GROUP BY c.cell_a10
			HAVING ($7::text <> 'harmonized' OR COALESCE(SUM(c.harmonized_frp), 0) > 0 OR COALESCE(SUM(c.cells_harmonized), 0) > 0)
		),
		geom_cells AS (
			SELECT
				a.cell_a10,
				ROUND(a.harmonized_frp::numeric, 2)::double precision AS harmonized_frp,
				a.modis_px,
				a.viirs_px,
				a.raw_px,
				a.cells_harmonized,
				(a.harmonized_frp > 0) AS kept,
				$7::text AS sensor_mode,
				LEAST(180.0, GREATEST(-180.0, g.lon0)) AS lon0,
				LEAST(85.05, GREATEST(-85.05, g.lat0)) AS lat0,
				LEAST(180.0, GREATEST(-180.0, g.lon1)) AS lon1,
				LEAST(85.05, GREATEST(-85.05, g.lat1)) AS lat1
			FROM agg a
			CROSS JOIN LATERAL grid_bounds(a.cell_a10, 0.10) g
		),
		mvtgeom AS (
			SELECT
				gc.cell_a10,
				gc.harmonized_frp,
				gc.modis_px,
				gc.viirs_px,
				gc.raw_px,
				gc.cells_harmonized,
				gc.kept,
				gc.sensor_mode,
				ST_AsMVTGeom(
					ST_Transform(ST_MakeEnvelope(gc.lon0, gc.lat0, gc.lon1, gc.lat1, 4326), 3857),
					b.env3857,
					%d,
					%d,
					true
				) AS geom
			FROM geom_cells gc
			CROSS JOIN bounds b
			WHERE gc.lat1 > gc.lat0 AND gc.lon1 > gc.lon0
		)
		SELECT COALESCE(ST_AsMVT(q, 'cells10', %d, 'geom'), ''::bytea)
		FROM mvtgeom q
		WHERE q.geom IS NOT NULL
	`, bboxExpr, mvtExtent, mvtBuffer, mvtExtent)

	var tile []byte
	if err := s.pool.QueryRow(ctx, sql, args...).Scan(&tile); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to render cells10 tile")
		return
	}
	writeMVT(w, tile)
}

func (s *Server) handleTilesCells(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	z, x, y, mode, err := parseTileParams(r)
	if err != nil {
		writeHTTPError(w, err)
		return
	}

	f, err := s.parseFilter(ctx, r, "live", 2)
	if err != nil {
		writeHTTPError(w, err)
		return
	}

	if z < cellsMinZoom {
		writeMVT(w, nil)
		return
	}

	release, ok := s.acquireTileSlot(ctx)
	if !ok {
		writeError(w, http.StatusRequestTimeout, "request cancelled while waiting for tile slot")
		return
	}
	defer release()

	bboxExpr, bboxArgs := f.bboxSQL("c.pt", 8)
	args := append([]any{z, x, y, f.From, f.To, f.ConfLevel, mode}, bboxArgs...)

	sql := fmt.Sprintf(`
		WITH bounds AS MATERIALIZED (
			SELECT
				ST_TileEnvelope($1::int, $2::int, $3::int) AS env3857,
				ST_Transform(ST_TileEnvelope($1::int, $2::int, $3::int), 4326) AS env4326
		),
		slice AS MATERIALIZED (
			SELECT cell_a1, harmonized_frp, modis_frp, viirs_frp_meq, viirs_frp_all, modis_px, viirs_px, n_ov_modis, n_ov_viirs_meq, pt
			FROM cell_day_a1
			WHERE acq_date BETWEEN $4::date AND $5::date
				AND conf_level = $6
		),
		agg AS (
			SELECT
				c.cell_a1,
				COALESCE(SUM(c.harmonized_frp), 0)::double precision AS harmonized_frp,
				COALESCE(SUM(c.modis_frp), 0)::double precision AS modis_frp,
				COALESCE(SUM(c.viirs_frp_meq), 0)::double precision AS viirs_frp_meq,
				COALESCE(SUM(c.viirs_frp_all), 0)::double precision AS viirs_frp_all,
				COALESCE(SUM(c.modis_px), 0)::int AS modis_px,
				COALESCE(SUM(c.viirs_px), 0)::int AS viirs_px,
				COALESCE(SUM(c.modis_px + c.viirs_px), 0)::int AS raw_px,
				(COALESCE(SUM(c.n_ov_modis + c.n_ov_viirs_meq), 0) > 0) AS kept
			FROM slice c
			CROSS JOIN bounds b
			WHERE c.pt && ST_Expand(b.env4326, 0.02)
				AND %s
			GROUP BY c.cell_a1
			HAVING ($7::text <> 'harmonized' OR COALESCE(SUM(c.harmonized_frp), 0) > 0)
		),
		geom_cells AS (
			SELECT
				a.cell_a1,
				ROUND(a.harmonized_frp::numeric, 2)::double precision AS harmonized_frp,
				ROUND(a.modis_frp::numeric, 2)::double precision AS modis_frp,
				ROUND(a.viirs_frp_meq::numeric, 2)::double precision AS viirs_frp_meq,
				ROUND(a.viirs_frp_all::numeric, 2)::double precision AS viirs_frp_all,
				a.modis_px,
				a.viirs_px,
				a.raw_px,
				a.kept,
				$7::text AS sensor_mode,
				LEAST(180.0, GREATEST(-180.0, g.lon0)) AS lon0,
				LEAST(85.05, GREATEST(-85.05, g.lat0)) AS lat0,
				LEAST(180.0, GREATEST(-180.0, g.lon1)) AS lon1,
				LEAST(85.05, GREATEST(-85.05, g.lat1)) AS lat1
			FROM agg a
			CROSS JOIN LATERAL grid_bounds(a.cell_a1, 0.01) g
		),
		mvtgeom AS (
			SELECT
				gc.cell_a1,
				gc.harmonized_frp,
				gc.modis_frp,
				gc.viirs_frp_meq,
				gc.viirs_frp_all,
				gc.modis_px,
				gc.viirs_px,
				gc.raw_px,
				gc.kept,
				gc.sensor_mode,
				ST_AsMVTGeom(
					ST_Transform(ST_MakeEnvelope(gc.lon0, gc.lat0, gc.lon1, gc.lat1, 4326), 3857),
					b.env3857,
					%d,
					%d,
					true
				) AS geom
			FROM geom_cells gc
			CROSS JOIN bounds b
			WHERE gc.lat1 > gc.lat0 AND gc.lon1 > gc.lon0
		)
		SELECT COALESCE(ST_AsMVT(q, 'cells', %d, 'geom'), ''::bytea)
		FROM mvtgeom q
		WHERE q.geom IS NOT NULL
	`, bboxExpr, mvtExtent, mvtBuffer, mvtExtent)

	var tile []byte
	if err := s.pool.QueryRow(ctx, sql, args...).Scan(&tile); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to render cells tile")
		return
	}
	writeMVT(w, tile)
}

func (s *Server) handleTilesDetections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	z, x, y, mode, err := parseTileParams(r)
	if err != nil {
		writeHTTPError(w, err)
		return
	}

	f, err := s.parseFilter(ctx, r, "live", 2)
	if err != nil {
		writeHTTPError(w, err)
		return
	}

	if z < detectionsMinZoom {
		writeMVT(w, nil)
		return
	}

	release, ok := s.acquireTileSlot(ctx)
	if !ok {
		writeError(w, http.StatusRequestTimeout, "request cancelled while waiting for tile slot")
		return
	}
	defer release()

	floorMW, _ := s.activeFloor(ctx)
	bboxExpr, bboxArgs := f.bboxSQL("r.geom", 9)
	args := append([]any{z, x, y, f.From, f.To, f.ConfLevel, mode, floorMW}, bboxArgs...)

	sql := fmt.Sprintf(`
		WITH bounds AS (
			SELECT
				ST_TileEnvelope($1::int, $2::int, $3::int) AS env3857,
				ST_Transform(ST_TileEnvelope($1::int, $2::int, $3::int), 4326) AS env4326
		),
		dets AS (
			SELECT
				r.id,
				r.cell_a1,
				r.sensor::text AS sensor,
				r.satellite::text AS satellite,
				ROUND(r.frp_mw::numeric, 2)::double precision AS frp_mw,
				ROUND(r.bt_mwir_k::numeric, 2)::double precision AS bt_mwir_k,
				ROUND(r.bt_lwir_k::numeric, 2)::double precision AS bt_lwir_k,
				ROUND(r.scan_km::numeric, 3)::double precision AS scan_km,
				ROUND(r.track_km::numeric, 3)::double precision AS track_km,
				r.conf_rank,
				r.daynight,
				r.status::text AS status,
				to_char(r.acq_ts AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS acq_ts,
				(r.sensor = 'MODIS' OR r.frp_mw >= $8::double precision) AS kept,
				$7::text AS sensor_mode,
				(r.lat_e5::double precision / 100000.0) AS lat,
				(r.lon_e5::double precision / 100000.0) AS lon,
				GREATEST(0.05, r.track_km)::double precision / 2.0 / 110.574 AS dlat,
				GREATEST(0.05, r.scan_km)::double precision / 2.0 / (
					111.320 * GREATEST(0.05, COS(RADIANS(r.lat_e5::double precision / 100000.0)))
				) AS dlon
			FROM raw_detections r
			CROSS JOIN bounds b
			WHERE r.acq_date BETWEEN $4::date AND $5::date
				AND r.conf_rank >= $6
				AND r.geom && ST_Expand(b.env4326, 0.05)
				AND %s
				AND ($7::text <> 'harmonized' OR r.sensor = 'MODIS' OR r.frp_mw >= $8::double precision)
		),
		footprints AS (
			SELECT
				id, cell_a1, sensor, satellite, frp_mw, bt_mwir_k, bt_lwir_k,
				scan_km, track_km, conf_rank, daynight, status, acq_ts, kept, sensor_mode,
				LEAST(180.0, GREATEST(-180.0, lon - dlon)) AS lon0,
				LEAST(85.05, GREATEST(-85.05, lat - dlat)) AS lat0,
				LEAST(180.0, GREATEST(-180.0, lon + dlon)) AS lon1,
				LEAST(85.05, GREATEST(-85.05, lat + dlat)) AS lat1
			FROM dets
		),
		mvtgeom AS (
			SELECT
				id, cell_a1, sensor, satellite, frp_mw, bt_mwir_k, bt_lwir_k,
				scan_km, track_km, conf_rank, daynight, status, acq_ts, kept, sensor_mode,
				ST_AsMVTGeom(
					ST_Transform(ST_MakeEnvelope(lon0, lat0, lon1, lat1, 4326), 3857),
					b.env3857,
					%d,
					%d,
					true
				) AS geom
			FROM footprints
			CROSS JOIN bounds b
			WHERE lat1 > lat0 AND lon1 > lon0
		)
		SELECT COALESCE(ST_AsMVT(q, 'det', %d, 'geom'), ''::bytea)
		FROM mvtgeom q
		WHERE q.geom IS NOT NULL
	`, bboxExpr, mvtExtent, mvtBuffer, mvtExtent)

	var tile []byte
	if err := s.pool.QueryRow(ctx, sql, args...).Scan(&tile); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to render detections tile")
		return
	}
	writeMVT(w, tile)
}

func parseTileParams(r *http.Request) (z, x, y int, mode string, err error) {
	zStr := strings.TrimSpace(chi.URLParam(r, "z"))
	xStr := strings.TrimSpace(chi.URLParam(r, "x"))
	yStr := strings.TrimSuffix(strings.TrimSpace(chi.URLParam(r, "y")), ".mvt")

	z, err = strconv.Atoi(zStr)
	if err != nil || z < 0 || z > maxTileZoom {
		return 0, 0, 0, "", httpStatusError{http.StatusBadRequest, fmt.Sprintf("invalid tile z %q: expected 0..%d", zStr, maxTileZoom)}
	}
	maxCoord := int64(1) << z

	x64, err := strconv.ParseInt(xStr, 10, 64)
	if err != nil || x64 < 0 || x64 >= maxCoord {
		return 0, 0, 0, "", httpStatusError{http.StatusBadRequest, fmt.Sprintf("invalid tile x %q for z=%d", xStr, z)}
	}
	y64, err := strconv.ParseInt(yStr, 10, 64)
	if err != nil || y64 < 0 || y64 >= maxCoord {
		return 0, 0, 0, "", httpStatusError{http.StatusBadRequest, fmt.Sprintf("invalid tile y %q for z=%d", yStr, z)}
	}

	mode = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = "harmonized"
	}
	switch mode {
	case "harmonized", "split", "raw":
	default:
		return 0, 0, 0, "", httpStatusError{http.StatusBadRequest, fmt.Sprintf("invalid mode %q: expected harmonized, split, or raw", mode)}
	}

	return z, int(x64), int(y64), mode, nil
}

func (s *Server) acquireTileSlot(ctx context.Context) (func(), bool) {
	select {
	case s.tileSem <- struct{}{}:
		return func() { <-s.tileSem }, true
	case <-ctx.Done():
		return nil, false
	}
}

func writeMVT(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/vnd.mapbox-vector-tile")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	if len(data) > 0 {
		_, _ = w.Write(data)
	}
}
