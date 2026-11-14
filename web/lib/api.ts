import type {
  CalendarResponse,
  CalendarTab,
  CellResponse,
  DashboardParams,
  HourlyResponse,
  MetaResponse,
  RegionsResponse,
  SummaryResponse,
  Timeline6hResponse,
  TimelineDayResponse,
  ViewMode,
} from "./types";

export function getApiBase(): string {
  const envBase = (process.env.NEXT_PUBLIC_API_BASE || "").trim().replace(/\/+$/, "");
  if (envBase) {
    return envBase;
  }
  return "/api/v1";
}

export function buildFilterSearchParams(
  params: Pick<DashboardParams, "region" | "bbox" | "from" | "to" | "conf">,
  extra?: Record<string, string | undefined>,
): URLSearchParams {
  const qs = new URLSearchParams();
  if (params.bbox) {
    qs.set("bbox", params.bbox.map((v) => v.toFixed(4)).join(","));
  } else if (params.region) {
    qs.set("region", params.region);
  }
  if (params.from) {
    qs.set("from", params.from);
  }
  if (params.to) {
    qs.set("to", params.to);
  }
  qs.set("conf", String(params.conf));
  if (extra) {
    for (const [k, v] of Object.entries(extra)) {
      if (v !== undefined && v !== "") {
        qs.set(k, v);
      }
    }
  }
  return qs;
}

async function fetchJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const url = `${getApiBase()}${path}`;
  const res = await fetch(url, {
    method: "GET",
    headers: { Accept: "application/json" },
    signal,
  });
  if (!res.ok) {
    let msg = `API error (${res.status})`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body && typeof body.error === "string" && body.error) {
        msg = body.error;
      }
    } catch {
      // Ignore non-JSON error body
    }
    throw new Error(msg);
  }
  return (await res.json()) as T;
}

export function fetchMeta(signal?: AbortSignal): Promise<MetaResponse> {
  return fetchJSON<MetaResponse>("/meta", signal);
}

export function fetchRegions(signal?: AbortSignal): Promise<RegionsResponse> {
  return fetchJSON<RegionsResponse>("/regions", signal);
}

export function fetchSummary(params: DashboardParams, signal?: AbortSignal): Promise<SummaryResponse> {
  const qs = buildFilterSearchParams(params);
  return fetchJSON<SummaryResponse>(`/summary?${qs.toString()}`, signal);
}

export function fetchTimelineDay(
  params: DashboardParams,
  signal?: AbortSignal,
): Promise<TimelineDayResponse> {
  const qs = buildFilterSearchParams(params, { bucket: "day" });
  return fetchJSON<TimelineDayResponse>(`/timeline?${qs.toString()}`, signal);
}

export function fetchTimeline6h(
  params: DashboardParams,
  signal?: AbortSignal,
): Promise<Timeline6hResponse> {
  const qs = buildFilterSearchParams(params, { bucket: "6h" });
  return fetchJSON<Timeline6hResponse>(`/timeline?${qs.toString()}`, signal);
}

export function fetchHourly(params: DashboardParams, signal?: AbortSignal): Promise<HourlyResponse> {
  const qs = buildFilterSearchParams(params);
  return fetchJSON<HourlyResponse>(`/hourly?${qs.toString()}`, signal);
}

export function fetchCalendar(
  params: DashboardParams,
  mode: CalendarTab,
  signal?: AbortSignal,
): Promise<CalendarResponse> {
  const qs =
    mode === "history"
      ? buildFilterSearchParams(
          { region: params.region, bbox: params.bbox, from: "", to: "", conf: params.conf },
          { mode },
        )
      : buildFilterSearchParams(params, { mode });
  return fetchJSON<CalendarResponse>(`/calendar?${qs.toString()}`, signal);
}

export function fetchCell(
  cellId: number,
  params: DashboardParams,
  signal?: AbortSignal,
): Promise<CellResponse> {
  const qs = new URLSearchParams();
  if (params.from) qs.set("from", params.from);
  if (params.to) qs.set("to", params.to);
  qs.set("conf", String(params.conf));
  return fetchJSON<CellResponse>(`/cell/${cellId}?${qs.toString()}`, signal);
}

export function tileUrlTemplate(
  layer: "cells10" | "cells" | "det",
  params: DashboardParams,
  mode: ViewMode,
): string {
  const qs = buildFilterSearchParams(
    { region: params.region, bbox: params.bbox, from: params.from, to: params.to, conf: params.conf },
    { mode },
  );
  const base = getApiBase();
  const originPrefix =
    typeof window !== "undefined" && base.startsWith("/") ? window.location.origin : "";
  return `${originPrefix}${base}/tiles/${layer}/{z}/{x}/{y}.mvt?${qs.toString()}`;
}

function escapeCSV(val: string | number | boolean): string {
  const s = String(val);
  if (s.includes(",") || s.includes('"') || s.includes("\n")) {
    return `"${s.replace(/"/g, '""')}"`;
  }
  return s;
}

export function detectionsToCSV(cell: CellResponse): string {
  const headers = [
    "id",
    "cell_a1",
    "sensor",
    "satellite",
    "acq_ts",
    "daynight",
    "lat",
    "lon",
    "frp_mw",
    "overpass_frp_mw",
    "kept",
    "bt_mwir_k",
    "bt_lwir_k",
    "scan_km",
    "track_km",
    "conf_rank",
    "conf_raw",
    "status",
    "version",
  ];
  const lines = [headers.join(",")];
  for (const d of cell.detections) {
    lines.push(
      [
        d.id,
        cell.cell_a1,
        d.sensor,
        d.satellite,
        d.acq_ts,
        d.daynight,
        d.lat,
        d.lon,
        d.frp_mw,
        d.overpass_frp_mw,
        d.kept,
        d.bt_mwir_k,
        d.bt_lwir_k,
        d.scan_km,
        d.track_km,
        d.conf_rank,
        d.conf_raw,
        d.status,
        d.version,
      ]
        .map(escapeCSV)
        .join(","),
    );
  }
  return lines.join("\n");
}
