# NASA FIRMS & Basemap Verification Notes

## 1. Active Fire Data Sources & Rate Limits
- **API Endpoint Pattern:** `https://firms.modaps.eosdis.nasa.gov/api/area/csv/{MAP_KEY}/{SOURCE}/{AREA}/{DAY_RANGE}[/{DATE}]`
- **NRT Source Codes:**
  - `MODIS_NRT` (Terra and Aqua combined)
  - `VIIRS_SNPP_NRT` (Suomi-NPP, launched 2011)
  - `VIIRS_NOAA20_NRT` (NOAA-20 / JPSS-1, launched 2017)
  - `VIIRS_NOAA21_NRT` (NOAA-21 / JPSS-2, launched 2022)
- **Rate Limit:** `5,000` transactions per 10-minute window per `MAP_KEY`.
- **NRT vs. Archive Separation:**
  - NRT endpoints hold only recent rolling windows (~2-3 months) and cannot serve historical years such as 2013.
  - Historical years (`2010-2025`) come from NASA FIRMS Standard Processing (`SP`) Country Yearly Summary archives (`https://firms.modaps.eosdis.nasa.gov/data/country/zips/`), extracted via `scripts/fetch_firms_archive.py` into `data/archive/` and seeded directly into PostgreSQL.

## 2. Empirical Dataset Inventory (Verified Locally)
- **48-Hour Global NRT (`data/input/`):**
  - `MODIS_C6_1_Global_48h.csv`: `19,145` rows (`7,027` Terra `T`, `12,118` Aqua `A`; `839` low `<30`, `13,053` nominal `30-79`, `5,253` high `>=80`; `2026-11-12` to `2026-11-14 05:18 UTC`).
  - `J2_VIIRS_C2_Global_48h.csv`: `99,736` rows (all `N21`; `12,668` low, `81,150` nominal, `5,918` high; `2026-11-12` to `2026-11-14 04:14 UTC`).
  - Note: `2026-11-14` is a partial UTC day (`804` MODIS rows, `4,331` VIIRS rows).
- **Historical Pakistan SP Archive (`data/archive/`):**
  - `22` official yearly country CSVs (`853,015` rows total):
    - MODIS (`Terra`/`Aqua`, `2010, 2013, 2015, 2018, 2020, 2022, 2023, 2024, 2025`)
    - VIIRS Suomi-NPP (`N`, `2013, 2015, 2018, 2020, 2022, 2023, 2024`)
    - VIIRS NOAA-20 (`N20`, `2018, 2020, 2022, 2023, 2024, 2025`)
  - Archive CSVs include `instrument` and `type` columns and full names (`Terra`, `Aqua`) for MODIS satellites; the Go parser resolves columns by header name.

## 3. Basemap Provider
- **Provider:** CARTO Dark Matter Raster Tiles (`@2x` retina PNG)
- **Tile URLs:** `https://{a,b,c,d}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}@2x.png`
- **Attribution:** `&copy; OpenStreetMap contributors &copy; CARTO`

## 4. Citation Wording
> Active fire data provided by NASA's Land, Atmosphere Near real-time Capability for EOS (LANCE) Fire Information for Resource Management System (FIRMS), MODIS Collection 6.1 and VIIRS 375 m Collection 2 products.
