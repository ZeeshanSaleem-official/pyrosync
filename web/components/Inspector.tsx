"use client";

import { useState } from "react";
import { detectionsToCSV } from "@/lib/api";
import { formatMW, formatNumber, formatRatio, robustZColor, SENSOR_COLORS } from "@/lib/colors";
import type {
  CellResponse,
  HourlyResponse,
  LadderStep,
  SummaryResponse,
} from "@/lib/types";

interface InspectorProps {
  summary?: SummaryResponse;
  summaryLoading: boolean;
  summaryError: string | null;
  hourly?: HourlyResponse;
  hourlyLoading: boolean;
  cellId: number | null;
  cellData?: CellResponse;
  cellLoading: boolean;
  cellError: string | null;
  onClearCell: () => void;
}

export function ProofLadder({ steps }: { steps: LadderStep[] }) {
  if (!steps.length) {
    return (
      <div style={{ fontSize: "11px", color: "var(--text-3)" }}>
        No ladder metrics available for this filter.
      </div>
    );
  }

  return (
    <div className="ps-ladder-list" aria-label="4-step MODIS to VIIRS convergence ladder">
      {steps.map((st) => {
        const peak = Math.max(st.modis_value, st.viirs_value, 1);
        const modisPct = Math.max(2, (st.modis_value / peak) * 100);
        const viirsPct = Math.max(2, (st.viirs_value / peak) * 100);
        return (
          <div key={st.key} className="ps-ladder-row">
            <div className="ps-ladder-meta">
              <span style={{ color: "var(--text-1)", fontWeight: 500 }}>
                {st.step}. {st.label}
              </span>
              <span className="ps-ladder-ratio mono">{formatRatio(st.ratio)}</span>
            </div>
            <div className="ps-ladder-bars">
              <div className="ps-bar-track" title={`MODIS: ${formatNumber(st.modis_value)} ${st.unit}`}>
                <div
                  className="ps-bar-fill"
                  style={{ width: `${modisPct.toFixed(1)}%`, backgroundColor: SENSOR_COLORS.modis }}
                />
              </div>
              <div className="ps-bar-track" title={`VIIRS: ${formatNumber(st.viirs_value)} ${st.unit}`}>
                <div
                  className="ps-bar-fill"
                  style={{ width: `${viirsPct.toFixed(1)}%`, backgroundColor: SENSOR_COLORS.viirs }}
                />
              </div>
            </div>
            <div
              className="mono"
              style={{ display: "flex", justifyContent: "space-between", fontSize: "10px", color: "var(--text-3)" }}
            >
              <span>
                M: {formatNumber(st.modis_value)} {st.unit}
              </span>
              <span>
                V: {formatNumber(st.viirs_value)} {st.unit}
              </span>
            </div>
          </div>
        );
      })}
    </div>
  );
}

export function HourlyCycleChart({
  hourly,
}: {
  hourly: HourlyResponse;
}) {
  const [metric, setMetric] = useState<"px" | "frp">("px");
  const [hoverHour, setHoverHour] = useState<number | null>(null);

  const buckets = hourly.hours || [];
  const maxVal = Math.max(
    1,
    ...buckets.map((b) =>
      metric === "px" ? Math.max(b.modis_px, b.viirs_px) : Math.max(b.modis_frp, b.viirs_frp),
    ),
  );

  const chartW = 340;
  const chartH = 74;
  const colW = chartW / 24;
  const inspected = hoverHour !== null ? buckets[hoverHour] : null;

  return (
    <div>
      <div className="ps-sec-head" style={{ marginBottom: "6px" }}>
        <h2 className="ps-sec-title">24h Diurnal Cycle (UTC)</h2>
        <div className="ps-segmented" role="group" aria-label="Hourly chart metric">
          <button
            type="button"
            className="ps-seg-btn"
            style={{ height: "22px", fontSize: "10px", padding: "0 6px" }}
            aria-pressed={metric === "px"}
            onClick={() => setMetric("px")}
          >
            Pixels
          </button>
          <button
            type="button"
            className="ps-seg-btn"
            style={{ height: "22px", fontSize: "10px", padding: "0 6px" }}
            aria-pressed={metric === "frp"}
            onClick={() => setMetric("frp")}
          >
            FRP MW
          </button>
        </div>
      </div>

      <svg
        viewBox={`0 0 ${chartW} ${chartH + 16}`}
        width="100%"
        height={94}
        role="img"
        aria-label="Hourly UTC MODIS and VIIRS detection bar chart"
        onMouseLeave={() => setHoverHour(null)}
        style={{ display: "block", backgroundColor: "var(--bg-canvas)", border: "1px solid var(--line)" }}
      >
        {/* Grid baselines */}
        <line x1={0} y1={chartH} x2={chartW} y2={chartH} stroke="#2E3338" strokeWidth={1} />
        {buckets.map((b) => {
          const x0 = b.hour * colW;
          const mVal = metric === "px" ? b.modis_px : b.modis_frp;
          const vVal = metric === "px" ? b.viirs_px : b.viirs_frp;
          const mH = mVal > 0 ? Math.max(2, (mVal / maxVal) * (chartH - 6)) : 0;
          const vH = vVal > 0 ? Math.max(2, (vVal / maxVal) * (chartH - 6)) : 0;
          const isHover = hoverHour === b.hour;
          return (
            <g
              key={b.hour}
              onMouseEnter={() => setHoverHour(b.hour)}
              style={{ cursor: "crosshair" }}
            >
              <rect
                x={x0}
                y={0}
                width={colW}
                height={chartH}
                fill={isHover ? "#1F2327" : "transparent"}
              />
              <rect
                x={x0 + 1.5}
                y={chartH - mH}
                width={Math.max(1, colW * 0.42)}
                height={mH}
                fill={SENSOR_COLORS.modis}
              />
              <rect
                x={x0 + 1.5 + colW * 0.44}
                y={chartH - vH}
                width={Math.max(1, colW * 0.42)}
                height={vH}
                fill={SENSOR_COLORS.viirs}
              />
              {b.hour % 6 === 0 && (
                <text
                  x={x0 + 2}
                  y={chartH + 11}
                  fill="#8A9299"
                  fontSize="8.5"
                  fontFamily="IBM Plex Mono, monospace"
                >
                  {String(b.hour).padStart(2, "0")}Z
                </text>
              )}
            </g>
          );
        })}
      </svg>

      <div
        className="mono"
        style={{
          marginTop: "4px",
          fontSize: "10px",
          color: "var(--text-2)",
          minHeight: "15px",
        }}
      >
        {inspected
          ? `${String(inspected.hour).padStart(2, "0")}:00Z | M: ${
              metric === "px"
                ? `${formatNumber(inspected.modis_px)} px (${inspected.modis_day_px}D/${inspected.modis_night_px}N)`
                : formatMW(inspected.modis_frp)
            } | V: ${
              metric === "px"
                ? `${formatNumber(inspected.viirs_px)} px (${inspected.viirs_day_px}D/${inspected.viirs_night_px}N)`
                : formatMW(inspected.viirs_frp)
            }`
          : "Hover bar for UTC hour Day/Night breakdown"}
      </div>
    </div>
  );
}

export function Inspector({
  summary,
  summaryLoading,
  summaryError,
  hourly,
  hourlyLoading,
  cellId,
  cellData,
  cellLoading,
  cellError,
  onClearCell,
}: InspectorProps) {
  const [copied, setCopied] = useState(false);

  const zValues = (summary?.daily || [])
    .map((d) => d.robust_z)
    .filter((z): z is number => z !== null && z !== undefined && Number.isFinite(z));
  const peakZ = zValues.length
    ? zValues.reduce((acc, z) => (Math.abs(z) > Math.abs(acc) ? z : acc), zValues[0])
    : null;

  const avgHarmPerDay = summary
    ? summary.kpis.harmonized_frp_mw / Math.max(1, summary.kpis.days)
    : 0;

  async function handleCopyCSV() {
    if (!cellData) return;
    try {
      await navigator.clipboard.writeText(detectionsToCSV(cellData));
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  }

  return (
    <aside className="ps-inspector" aria-label="Analytical summary and pixel inspector">
      {summaryError && (
        <div className="ps-sec">
          <div className="ps-error-banner" role="alert">
            {summaryError}
          </div>
        </div>
      )}

      {/* 1. 4 KPI Cards */}
      <section className="ps-sec">
        <div className="ps-sec-head">
          <h2 className="ps-sec-title">
            {summary ? summary.region_name : "Harmonization Telemetry"}
          </h2>
          {summary && (
            <span className="mono" style={{ fontSize: "10px", color: "var(--text-3)" }}>
              {summary.from} to {summary.to}
            </span>
          )}
        </div>

        <div className="ps-kpi-grid">
          <div className="ps-kpi-card">
            <div className="ps-kpi-label">Raw -&gt; A1 Cells</div>
            <div className="ps-kpi-val mono">
              {summaryLoading && !summary
                ? "Loading..."
                : `${formatNumber(summary?.kpis.raw_pixels_total)} -> ${formatNumber(summary?.kpis.harmonized_cells_union)}`}
            </div>
            <div className="ps-kpi-sub mono">
              {summary
                ? `M ${formatNumber(summary.kpis.raw_pixels_modis)} | V ${formatNumber(summary.kpis.raw_pixels_viirs)}`
                : "Awaiting summary"}
            </div>
          </div>

          <div className="ps-kpi-card">
            <div className="ps-kpi-label">Harmonized FRP</div>
            <div className="ps-kpi-val mono">
              {summaryLoading && !summary ? "--" : formatMW(avgHarmPerDay)}
              <span style={{ fontSize: "11px", color: "var(--text-3)" }}>/d</span>
            </div>
            <div className="ps-kpi-sub mono" title={summary?.calibration.label || ""}>
              {summary ? summary.calibration.label : "--"}
            </div>
          </div>

          <div className="ps-kpi-card">
            <div className="ps-kpi-label">A10 Agreement</div>
            <div className="ps-kpi-val mono">
              {summary ? `${summary.a10_agreement.overall_pct.toFixed(1)}%` : "--"}
            </div>
            <div className="ps-kpi-sub mono">
              {summary
                ? `${formatNumber(summary.a10_agreement.both_cell_days)}/${formatNumber(summary.a10_agreement.modis_cell_days)} | Full ${summary.a10_agreement.full_day_pct.toFixed(1)}%`
                : "--"}
            </div>
          </div>

          <div className="ps-kpi-card">
            <div className="ps-kpi-label">Anomaly / Window</div>
            <div
              className="ps-kpi-val mono"
              style={{
                color: peakZ !== null && Math.abs(peakZ) >= 1 ? robustZColor(peakZ) : "var(--text-1)",
              }}
            >
              {peakZ !== null
                ? `${peakZ >= 0 ? "+" : ""}${peakZ.toFixed(2)} z`
                : summary
                  ? `${summary.kpis.days} UTC d`
                  : "--"}
            </div>
            <div className="ps-kpi-sub">
              {summary?.kpis.any_partial
                ? "Includes partial UTC boundary day"
                : peakZ !== null
                  ? "Peak seasonal DOY anomaly"
                  : "Complete UTC coverage"}
            </div>
          </div>
        </div>
      </section>

      {/* 2. 4-Step Proof Ladder */}
      <section className="ps-sec">
        <div className="ps-sec-head">
          <h2 className="ps-sec-title">4-Step Proof Ladder (VIIRS / MODIS)</h2>
          <span className="mono" style={{ fontSize: "10px", color: "var(--text-3)" }}>
            <span style={{ color: SENSOR_COLORS.modis }}>MODIS</span> vs{" "}
            <span style={{ color: SENSOR_COLORS.viirs }}>VIIRS</span>
          </span>
        </div>
        <ProofLadder steps={summary?.ladder || []} />
      </section>

      {/* 3. 24h Diurnal Cycle */}
      <section className="ps-sec">
        {hourlyLoading && !hourly ? (
          <div style={{ fontSize: "11px", color: "var(--text-3)" }}>Loading hourly cycle...</div>
        ) : hourly ? (
          <HourlyCycleChart hourly={hourly} />
        ) : (
          <div style={{ fontSize: "11px", color: "var(--text-3)" }}>No hourly records.</div>
        )}
      </section>

      {/* 4. Selected Cell Pixel Inspector */}
      <section className="ps-sec" style={{ borderBottom: 0 }}>
        <div className="ps-sec-head">
          <h2 className="ps-sec-title">
            {cellId !== null ? `Cell A1 #${cellId}` : "Pixel Overpass Inspector"}
          </h2>
          {cellId !== null && (
            <div style={{ display: "flex", gap: "4px" }}>
              <button
                type="button"
                className="ps-btn ps-btn-sm"
                onClick={handleCopyCSV}
                disabled={!cellData || cellData.detections.length === 0}
              >
                {copied ? "Copied" : "Copy CSV"}
              </button>
              <button type="button" className="ps-btn ps-btn-sm" onClick={onClearCell}>
                Clear
              </button>
            </div>
          )}
        </div>

        {cellId === null ? (
          <div style={{ fontSize: "11px", color: "var(--text-3)", lineHeight: 1.45 }}>
            Zoom to Z6+ on the map and click any A1 grid cell (~1.2 km2) or Z11+ detection
            footprint to inspect raw MODIS and VIIRS passes against the 6.2 MW floor.
          </div>
        ) : cellLoading ? (
          <div style={{ fontSize: "11px", color: "var(--text-3)" }}>Loading cell detections...</div>
        ) : cellError ? (
          <div className="ps-error-banner">{cellError}</div>
        ) : cellData ? (
          <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
            <div
              className="mono"
              style={{ fontSize: "11px", color: "var(--text-2)", display: "grid", gridTemplateColumns: "1fr 1fr", gap: "4px" }}
            >
              <span>
                Center: {cellData.center[0].toFixed(3)}, {cellData.center[1].toFixed(3)}
              </span>
              <span>Area: {cellData.area_km2.toFixed(2)} km2</span>
              <span>
                Px: {cellData.summary.modis_px}M / {cellData.summary.viirs_px}V
              </span>
              <span>Harm: {formatMW(cellData.summary.harmonized_frp)}</span>
            </div>

            {cellData.detections.length === 0 ? (
              <div style={{ fontSize: "11px", color: "var(--text-3)" }}>
                No detections at conf &gt;= {cellData.conf_level} in {cellData.from}..{cellData.to}.
              </div>
            ) : (
              <div className="ps-table-wrap">
                <table className="ps-table mono">
                  <thead>
                    <tr>
                      <th>UTC</th>
                      <th>Sat</th>
                      <th>FRP</th>
                      <th>OvSum</th>
                      <th>Floor</th>
                    </tr>
                  </thead>
                  <tbody>
                    {cellData.detections.map((d) => (
                      <tr key={d.id}>
                        <td>{d.acq_ts.slice(5, 16).replace("T", " ")}</td>
                        <td
                          style={{
                            color:
                              d.sensor === "MODIS" ? SENSOR_COLORS.modis : SENSOR_COLORS.viirs,
                          }}
                        >
                          {d.satellite}
                        </td>
                        <td>{d.frp_mw.toFixed(1)}</td>
                        <td>{d.overpass_frp_mw.toFixed(1)}</td>
                        <td>
                          <span
                            className={`ps-tag ${d.kept ? "" : "ps-tag-warn"}`}
                            style={{ padding: "1px 4px", fontSize: "9px" }}
                          >
                            {d.kept ? "KEPT" : "DROP"}
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        ) : null}
      </section>
    </aside>
  );
}
