# PyroSync

**Harmonized MODIS & VIIRS Active-Fire Explorer and Burning Activity Calendar**

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
- **Frontend:** Next.js 15 (TypeScript, static export) + MapLibre GL JS + CARTO Dark Matter
- **Deployment:** Docker Compose + Caddy

## Quick Start (Local Development)

### 1. Start PostGIS
```powershell
docker compose up -d db
```

### 2. Run Ingestion & Server (Go)
```powershell
copy .env.example .env
go run ./cmd/pyrosync version
```
*(Full ingestion, recompute, and server commands activate as Phases 1-3 are built.)*

### 3. Build & Run Frontend (Next.js)
```powershell
cd web
npm install
npm run dev
```

## Data Attribution

Active fire data provided by NASA's Land, Atmosphere Near real-time Capability for EOS (LANCE) Fire Information for Resource Management System (FIRMS), MODIS Collection 6.1 and VIIRS 375 m Collection 2 products. Basemap tiles by OpenStreetMap contributors and CARTO.

## License

MIT — see [LICENSE](LICENSE) and [THIRD_PARTY.md](THIRD_PARTY.md).
