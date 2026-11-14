"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { formatNumber } from "@/lib/colors";
import type { ConfLevel, DashboardParams, MetaResponse, RegionInfo, ViewMode } from "@/lib/types";

interface TopBarProps {
  params: DashboardParams;
  regions: RegionInfo[];
  meta?: MetaResponse;
  anyPartial?: boolean;
  resolvedFrom: string;
  resolvedTo: string;
  onSelectRegion: (regionId: string) => void;
  onApplyBBox: (bbox: [number, number, number, number]) => void;
  onUseViewportBBox: () => void;
  onChangeDates: (from: string, to: string) => void;
  onResetLiveDates: () => void;
  onChangeConf: (conf: ConfLevel) => void;
  onChangeMode: (mode: ViewMode) => void;
}

export function TopBar({
  params,
  regions,
  meta,
  anyPartial,
  resolvedFrom,
  resolvedTo,
  onSelectRegion,
  onApplyBBox,
  onUseViewportBBox,
  onChangeDates,
  onResetLiveDates,
  onChangeConf,
  onChangeMode,
}: TopBarProps) {
  const [bboxOpen, setBboxOpen] = useState(false);
  const activeRegion = regions.find((r) => r.id === params.region);

  const initialBBox = params.bbox ?? activeRegion?.bbox ?? [-180, -90, 180, 90];
  const [wStr, setWStr] = useState(String(initialBBox[0]));
  const [sStr, setSStr] = useState(String(initialBBox[1]));
  const [eStr, setEStr] = useState(String(initialBBox[2]));
  const [nStr, setNStr] = useState(String(initialBBox[3]));
  const [bboxError, setBboxError] = useState("");

  useEffect(() => {
    const src = params.bbox ?? activeRegion?.bbox;
    if (!src) return;
    setWStr(src[0].toFixed(2));
    setSStr(src[1].toFixed(2));
    setEStr(src[2].toFixed(2));
    setNStr(src[3].toFixed(2));
    setBboxError("");
  }, [params.bbox, activeRegion]);

  function handleBBoxSubmit(e: React.FormEvent) {
    e.preventDefault();
    const w = Number(wStr);
    const s = Number(sStr);
    const east = Number(eStr);
    const n = Number(nStr);
    if (
      !Number.isFinite(w) ||
      !Number.isFinite(s) ||
      !Number.isFinite(east) ||
      !Number.isFinite(n) ||
      w < -180 ||
      w > 180 ||
      east < -180 ||
      east > 180 ||
      s < -90 ||
      s > 90 ||
      n < -90 ||
      n > 90 ||
      s >= n
    ) {
      setBboxError("Invalid W,S,E,N bounds");
      return;
    }
    setBboxError("");
    setBboxOpen(false);
    onApplyBBox([w, s, east, n]);
  }

  return (
    <header className="ps-topbar" role="banner">
      <div className="ps-brand">
        <span className="ps-brand-mark" aria-hidden="true" />
        <h1 className="ps-brand-title">PyroSync</h1>
        <Link href="/about" className="ps-nav-link">
          Methodology
        </Link>
      </div>

      {/* Region & Coordinate BBox */}
      <div className="ps-bar-group" style={{ position: "relative" }}>
        <label htmlFor="ps-region-select" className="ps-bar-label">
          Region
        </label>
        <select
          id="ps-region-select"
          className="ps-select"
          value={params.bbox ? "custom" : params.region}
          onChange={(e) => {
            if (e.target.value !== "custom") {
              setBboxOpen(false);
              onSelectRegion(e.target.value);
            }
          }}
        >
          {regions.map((rg) => (
            <option key={rg.id} value={rg.id}>
              {rg.name}
            </option>
          ))}
          {params.bbox && <option value="custom">Custom Area</option>}
        </select>

        <button
          type="button"
          className="ps-btn"
          aria-expanded={bboxOpen}
          onClick={() => setBboxOpen((v) => !v)}
          title="Enter exact WGS84 bounding box"
        >
          BBox
        </button>

        <button
          type="button"
          className="ps-btn"
          onClick={onUseViewportBBox}
          title="Filter analytics to the current map viewport"
        >
          Lock View
        </button>

        {bboxOpen && (
          <form
            onSubmit={handleBBoxSubmit}
            style={{
              position: "fixed",
              top: "52px",
              left: "180px",
              zIndex: 40,
              backgroundColor: "var(--bg-panel)",
              border: "1px solid var(--line-strong)",
              padding: "10px",
              display: "flex",
              flexDirection: "column",
              gap: "8px",
            }}
          >
            <div style={{ fontSize: "11px", fontWeight: 600, color: "var(--text-2)" }}>
              WGS84 BOUNDING BOX (W, S, E, N)
            </div>
            <div style={{ display: "flex", gap: "6px", alignItems: "center" }}>
              <input
                aria-label="West longitude"
                className="ps-input mono ps-input-coord"
                value={wStr}
                onChange={(e) => setWStr(e.target.value)}
              />
              <input
                aria-label="South latitude"
                className="ps-input mono ps-input-coord"
                value={sStr}
                onChange={(e) => setSStr(e.target.value)}
              />
              <input
                aria-label="East longitude"
                className="ps-input mono ps-input-coord"
                value={eStr}
                onChange={(e) => setEStr(e.target.value)}
              />
              <input
                aria-label="North latitude"
                className="ps-input mono ps-input-coord"
                value={nStr}
                onChange={(e) => setNStr(e.target.value)}
              />
            </div>
            {bboxError && (
              <div style={{ fontSize: "11px", color: "var(--error)" }}>{bboxError}</div>
            )}
            <div style={{ display: "flex", gap: "6px", justifyContent: "flex-end" }}>
              <button
                type="button"
                className="ps-btn"
                onClick={() => setBboxOpen(false)}
              >
                Cancel
              </button>
              <button type="submit" className="ps-btn">
                Apply BBox
              </button>
            </div>
          </form>
        )}
      </div>

      {/* UTC Date Window */}
      <div className="ps-bar-group">
        <label htmlFor="ps-from-date" className="ps-bar-label">
          UTC
        </label>
        <input
          id="ps-from-date"
          type="date"
          aria-label="From UTC date"
          className="ps-input mono ps-input-date"
          value={params.from || resolvedFrom}
          onChange={(e) => {
            const nextFrom = e.target.value;
            const curTo = params.to || resolvedTo || nextFrom;
            if (nextFrom) {
              onChangeDates(nextFrom, curTo < nextFrom ? nextFrom : curTo);
            }
          }}
        />
        <span style={{ color: "var(--text-3)" }}>-</span>
        <input
          id="ps-to-date"
          type="date"
          aria-label="To UTC date"
          className="ps-input mono ps-input-date"
          value={params.to || resolvedTo}
          onChange={(e) => {
            const nextTo = e.target.value;
            const curFrom = params.from || resolvedFrom || nextTo;
            if (nextTo) {
              onChangeDates(curFrom > nextTo ? nextTo : curFrom, nextTo);
            }
          }}
        />
        <button
          type="button"
          className="ps-btn"
          onClick={onResetLiveDates}
          title="Snap back to the latest 48h live feed window"
        >
          48h Live
        </button>
      </div>

      {/* Confidence Filter */}
      <div className="ps-bar-group">
        <span className="ps-bar-label">Conf</span>
        <div className="ps-segmented" role="group" aria-label="Confidence filter">
          {(
            [
              [1, "All"],
              [2, "Nom+Hi"],
              [3, "High"],
            ] as const
          ).map(([lvl, text]) => (
            <button
              key={lvl}
              type="button"
              className="ps-seg-btn"
              aria-pressed={params.conf === lvl}
              onClick={() => onChangeConf(lvl)}
            >
              {text}
            </button>
          ))}
        </div>
      </div>

      {/* View Mode Switcher */}
      <div className="ps-bar-group" style={{ borderRight: 0, paddingRight: 0 }}>
        <span className="ps-bar-label">Layer</span>
        <div className="ps-segmented" role="group" aria-label="Map layer mode">
          {(
            [
              ["harmonized", "Harmonized FRP"],
              ["split", "Sensor Split"],
              ["raw", "Raw Count"],
            ] as const
          ).map(([modeVal, label]) => (
            <button
              key={modeVal}
              type="button"
              className="ps-seg-btn"
              aria-pressed={params.mode === modeVal}
              onClick={() => onChangeMode(modeVal)}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      {/* Data Freshness Telemetry */}
      <div className="ps-freshness">
        {anyPartial && <span className="ps-tag ps-tag-warn">Partial UTC Day</span>}
        {meta && (
          <span className="mono" title="Ingested records and latest live date">
            {formatNumber(meta.total_detections)} det
            {meta.live_max_date ? ` | ${meta.live_max_date}` : ""}
          </span>
        )}
      </div>
    </header>
  );
}
