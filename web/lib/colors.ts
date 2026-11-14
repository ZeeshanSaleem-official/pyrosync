import { interpolateInferno } from "d3-scale-chromatic";

/**
 * Exact Section 8.2 Inferno intensity stops sampled at t = 0.2, 0.4, 0.6, 0.7, 0.8, 1.0.
 */
export const INFERNO_STOPS = [
  "#420A68",
  "#932667",
  "#DD513A",
  "#F37819",
  "#FCA50A",
  "#FCFFA4",
] as const;

/**
 * True Okabe-Ito categorical palette for MODIS vs VIIRS vs verified overlap.
 */
export const SENSOR_COLORS = {
  modis: "#56B4E9",
  viirs: "#E69F00",
  both: "#CC79A7",
  zero: "#1F2327",
} as const;

export const Z_SCORE_BINS = [
  { label: "<= -2", color: "#2C7BB6" },
  { label: "-2..-1", color: "#7FB3D5" },
  { label: "-1..+1", color: "#3A4046" },
  { label: "+1..+2", color: "#FDAE61" },
  { label: "+2..+3", color: "#F46D43" },
  { label: ">= +3", color: "#D73027" },
] as const;

export function infernoColor(value: number, maxValue: number): string {
  if (!Number.isFinite(value) || value <= 0) {
    return SENSOR_COLORS.zero;
  }
  const safeMax = Math.max(1, maxValue);
  // Log scaling preserves detail across weak (10 MW) and extreme (10,000+ MW) days.
  const norm = Math.log1p(value) / Math.log1p(safeMax);
  const clamped = Math.min(1, Math.max(0, norm));
  const t = 0.2 + clamped * 0.8;
  return interpolateInferno(t);
}

export function robustZColor(z: number | null | undefined): string {
  if (z === null || z === undefined || !Number.isFinite(z)) {
    return SENSOR_COLORS.zero;
  }
  if (z <= -2) return "#2C7BB6";
  if (z < -1) return "#7FB3D5";
  if (z <= 1) return "#3A4046";
  if (z < 2) return "#FDAE61";
  if (z < 3) return "#F46D43";
  return "#D73027";
}

export function formatNumber(n: number | null | undefined, digits = 0): string {
  if (n === null || n === undefined || !Number.isFinite(n)) {
    return "--";
  }
  return n.toLocaleString("en-US", {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  });
}

export function formatMW(mw: number | null | undefined): string {
  if (mw === null || mw === undefined || !Number.isFinite(mw)) {
    return "--";
  }
  if (mw >= 10000) {
    return `${formatNumber(mw, 0)} MW`;
  }
  if (mw >= 100) {
    return `${formatNumber(mw, 1)} MW`;
  }
  return `${formatNumber(mw, 2)} MW`;
}

export function formatRatio(r: number | null | undefined): string {
  if (r === null || r === undefined || !Number.isFinite(r) || r <= 0) {
    return "--";
  }
  return `${r.toFixed(2)}x`;
}

function linearChannel(c: number): number {
  const srgb = c / 255;
  return srgb <= 0.03928 ? srgb / 12.92 : Math.pow((srgb + 0.055) / 1.055, 2.4);
}

export function relativeLuminance(hex: string): number {
  const clean = hex.replace(/^#/, "");
  const r = parseInt(clean.slice(0, 2), 16);
  const g = parseInt(clean.slice(2, 4), 16);
  const b = parseInt(clean.slice(4, 6), 16);
  return 0.2126 * linearChannel(r) + 0.7152 * linearChannel(g) + 0.0722 * linearChannel(b);
}

export function contrastRatio(hexA: string, hexB: string): number {
  const l1 = relativeLuminance(hexA);
  const l2 = relativeLuminance(hexB);
  const hi = Math.max(l1, l2);
  const lo = Math.min(l1, l2);
  return (hi + 0.05) / (lo + 0.05);
}
