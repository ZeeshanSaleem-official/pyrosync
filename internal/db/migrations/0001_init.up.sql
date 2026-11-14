CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TYPE sensor_family  AS ENUM ('MODIS', 'VIIRS');
CREATE TYPE satellite_code AS ENUM ('TERRA', 'AQUA', 'SNPP', 'NOAA20', 'NOAA21');
CREATE TYPE product_status AS ENUM ('NRT', 'SP');

CREATE TABLE raw_detections (
  id             BIGINT GENERATED ALWAYS AS IDENTITY,
  sensor         sensor_family  NOT NULL,
  satellite      satellite_code NOT NULL,
  lat_e5         INTEGER NOT NULL,
  lon_e5         INTEGER NOT NULL,
  geom           geometry(Point, 4326) NOT NULL,
  bt_mwir_k      REAL NOT NULL,
  bt_lwir_k      REAL NOT NULL,
  scan_km        REAL NOT NULL CHECK (scan_km > 0),
  track_km       REAL NOT NULL CHECK (track_km > 0),
  acq_ts         TIMESTAMPTZ NOT NULL,
  acq_date       DATE NOT NULL,
  conf_rank      SMALLINT NOT NULL CHECK (conf_rank BETWEEN 1 AND 3),
  conf_raw       TEXT NOT NULL,
  frp_mw         REAL NOT NULL CHECK (frp_mw >= 0),
  daynight       CHAR(1) NOT NULL CHECK (daynight IN ('D', 'N')),
  version        TEXT NOT NULL,
  status         product_status NOT NULL,
  source_type    SMALLINT NOT NULL DEFAULT 0,
  cell_a1        BIGINT NOT NULL,
  cell_a10       BIGINT NOT NULL,
  ingest_file_id BIGINT,
  PRIMARY KEY (satellite, lat_e5, lon_e5, acq_ts)
);

CREATE INDEX raw_date_idx ON raw_detections (acq_date);
CREATE INDEX raw_cell_idx ON raw_detections (cell_a1, acq_date);
CREATE INDEX raw_geom_idx ON raw_detections USING GIST (geom);
CREATE INDEX raw_ts_idx   ON raw_detections (acq_ts DESC);

CREATE TABLE ingest_files (
  id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  sha256          CHAR(64) NOT NULL UNIQUE,
  file_name       TEXT NOT NULL,
  schema_detected TEXT NOT NULL,
  rows_read       INT NOT NULL,
  rows_inserted   INT NOT NULL,
  rows_updated    INT NOT NULL,
  rows_ignored    INT NOT NULL,
  rows_rejected   INT NOT NULL,
  started_at      TIMESTAMPTZ NOT NULL,
  finished_at     TIMESTAMPTZ NOT NULL
);

CREATE TABLE ingest_rejects (
  id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  ingest_file_id BIGINT NOT NULL REFERENCES ingest_files(id) ON DELETE CASCADE,
  line_no        INT NOT NULL,
  reason         TEXT NOT NULL,
  raw_line       TEXT NOT NULL
);

CREATE TABLE ingest_runs (
  id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  kind        TEXT NOT NULL,
  source      TEXT,
  area        TEXT,
  started_at  TIMESTAMPTZ NOT NULL,
  finished_at TIMESTAMPTZ,
  rows_in     INT NOT NULL DEFAULT 0,
  status      TEXT NOT NULL,
  error       TEXT
);

CREATE TABLE coverage_days (
  area_id    TEXT NOT NULL,
  family     sensor_family NOT NULL,
  acq_date   DATE NOT NULL,
  is_partial BOOLEAN NOT NULL DEFAULT FALSE,
  PRIMARY KEY (area_id, family, acq_date)
);

CREATE TABLE harmonization_params (
  version    INT PRIMARY KEY,
  floor_mw   REAL NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  notes      TEXT
);

INSERT INTO harmonization_params (version, floor_mw, notes)
VALUES (1, 6.2, 'MODIS P10 per-pixel FRP at Nominal+High confidence (6.24 MW rounded)');

CREATE TABLE regions (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  sort_order   SMALLINT NOT NULL,
  west         DOUBLE PRECISION NOT NULL,
  south        DOUBLE PRECISION NOT NULL,
  east         DOUBLE PRECISION NOT NULL,
  north        DOUBLE PRECISION NOT NULL,
  geom         geometry(MultiPolygon, 4326) NOT NULL,
  has_history  BOOLEAN NOT NULL DEFAULT FALSE,
  history_from DATE,
  history_to   DATE
);

CREATE INDEX regions_geom_idx ON regions USING GIST (geom);

CREATE TABLE cell_day_a1 (
  acq_date       DATE NOT NULL,
  cell_a1        BIGINT NOT NULL,
  conf_level     SMALLINT NOT NULL,
  cell_a10       BIGINT NOT NULL,
  lat            REAL NOT NULL,
  lon            REAL NOT NULL,
  pt             geometry(Point, 4326) NOT NULL,
  modis_px       INT NOT NULL DEFAULT 0,
  viirs_px       INT NOT NULL DEFAULT 0,
  n_ov_modis     SMALLINT NOT NULL DEFAULT 0,
  n_ov_viirs_meq SMALLINT NOT NULL DEFAULT 0,
  modis_frp      REAL,
  viirs_frp_all  REAL,
  viirs_frp_meq  REAL,
  harmonized_frp REAL,
  params_version INT NOT NULL DEFAULT 1,
  PRIMARY KEY (acq_date, conf_level, cell_a1)
);

CREATE INDEX cd1_pt_idx       ON cell_day_a1 USING GIST (pt);
CREATE INDEX cd1_date_lvl_idx ON cell_day_a1 (acq_date, conf_level);
CREATE INDEX cd1_a10_idx      ON cell_day_a1 (acq_date, conf_level, cell_a10);

CREATE TABLE cell_day_a10 (
  acq_date         DATE NOT NULL,
  cell_a10         BIGINT NOT NULL,
  conf_level       SMALLINT NOT NULL,
  lat              REAL NOT NULL,
  lon              REAL NOT NULL,
  pt               geometry(Point, 4326) NOT NULL,
  modis_px         INT NOT NULL,
  viirs_px         INT NOT NULL,
  cells_harmonized INT NOT NULL,
  harmonized_frp   REAL NOT NULL,
  PRIMARY KEY (acq_date, conf_level, cell_a10)
);

CREATE INDEX cd10_pt_idx ON cell_day_a10 USING GIST (pt);

CREATE TABLE region_day (
  region_id       TEXT NOT NULL REFERENCES regions(id),
  acq_date        DATE NOT NULL,
  conf_level      SMALLINT NOT NULL,
  m_frp           REAL,
  v_all_frp       REAL,
  v_meq_frp       REAL,
  modis_px        INT NOT NULL DEFAULT 0,
  viirs_px        INT NOT NULL DEFAULT 0,
  cells_modis     INT NOT NULL DEFAULT 0,
  cells_viirs_meq INT NOT NULL DEFAULT 0,
  cells_union     INT NOT NULL DEFAULT 0,
  sats_modis      SMALLINT NOT NULL DEFAULT 0,
  sats_viirs      SMALLINT NOT NULL DEFAULT 0,
  is_partial      BOOLEAN NOT NULL DEFAULT FALSE,
  PRIMARY KEY (region_id, conf_level, acq_date)
);

CREATE TABLE calibration_models (
  region_id      TEXT NOT NULL,
  scope          TEXT NOT NULL CHECK (scope IN ('live', 'history')),
  k              DOUBLE PRECISION NOT NULL,
  ci_low         DOUBLE PRECISION NOT NULL,
  ci_high        DOUBLE PRECISION NOT NULL,
  n_days         INT NOT NULL,
  reliable       BOOLEAN NOT NULL,
  params_version INT NOT NULL DEFAULT 1,
  fitted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (region_id, scope)
);

CREATE TABLE aggregate_dirty (
  acq_date  DATE PRIMARY KEY,
  marked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
