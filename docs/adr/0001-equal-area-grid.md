# ADR 0001: Pure-Go Equal-Area Cosine Grid Over H3 and Naive Degree Rounding

## Status
Accepted

## Context
Comparing 1 km MODIS and 375 m VIIRS thermal anomalies requires aggregating detections onto a uniform spatial grid before summing or counting.
- Naive coordinate rounding (`round(lat, 2)`, `round(lon, 2)`) shrinks cell area with `cos(lat)` toward high latitudes (a 0.01° cell at 60°N has half the area of an equatorial cell).
- Uber H3 solves area uniformity, but `github.com/uber/h3-go/v4` requires CGo (`gcc`) on Windows and `ST_AsMVT` tile rendering inside the stock `postgis/postgis:16-3.4` container lacks the `h3-pg` extension.

## Decision
Use a deterministic cosine-adjusted latitude-band equal-area grid at two fixed resolutions (`A1 = 0.01°` ~1.23 km² and `A10 = 0.10°` ~123 km²):
- Latitude rows have fixed step `res`.
- Columns per row scale by `max(1, round(360 * cos(lat_center) / res))`.
- Cell IDs encode `row * 40000 + col` in a 64-bit integer.

## Consequences
- Zero CGo dependencies (`CGO_ENABLED=0` builds cleanly on Windows and in distroless containers).
- Exact deterministic parity between Go (`internal/grid`) and PostgreSQL (`grid_bounds`, `grid_center`) using `ST_MakeEnvelope`, enabling native `ST_AsMVT` vector tiles.
- Every cell has nearly identical ground area across latitudes without external database extensions.
