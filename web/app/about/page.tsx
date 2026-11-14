import Link from "next/link";

export default function AboutPage() {
  return (
    <main className="ps-about-page">
      <div className="ps-about-container">
        <header
          className="ps-about-card"
          style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "12px" }}
        >
          <div>
            <h1 style={{ margin: "0 0 4px", fontSize: "16px", fontWeight: 600 }}>
              PyroSync — Scientific Harmonization Methodology
            </h1>
            <p style={{ margin: 0, color: "var(--text-2)", fontSize: "12px" }}>
              2026 NASA Space Apps Challenge — Harmonization of MODIS and VIIRS Active-Fire Observations
            </p>
          </div>
          <Link href="/" className="ps-btn" style={{ display: "inline-flex", alignItems: "center", textDecoration: "none" }}>
            Back to Explorer
          </Link>
        </header>

        <section className="ps-about-card">
          <h2 style={{ margin: "0 0 8px", fontSize: "13px", fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.05em", color: "var(--text-2)" }}>
            1. Four-Stage Harmonization Pipeline
          </h2>
          <p style={{ margin: "0 0 8px", color: "var(--text-2)" }}>
            VIIRS 375 m instruments (Suomi-NPP, NOAA-20, NOAA-21) detect roughly 4.8x more active-fire pixels than legacy MODIS 1 km (Terra, Aqua) because smaller sub-pixel fires are resolved. Direct raw-count stitching introduces an artificial step jump after 2012. PyroSync reconciles both constellations in four deterministic stages:
          </p>
          <ol style={{ margin: "0", paddingLeft: "18px", color: "var(--text-1)", lineHeight: 1.6 }}>
            <li>
              <strong>Schema &amp; Quality Standardization:</strong> Unifies MODIS (<code className="mono">brightness</code>, <code className="mono">bright_t31</code>, 0-100 confidence) and VIIRS (<code className="mono">bright_ti4</code>, <code className="mono">bright_ti5</code>, nominal/high/low confidence) with integer coordinate deduplication (<code className="mono">1e-5 deg</code>).
            </li>
            <li>
              <strong>Equal-Area Cosine Grid (A1 &amp; A10):</strong> Projects every fire pixel onto latitude-adjusted equal-area cells (~1.23 km2 at A1, ~123 km2 at A10), collapsing multiple 375 m VIIRS pixels inside a single MODIS-scale cell per satellite overpass.
            </li>
            <li>
              <strong>MODIS-Equivalent Sensitivity Floor (6.2 MW):</strong> Evaluates each VIIRS overpass-cell against the empirical MODIS 10th-percentile FRP sensitivity floor (<code className="mono">F = 6.2 MW</code>), filtering isolated smoldering pixels invisible to MODIS while keeping dense sub-pixel clusters whose cell sum reaches 6.2 MW.
            </li>
            <li>
              <strong>Overpass Mean &amp; Empirical Gain Calibration:</strong> Combines Terra, Aqua, SNPP, NOAA-20, and NOAA-21 passes by mean per cell-day (preventing constellation double-counting) and fits median cross-sensor FRP gain <code className="mono">k</code> with 1,000-bootstrap 95% confidence bounds over overlapping non-partial archive days.
            </li>
          </ol>
          <pre className="ps-code-block">{`numCols(row)  = max(1, round(360 * cos(lat_center) / res))
cellID        = row * 40000 + col
kept(VIIRS)   = sum(frp_mw per overpass-cell) >= 6.2 MW
robust_z(d)   = (H_d - median(H_{doy +/- 7})) / max(1.4826 * MAD, 0.05 * median, 1.0)`}</pre>
        </section>

        <section className="ps-about-card">
          <h2 style={{ margin: "0 0 8px", fontSize: "13px", fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.05em", color: "var(--text-2)" }}>
            2. Space Agency Data Declaration
          </h2>
          <p style={{ margin: 0, color: "var(--text-1)", lineHeight: 1.6 }}>
            This project uses active fire data from NASA&apos;s Land, Atmosphere Near real-time Capability for EOS (LANCE) Fire Information for Resource Management System (FIRMS), specifically MODIS Collection 6.1 (Terra/Aqua) and VIIRS 375 m Collection 2 (Suomi-NPP, NOAA-20, NOAA-21) standard processing and near real-time products.
          </p>
          <p style={{ margin: "8px 0 0", color: "var(--text-3)", fontSize: "12px" }}>
            Basemap cartography: OpenStreetMap contributors and CARTO Dark Matter raster tiles.
          </p>
        </section>

        <section className="ps-about-card">
          <h2 style={{ margin: "0 0 8px", fontSize: "13px", fontWeight: 600, textTransform: "uppercase", letterSpacing: "0.05em", color: "var(--text-2)" }}>
            3. AI Tool Declaration
          </h2>
          <p style={{ margin: 0, color: "var(--text-1)", lineHeight: 1.6 }}>
            Architecture planning, code scaffolding, and documentation refinement were assisted by AI coding assistants. All scientific harmonization formulas, equal-area spatial aggregations, bootstrap confidence intervals, and validation metrics are deterministic and computed directly from official NASA FIRMS datasets in Go and PostgreSQL/PostGIS.
          </p>
        </section>
      </div>
    </main>
  );
}
