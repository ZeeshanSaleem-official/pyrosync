"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import {
  formatMW,
  formatNumber,
  infernoColor,
  INFERNO_STOPS,
  robustZColor,
  SENSOR_COLORS,
  Z_SCORE_BINS,
} from "@/lib/colors";
import type {
  CalendarResponse,
  CalendarTab,
  DailyPoint,
  DashboardParams,
  HistoryMetric,
  RegionInfo,
  Timeline6hResponse,
  TimelineDayResponse,
} from "@/lib/types";

interface CalendarDockProps {
  params: DashboardParams;
  regions: RegionInfo[];
  timelineDay?: TimelineDayResponse;
  timeline6h?: Timeline6hResponse;
  liveLoading: boolean;
  liveError: string | null;
  calendar?: CalendarResponse;
  calendarLoading: boolean;
  calendarError: string | null;
  onChangeTab: (tab: CalendarTab) => void;
  onChangeMetric: (metric: HistoryMetric) => void;
  onSelectDateRange: (from: string, to: string) => void;
  onResetLiveDates: () => void;
  onSelectRegion: (regionId: string) => void;
}

const MONTH_DOY = [
  [1, "Jan"],
  [32, "Feb"],
  [60, "Mar"],
  [91, "Apr"],
  [121, "May"],
  [152, "Jun"],
  [182, "Jul"],
  [213, "Aug"],
  [244, "Sep"],
  [274, "Oct"],
  [305, "Nov"],
  [335, "Dec"],
] as const;

function dayOfYearUTC(dateStr: string): { year: number; doy: number } | null {
  const parts = dateStr.split("-").map(Number);
  if (parts.length !== 3 || parts.some((n) => !Number.isFinite(n))) return null;
  const [y, m, d] = parts;
  const start = Date.UTC(y, 0, 1);
  const cur = Date.UTC(y, m - 1, d);
  const doy = Math.floor((cur - start) / 86_400_000) + 1;
  return { year: y, doy };
}

function dateFromYearDOY(year: number, doy: number): string {
  const dt = new Date(Date.UTC(year, 0, doy));
  return dt.toISOString().slice(0, 10);
}

function isLeapYear(year: number): boolean {
  return (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0;
}

function eraColor(era: string): string {
  if (era === "MODIS Only") return SENSOR_COLORS.modis;
  if (era === "MODIS + S-NPP") return SENSOR_COLORS.both;
  return SENSOR_COLORS.viirs;
}

export function CalendarDock({
  params,
  regions,
  timelineDay,
  timeline6h,
  liveLoading,
  liveError,
  calendar,
  calendarLoading,
  calendarError,
  onChangeTab,
  onChangeMetric,
  onSelectDateRange,
  onResetLiveDates,
  onSelectRegion,
}: CalendarDockProps) {
  const [liveBucket, setLiveBucket] = useState<"day" | "6h">("day");
  const [hoverDay, setHoverDay] = useState<{
    date: string;
    era: string;
    point: DailyPoint | null;
  } | null>(null);

  const canvasBoxRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);

  const historyIndex = useMemo(() => {
    const byKey = new Map<string, DailyPoint>();
    const byDate = new Map<string, DailyPoint>();
    const eraByYear = new Map<number, string>();
    let maxFRP = 1;

    if (calendar) {
      for (const y of calendar.years || []) {
        eraByYear.set(y.year, y.era);
      }
      for (const d of calendar.days || []) {
        byDate.set(d.date, d);
        if (d.harmonized_frp > maxFRP) {
          maxFRP = d.harmonized_frp;
        }
        const yd = dayOfYearUTC(d.date);
        if (yd) {
          byKey.set(`${yd.year}-${yd.doy}`, d);
        }
      }
    }
    return { byKey, byDate, eraByYear, maxFRP };
  }, [calendar]);

  useEffect(() => {
    if (params.tab !== "history" || !calendar?.has_history) return;
    const box = canvasBoxRef.current;
    const canvas = canvasRef.current;
    if (!box || !canvas) return;

    const years = (calendar.years || []).map((y) => y.year).sort((a, b) => a - b);
    if (!years.length) return;

    const draw = () => {
      const rect = box.getBoundingClientRect();
      const width = Math.max(320, Math.floor(rect.width));
      const height = Math.max(120, Math.floor(rect.height));
      const dpr = Math.min(2, window.devicePixelRatio || 1);

      canvas.width = Math.floor(width * dpr);
      canvas.height = Math.floor(height * dpr);
      canvas.style.width = `${width}px`;
      canvas.style.height = `${height}px`;

      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

      ctx.fillStyle = "#101214";
      ctx.fillRect(0, 0, width, height);

      const leftPad = 50;
      const topPad = 18;
      const rightPad = 8;
      const bottomPad = 6;
      const gridW = Math.max(100, width - leftPad - rightPad);
      const gridH = Math.max(40, height - topPad - bottomPad);
      const cellW = gridW / 366;
      const rowH = gridH / years.length;

      // Month headers across DOY axis.
      ctx.font = "10px IBM Plex Mono, monospace";
      ctx.fillStyle = "#8A9299";
      ctx.textBaseline = "top";
      for (const [doy, label] of MONTH_DOY) {
        const x = leftPad + (doy - 1) * cellW;
        ctx.fillText(label, x + 1, 3);
        ctx.fillStyle = "#2E3338";
        ctx.fillRect(Math.floor(x), topPad, 1, gridH);
        ctx.fillStyle = "#8A9299";
      }

      for (let r = 0; r < years.length; r++) {
        const y = years[r];
        const yTop = topPad + r * rowH;
        const yCellH = Math.max(2, rowH - 1.5);
        const era = historyIndex.eraByYear.get(y) || "";

        // Era marker stripe + year label.
        ctx.fillStyle = eraColor(era);
        ctx.fillRect(4, yTop + 1, 4, yCellH - 1);

        ctx.fillStyle = "#A3AAB0";
        ctx.font = rowH >= 11 ? "10px IBM Plex Mono, monospace" : "9px IBM Plex Mono, monospace";
        ctx.textBaseline = "middle";
        ctx.fillText(String(y), 12, yTop + rowH / 2);

        const maxDOY = isLeapYear(y) ? 366 : 365;
        for (let doy = 1; doy <= maxDOY; doy++) {
          const x = leftPad + (doy - 1) * cellW;
          const pt = historyIndex.byKey.get(`${y}-${doy}`);
          if (!pt) {
            // Unseeded date gap (distinct from zero-fire seeded day).
            ctx.fillStyle = "#16191D";
            ctx.fillRect(x, yTop, Math.max(1, cellW - 0.3), yCellH);
            continue;
          }

          if (params.metric === "zscore") {
            ctx.fillStyle =
              pt.robust_z !== null && pt.robust_z !== undefined
                ? robustZColor(pt.robust_z)
                : "#1F2327";
          } else {
            ctx.fillStyle = infernoColor(pt.harmonized_frp, historyIndex.maxFRP);
          }
          ctx.fillRect(x, yTop, Math.max(1, cellW - 0.3), yCellH);

          if (pt.is_partial) {
            ctx.fillStyle = "#E0B341";
            ctx.fillRect(x, yTop, Math.max(1, cellW - 0.3), 1.5);
          }

          if (params.from && params.to && pt.date >= params.from && pt.date <= params.to) {
            ctx.strokeStyle = "#E8EAEB";
            ctx.lineWidth = 1;
            ctx.strokeRect(x + 0.5, yTop + 0.5, Math.max(1, cellW - 1), Math.max(1, yCellH - 1));
          }
        }
      }
    };

    draw();
    const ro = new ResizeObserver(() => draw());
    ro.observe(box);
    return () => ro.disconnect();
  }, [params.tab, params.metric, params.from, params.to, calendar, historyIndex]);

  function resolveCanvasPoint(e: React.MouseEvent<HTMLCanvasElement>): {
    date: string;
    era: string;
    point: DailyPoint | null;
  } | null {
    if (!calendar?.years?.length || !canvasRef.current) return null;
    const years = calendar.years.map((y) => y.year).sort((a, b) => a - b);
    const rect = canvasRef.current.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;

    const leftPad = 50;
    const topPad = 18;
    const rightPad = 8;
    const bottomPad = 6;
    const gridW = Math.max(100, rect.width - leftPad - rightPad);
    const gridH = Math.max(40, rect.height - topPad - bottomPad);

    if (x < leftPad || x > leftPad + gridW || y < topPad || y > topPad + gridH) {
      return null;
    }

    const rowIdx = Math.min(years.length - 1, Math.max(0, Math.floor(((y - topPad) / gridH) * years.length)));
    const doy = Math.min(366, Math.max(1, Math.floor(((x - leftPad) / gridW) * 366) + 1));
    const year = years[rowIdx];
    const maxDOY = isLeapYear(year) ? 366 : 365;
    if (doy > maxDOY) return null;

    const date = dateFromYearDOY(year, doy);
    const pt = historyIndex.byDate.get(date) || null;
    const era = historyIndex.eraByYear.get(year) || "";
    return { date, era, point: pt };
  }

  const archiveRegions = regions.filter((r) => r.has_history);

  return (
    <section className="ps-calendar" aria-label="Burning activity calendar dock">
      {/* Header strip */}
      <div className="ps-cal-head">
        <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
          <div className="ps-segmented" role="tablist" aria-label="Timeline mode">
            <button
              type="button"
              role="tab"
              className="ps-seg-btn"
              aria-selected={params.tab === "live"}
              onClick={() => onChangeTab("live")}
            >
              Live 48h / 30d Strip
            </button>
            <button
              type="button"
              role="tab"
              className="ps-seg-btn"
              aria-selected={params.tab === "history"}
              onClick={() => onChangeTab("history")}
            >
              Multi-Year History Calendar
            </button>
          </div>

          {params.tab === "live" ? (
            <div className="ps-segmented" role="group" aria-label="Live strip bucket">
              <button
                type="button"
                className="ps-seg-btn"
                aria-pressed={liveBucket === "day"}
                onClick={() => setLiveBucket("day")}
              >
                Daily
              </button>
              <button
                type="button"
                className="ps-seg-btn"
                aria-pressed={liveBucket === "6h"}
                onClick={() => setLiveBucket("6h")}
              >
                6h Synoptic
              </button>
            </div>
          ) : (
            calendar?.has_history && (
              <div className="ps-segmented" role="group" aria-label="History calendar metric">
                <button
                  type="button"
                  className="ps-seg-btn"
                  aria-pressed={params.metric === "frp"}
                  onClick={() => onChangeMetric("frp")}
                >
                  Harmonized FRP
                </button>
                <button
                  type="button"
                  className="ps-seg-btn"
                  aria-pressed={params.metric === "zscore"}
                  onClick={() => onChangeMetric("zscore")}
                >
                  Anomaly Z-Score
                </button>
              </div>
            )
          )}
        </div>

        {/* Right legend strip */}
        {params.tab === "live" ? (
          <div className="mono" style={{ display: "flex", alignItems: "center", gap: "10px", fontSize: "10px", color: "var(--text-3)" }}>
            <span>
              <span style={{ color: SENSOR_COLORS.modis }}>--</span> MODIS FRP
            </span>
            <span>
              <span style={{ color: SENSOR_COLORS.viirs }}>--</span> VIIRS &gt;= 6.2 MW
            </span>
            <span style={{ borderBottom: "1px dashed var(--warn)", color: "var(--warn)" }}>
              Dashed = Partial UTC Day
            </span>
          </div>
        ) : (
          calendar?.has_history && (
            <div style={{ display: "flex", alignItems: "center", gap: "12px", fontSize: "10px", color: "var(--text-2)" }}>
              <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
                {(calendar.era_brackets || []).map((b) => (
                  <span key={`${b.era}-${b.from_year}`} className="mono" style={{ display: "inline-flex", alignItems: "center", gap: "4px" }}>
                    <span style={{ width: "6px", height: "8px", backgroundColor: eraColor(b.era), display: "inline-block" }} />
                    {b.era} ({b.from_year}-{b.to_year})
                  </span>
                ))}
              </div>
              {params.metric === "zscore" ? (
                <div style={{ display: "flex", alignItems: "center", gap: "3px" }}>
                  {Z_SCORE_BINS.map((z) => (
                    <span
                      key={z.label}
                      title={`Z-score ${z.label}`}
                      className="mono"
                      style={{
                        backgroundColor: z.color,
                        color: z.label === "-2..-1" || z.label === "+1..+2" ? "#101214" : "#E8EAEB",
                        padding: "1px 4px",
                        fontSize: "9px",
                      }}
                    >
                      {z.label}
                    </span>
                  ))}
                </div>
              ) : (
                <div style={{ display: "flex", alignItems: "center", gap: "5px" }}>
                  <span style={{ width: "54px", height: "8px", background: `linear-gradient(to right, ${INFERNO_STOPS.join(", ")})`, border: "1px solid var(--line)", display: "inline-block" }} />
                  <span className="mono">0..{formatNumber(historyIndex.maxFRP)} MW</span>
                </div>
              )}
            </div>
          )
        )}
      </div>

      {/* Body */}
      <div className="ps-cal-body">
        {params.tab === "live" ? (
          liveError ? (
            <div className="ps-error-banner">{liveError}</div>
          ) : liveBucket === "day" ? (
            liveLoading && !timelineDay ? (
              <div style={{ fontSize: "11px", color: "var(--text-3)" }}>Loading live synoptic days...</div>
            ) : !timelineDay?.series?.length ? (
              <div style={{ fontSize: "11px", color: "var(--text-3)" }}>No daily series in selected filter.</div>
            ) : (
              <div className="ps-strip-grid">
                {(() => {
                  const series = timelineDay.series;
                  const peakHarm = Math.max(1, ...series.map((d) => d.harmonized_frp));
                  return series.map((d) => {
                    const isFocused = params.from === d.date && params.to === d.date;
                    const mVal = d.modis_frp ?? 0;
                    const vVal = d.viirs_meq_frp ?? 0;
                    const barPeak = Math.max(1, mVal, vVal);
                    const totalFrac = Math.min(100, (d.harmonized_frp / peakHarm) * 100);
                    return (
                      <button
                        key={d.date}
                        type="button"
                        className="ps-day-tile"
                        data-partial={d.is_partial ? "true" : "false"}
                        data-active={isFocused ? "true" : "false"}
                        onClick={() =>
                          isFocused ? onResetLiveDates() : onSelectDateRange(d.date, d.date)
                        }
                        title={
                          isFocused
                            ? "Click again to restore full window"
                            : `Click to filter map and inspector to ${d.date}`
                        }
                      >
                        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "4px" }}>
                          <span className="mono" style={{ fontSize: "11px", fontWeight: 600 }}>
                            {d.date}
                          </span>
                          {d.is_partial && (
                            <span className="ps-tag ps-tag-warn" style={{ padding: "1px 4px", fontSize: "9px" }}>
                              Partial
                            </span>
                          )}
                        </div>

                        <div style={{ margin: "6px 0" }}>
                          <div className="mono" style={{ fontSize: "14px", fontWeight: 600 }}>
                            {formatMW(d.harmonized_frp)}
                          </div>
                          <div className="mono" style={{ fontSize: "10px", color: "var(--text-3)" }}>
                            {formatNumber(d.cells_union)} A1 cells
                            {d.robust_z !== null && d.robust_z !== undefined
                              ? ` | ${d.robust_z >= 0 ? "+" : ""}${d.robust_z.toFixed(2)}z`
                              : ""}
                          </div>
                        </div>

                        <div style={{ display: "flex", flexDirection: "column", gap: "3px" }}>
                          <div className="ps-bar-track" style={{ height: "4px" }}>
                            <div
                              className="ps-bar-fill"
                              style={{
                                width: `${totalFrac.toFixed(1)}%`,
                                backgroundColor: infernoColor(d.harmonized_frp, peakHarm),
                              }}
                            />
                          </div>
                          <div style={{ display: "flex", gap: "3px", alignItems: "center" }}>
                            <div className="ps-bar-track" style={{ height: "4px", flex: 1 }}>
                              <div
                                className="ps-bar-fill"
                                style={{
                                  width: `${Math.min(100, (mVal / barPeak) * 100).toFixed(1)}%`,
                                  backgroundColor: SENSOR_COLORS.modis,
                                }}
                              />
                            </div>
                            <div className="ps-bar-track" style={{ height: "4px", flex: 1 }}>
                              <div
                                className="ps-bar-fill"
                                style={{
                                  width: `${Math.min(100, (vVal / barPeak) * 100).toFixed(1)}%`,
                                  backgroundColor: SENSOR_COLORS.viirs,
                                }}
                              />
                            </div>
                          </div>
                          <div
                            className="mono"
                            style={{ display: "flex", justifyContent: "space-between", fontSize: "9px", color: "var(--text-3)" }}
                          >
                            <span>M {formatNumber(d.modis_px)}px</span>
                            <span>V {formatNumber(d.viirs_px)}px</span>
                          </div>
                        </div>
                      </button>
                    );
                  });
                })()}
              </div>
            )
          ) : liveLoading && !timeline6h ? (
            <div style={{ fontSize: "11px", color: "var(--text-3)" }}>Loading 6h synoptic buckets...</div>
          ) : !timeline6h?.series?.length ? (
            <div style={{ fontSize: "11px", color: "var(--text-3)" }}>
              No 6h synoptic buckets (select a window &lt;= 31 days).
            </div>
          ) : (
            <div className="ps-strip-grid">
              {(() => {
                const buckets = timeline6h.series;
                const peakH = Math.max(1, ...buckets.map((b) => b.harmonized_frp));
                return buckets.map((b) => {
                  const datePart = b.bucket_ts.slice(0, 10);
                  const hourPart = b.bucket_ts.slice(11, 13);
                  return (
                    <button
                      key={b.bucket_ts}
                      type="button"
                      className="ps-day-tile"
                      onClick={() => onSelectDateRange(datePart, datePart)}
                    >
                      <div className="mono" style={{ fontSize: "11px", fontWeight: 600 }}>
                        {datePart} {hourPart}Z
                      </div>
                      <div className="mono" style={{ fontSize: "13px", fontWeight: 600 }}>
                        {formatMW(b.harmonized_frp)}
                      </div>
                      <div>
                        <div className="ps-bar-track" style={{ height: "5px", marginBottom: "4px" }}>
                          <div
                            className="ps-bar-fill"
                            style={{
                              width: `${Math.min(100, (b.harmonized_frp / peakH) * 100).toFixed(1)}%`,
                              backgroundColor: infernoColor(b.harmonized_frp, peakH),
                            }}
                          />
                        </div>
                        <div className="mono" style={{ fontSize: "9px", color: "var(--text-3)" }}>
                          M {b.modis_px}px | V {b.viirs_px}px | {b.cells_union} cells
                        </div>
                      </div>
                    </button>
                  );
                });
              })()}
            </div>
          )
        ) : calendarError ? (
          <div className="ps-error-banner">{calendarError}</div>
        ) : calendarLoading && !calendar ? (
          <div style={{ fontSize: "11px", color: "var(--text-3)" }}>
            Loading multi-year archive calendar...
          </div>
        ) : !calendar?.has_history ? (
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: "16px", padding: "14px", border: "1px solid var(--line)", backgroundColor: "var(--bg-canvas)" }}>
            <div>
              <div style={{ fontSize: "12px", fontWeight: 600, marginBottom: "4px" }}>
                Multi-Year Archive Unavailable for {calendar?.region_name || params.region}
              </div>
              <div style={{ fontSize: "11px", color: "var(--text-2)" }}>
                Seasonal burning calendars and Z-score anomaly baselines require &gt;= 3 years of NASA FIRMS Standard Processing (SP) records; this region currently holds near real-time (NRT) coverage.
              </div>
            </div>
            {archiveRegions.length > 0 && (
              <div style={{ display: "flex", gap: "6px", flexShrink: 0 }}>
                {archiveRegions.map((rg) => (
                  <button
                    key={rg.id}
                    type="button"
                    className="ps-btn"
                    onClick={() => onSelectRegion(rg.id)}
                  >
                    View {rg.name} ({rg.history_from?.slice(0, 4)}-{rg.history_to?.slice(0, 4)})
                  </button>
                ))}
              </div>
            )}
          </div>
        ) : (
          <div className="ps-history-wrap">
            <div className="ps-history-canvas-box">
              <div ref={canvasBoxRef} style={{ flex: 1, minHeight: 0, position: "relative" }}>
                <canvas
                  ref={canvasRef}
                  aria-label="Multi-year burning activity calendar heatmap"
                  style={{ display: "block", cursor: "pointer" }}
                  onMouseMove={(e) => setHoverDay(resolveCanvasPoint(e))}
                  onMouseLeave={() => setHoverDay(null)}
                  onClick={(e) => {
                    const hit = resolveCanvasPoint(e);
                    if (hit?.point) {
                      onSelectDateRange(hit.point.date, hit.point.date);
                    }
                  }}
                />
              </div>
              <div
                className="mono"
                style={{
                  height: "20px",
                  borderTop: "1px solid var(--line)",
                  padding: "0 8px",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  fontSize: "10px",
                  color: "var(--text-2)",
                  backgroundColor: "var(--bg-raised)",
                }}
              >
                {hoverDay ? (
                  hoverDay.point ? (
                    <span>
                      {hoverDay.point.date} [{hoverDay.era}] | Harm:{" "}
                      {formatMW(hoverDay.point.harmonized_frp)} | M:{" "}
                      {formatNumber(hoverDay.point.modis_px)}px | V:{" "}
                      {formatNumber(hoverDay.point.viirs_px)}px
                      {hoverDay.point.robust_z !== null && hoverDay.point.robust_z !== undefined
                        ? ` | Z: ${hoverDay.point.robust_z >= 0 ? "+" : ""}${hoverDay.point.robust_z.toFixed(2)}`
                        : ""}
                    </span>
                  ) : (
                    <span>{hoverDay.date} [{hoverDay.era}] | No archived pass in dataset</span>
                  )
                ) : (
                  <span>
                    Hover cell for daily telemetry; click any active historical day to inspect on map
                  </span>
                )}
                <span>{calendar.calibration.label}</span>
              </div>
            </div>

            <div className="ps-year-rail" aria-label="Yearly archive summary">
              {(calendar.years || []).map((ys) => (
                <button
                  key={ys.year}
                  type="button"
                  className="ps-year-btn mono"
                  onClick={() =>
                    onSelectDateRange(`${ys.year}-01-01`, `${ys.year}-12-31`)
                  }
                  title={`Filter map and inspector to ${ys.year} (${ys.era})`}
                >
                  <span style={{ display: "inline-flex", alignItems: "center", gap: "5px" }}>
                    <span
                      style={{
                        width: "6px",
                        height: "6px",
                        backgroundColor: eraColor(ys.era),
                        display: "inline-block",
                      }}
                    />
                    {ys.year}
                  </span>
                  <span style={{ color: "var(--text-3)", fontSize: "10px" }}>
                    {formatNumber(ys.total_frp_mw)} MW | +{ys.max_z.toFixed(1)}z
                  </span>
                </button>
              ))}
            </div>
          </div>
        )}
      </div>
    </section>
  );
}
