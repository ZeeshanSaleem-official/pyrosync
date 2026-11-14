# ADR 0002: Decoupled Archive Seeding and CARTO Dark Matter Basemap

## Status
Accepted

## Context
NASA FIRMS NRT API endpoints enforce a 5,000-transaction / 10-minute rate limit per `MAP_KEY` and serve only recent rolling windows (~2-3 months), without historical pre-launch years such as 2013 NOAA-20/21. At the same time, the application requires both a multi-year Burning Activity Calendar and a live 48h/30d map that never blocks on NASA API quotas.

## Decision
1. **Decouple Ingestion Paths:**
   - Static 48-hour global files and multi-year Standard Processing (`SP`) archive CSVs (2010-2025) are ingested offline/out-of-band into PostgreSQL via `pyrosync ingest-file` / `ingest-dir`.
   - The Go backend runs an optional background NRT poller (`MODIS_NRT`, `VIIRS_SNPP_NRT`, `VIIRS_NOAA20_NRT`, `VIIRS_NOAA21_NRT`) on a 60-minute interval (`day_range=1`), consuming 4 requests/hour.
   - The Next.js frontend communicates exclusively with the Go REST/MVT API.
2. **CARTO Dark Matter Basemap:**
   - Use CARTO Dark Matter `@2x` raster tiles in MapLibre GL JS with no API key required and explicit OpenStreetMap + CARTO attribution.

## Consequences
- Frontend panning, filtering, and multi-year calendar queries execute in milliseconds against PostgreSQL/PostGIS with zero risk of hitting FIRMS rate limits.
- Historical archive rows use `status = 'SP'` and automatically upgrade matching `NRT` rows during upsert.
