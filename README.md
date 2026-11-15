# PyroSync

**Harmonized MODIS & VIIRS Active-Fire Explorer and Burning Activity Calendar**

- **Live Explorer:** https://pyrosync.earth

PyroSync reconciles multi-sensor satellite thermal anomaly records across legacy **MODIS** (`1.0 km` nadir pixels; Terra & Aqua) and higher-resolution **VIIRS** (`375 m` pixels; Suomi-NPP, NOAA-20, NOAA-21) so fire activity and seasonal anomalies remain comparable as MODIS approaches end of mission.

## Method Summary

Raw VIIRS detections outnumber MODIS by roughly `4.76x` (Nominal+High confidence) because VIIRS resolves multiple sub-pixel hot spots inside a single MODIS footprint and detects cooler fires below MODIS's sensitivity threshold. PyroSync addresses this in four deterministic steps:

1. **Schema & Quality Standardization:** Maps MODIS (`brightness`, `bright_t31`, `0-100` confidence) and VIIRS (`bright_ti4`, `bright_ti5`, `low/nominal/high` or `l/n/h`) into a canonical UTC schema with integer coordinate keys (`lat_e5`, `lon_e5`) and Standard Processing (`SP`) upgrades over Near Real-Time (`NRT`) records.
2. **Equal-Area Cosine Grid (`A1` & `A10`):** Aggregates detections onto latitude-band equal-area cells (`~1.23 km²` fine scale and `~123 km²` regional scale) without high-latitude distortion.
3. **Overpass-Cell Rollup & MODIS Sensitivity Floor (`6.2 MW`):** Groups detections per `(cell, satellite, UTC date, day/night)` overpass, filters VIIRS overpass-cells below the empirical MODIS 10th-percentile radiative floor (`6.2 MW`), and averages across observing satellites to prevent multi-pass double counting.
4. **Cross-Era Gain & Seasonal Anomaly Baseline:** Computes bootstrapped cross-sensor gain (`k`) where sufficient overlap exists (`>= 14` full days) and evaluates robust Z-scores against multi-year seasonal windows (`doy +/- 7 days`) from ingested FIRMS historical archives.

## Stack

- **Backend & CLI:** Go 1.25 (`chi`, `pgx/v5`, `golang-migrate`)
- **Spatial Database:** PostgreSQL 16 + PostGIS 3.4 (`ST_AsMVT` vector tiles)
- **Frontend:** Next.js 15 (TypeScript, static export) + MapLibre GL JS + Esri World Dark Gray Canvas + custom PostGIS MVT vector layers
- **Deployment:** Docker Compose + Caddy

## Quick Start (Local Development)

### 1. Start PostGIS
```powershell
docker compose up -d db
```

### 2. Run Migrations, Ingestion & API Server (Go)
```powershell
copy .env.example .env
go build -o bin/pyrosync.exe ./cmd/pyrosync
.\bin\pyrosync.exe migrate
.\bin\pyrosync.exe ingest-dir --area world data\input
.\bin\pyrosync.exe ingest-dir --area pakistan-indus data\archive
.\bin\pyrosync.exe recompute --all
.\bin\pyrosync.exe calibrate
.\bin\pyrosync.exe serve --addr 127.0.0.1:8080
```
If `FIRMS_MAP_KEY` is configured in `.env`, run live NASA FIRMS NRT ingestion on demand (`.\bin\pyrosync.exe ingest-api`) or let `serve` poll automatically on startup/interval (`INGEST_INTERVAL_MIN=60`).

### 3. Build & Run Frontend (Next.js)
```powershell
cd web
npm install
npm run dev
```

### 4. Production Deployment (Docker Compose + Caddy)
Build the static frontend export into `web/out`, configure `deploy/.env` from `deploy/.env.example`, and start PostGIS, the Go API, and Caddy:
```bash
cd web && npm ci && NEXT_PUBLIC_API_BASE=/api/v1 npm run build && cd ..
cp deploy/.env.example deploy/.env
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d --build
```

## Golden Value Verification (`go test ./...`)

| Check | Expected Value |
|---|---|
| `raw_detections` count (seed corpus) | `971,896` (`+ NRT API rows` when live polling is enabled) |
| Global 24h raw pixel ratio (`VIIRS / MODIS`, `conf >= 2`) | `4.76x` (`85,172 / 17,909`) |
| Global 24h equal-area `A1` cell ratio (`conf >= 2`) | `2.65x` (`44,067 / 16,608`) |
| Global 24h floor-aligned `A1` cell ratio (`>= 6.2 MW`, `conf >= 2`) | `1.12x` (`18,622 / 16,608`) |
| Global 24h floor-aligned FRP ratio (`VIIRS_meq / MODIS`, `conf >= 2`) | `0.81x` (`428,626.80 MW / 530,451.80 MW`) |
| Global `A10` full-day co-detection agreement (`2026-11-13`, `conf >= 2`) | `55.6%` (`1,778 / 3,200`) |
| Pakistan / Indus `k_applied` (`conf >= 2`) | `0.66x` (`n = 2,600` overlap days) |

## Space Agency & Data Attribution

- **NASA FIRMS / LANCE:** Active fire data provided by NASA's Land, Atmosphere Near real-time Capability for EOS (LANCE) Fire Information for Resource Management System (FIRMS), operated by the NASA/GSFC Earth Science Data and Information System (ESDIS), MODIS Collection 6.1 (`MCD14DL` / `MCD14ML`) and VIIRS 375 m Collection 2 (`VNP14IMGTDL_NRT`, `VJ114IMGTDL_NRT`, `VJ214IMGTDL_NRT`).
- **Basemap Tiles:** Esri, HERE, Garmin, OpenStreetMap contributors, and the GIS user community (World Dark Gray Canvas tiles).
- **AI Tool Transparency:** Architecture planning, code scaffolding, and documentation refinement were assisted by AI coding assistants. All scientific harmonization formulas, equal-area spatial aggregations, bootstrap confidence intervals, and validation metrics are deterministic and computed directly from official NASA FIRMS datasets in Go and PostgreSQL/PostGIS.

## License

MIT — see [LICENSE](LICENSE) and [THIRD_PARTY.md](THIRD_PARTY.md).
