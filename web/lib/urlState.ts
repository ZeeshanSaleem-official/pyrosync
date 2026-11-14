import type { CalendarTab, ConfLevel, DashboardParams, HistoryMetric, ViewMode } from "./types";

export const DEFAULT_PARAMS: DashboardParams = {
  region: "global",
  bbox: null,
  from: "",
  to: "",
  conf: 2,
  mode: "harmonized",
  tab: "live",
  metric: "frp",
  cellId: null,
};

export function parseBBoxParam(raw: string | null): [number, number, number, number] | null {
  if (!raw) return null;
  const parts = raw.split(",").map((s) => Number(s.trim()));
  if (parts.length !== 4 || parts.some((v) => !Number.isFinite(v))) {
    return null;
  }
  const [w, s, e, n] = parts;
  if (w < -180 || w > 180 || e < -180 || e > 180 || s < -90 || s > 90 || n < -90 || n > 90 || s >= n) {
    return null;
  }
  return [w, s, e, n];
}

export function parseDashboardParams(search: string): DashboardParams {
  const q = new URLSearchParams(search);
  const out: DashboardParams = { ...DEFAULT_PARAMS };

  const bbox = parseBBoxParam(q.get("bbox"));
  if (bbox) {
    out.bbox = bbox;
    out.region = "custom";
  } else if (q.get("region")) {
    out.region = q.get("region")!.trim();
  }

  const dateRe = /^\d{4}-\d{2}-\d{2}$/;
  const from = (q.get("from") || "").trim();
  const to = (q.get("to") || "").trim();
  if (dateRe.test(from)) out.from = from;
  if (dateRe.test(to)) out.to = to;

  const confRaw = Number(q.get("conf"));
  if (confRaw === 1 || confRaw === 2 || confRaw === 3) {
    out.conf = confRaw as ConfLevel;
  }

  const mode = (q.get("mode") || "").trim();
  if (mode === "harmonized" || mode === "split" || mode === "raw") {
    out.mode = mode as ViewMode;
  }

  const tab = (q.get("tab") || "").trim();
  if (tab === "live" || tab === "history") {
    out.tab = tab as CalendarTab;
  }

  const metric = (q.get("metric") || "").trim();
  if (metric === "frp" || metric === "zscore") {
    out.metric = metric as HistoryMetric;
  }

  const cellRaw = q.get("cell");
  if (cellRaw !== null && /^\d+$/.test(cellRaw.trim())) {
    out.cellId = Number(cellRaw.trim());
  }

  return out;
}

export function serializeDashboardParams(params: DashboardParams): string {
  const q = new URLSearchParams();
  if (params.bbox) {
    q.set("bbox", params.bbox.map((v) => v.toFixed(4)).join(","));
  } else if (params.region && params.region !== "global") {
    q.set("region", params.region);
  }
  if (params.from) q.set("from", params.from);
  if (params.to) q.set("to", params.to);
  if (params.conf !== 2) q.set("conf", String(params.conf));
  if (params.mode !== "harmonized") q.set("mode", params.mode);
  if (params.tab !== "live") q.set("tab", params.tab);
  if (params.metric !== "frp") q.set("metric", params.metric);
  if (params.cellId !== null) q.set("cell", String(params.cellId));
  return q.toString();
}
