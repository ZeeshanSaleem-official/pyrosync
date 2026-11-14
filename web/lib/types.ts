export type ViewMode = "harmonized" | "split" | "raw";
export type ConfLevel = 1 | 2 | 3;
export type CalendarTab = "live" | "history";
export type HistoryMetric = "frp" | "zscore";

export interface DashboardParams {
  region: string;
  bbox: [number, number, number, number] | null;
  from: string;
  to: string;
  conf: ConfLevel;
  mode: ViewMode;
  tab: CalendarTab;
  metric: HistoryMetric;
  cellId: number | null;
}

export interface SatelliteCount {
  sensor: "MODIS" | "VIIRS";
  satellite: string;
  status: "NRT" | "SP";
  count: number;
}

export interface IngestRunSummary {
  id: number;
  kind: string;
  source?: string;
  area?: string;
  started_at: string;
  finished_at?: string;
  rows_in: number;
  status: string;
}

export interface MetaResponse {
  version: string;
  params_version: number;
  floor_mw: number;
  grid: Record<string, number>;
  live_min_date?: string;
  live_max_date?: string;
  archive_min_date?: string;
  archive_max_date?: string;
  total_detections: number;
  satellites: SatelliteCount[];
  last_ingest?: IngestRunSummary;
  nrt_polling_enabled: boolean;
}

export interface RegionInfo {
  id: string;
  name: string;
  bbox: [number, number, number, number];
  has_history: boolean;
  history_from?: string;
  history_to?: string;
}

export interface RegionsResponse {
  regions: RegionInfo[];
}

export interface CalibrationModel {
  scope: "live" | "history";
  k: number;
  ci_low: number;
  ci_high: number;
  n_days: number;
  reliable: boolean;
  k_applied: number;
  params_version: number;
  label: string;
}

export interface LadderStep {
  step: number;
  key: string;
  label: string;
  modis_value: number;
  viirs_value: number;
  unit: string;
  ratio: number;
}

export interface AgreementStats {
  modis_cell_days: number;
  both_cell_days: number;
  overall_pct: number;
  full_day_modis_cell_days: number;
  full_day_both_cell_days: number;
  full_day_pct: number;
}

export interface DailyPoint {
  date: string;
  modis_frp?: number | null;
  viirs_all_frp?: number | null;
  viirs_meq_frp?: number | null;
  harmonized_frp: number;
  cell_union_frp: number;
  robust_z?: number | null;
  modis_px: number;
  viirs_px: number;
  cells_modis: number;
  cells_viirs_meq: number;
  cells_union: number;
  sats_modis: number;
  sats_viirs: number;
  is_partial: boolean;
}

export interface SummaryKPIs {
  raw_pixels_modis: number;
  raw_pixels_viirs: number;
  raw_pixels_total: number;
  harmonized_cells_union: number;
  harmonized_frp_mw: number;
  modis_frp_mw: number;
  viirs_all_frp_mw: number;
  viirs_meq_frp_mw: number;
  days: number;
  any_partial: boolean;
}

export interface SummaryResponse {
  region: string;
  region_name: string;
  from: string;
  to: string;
  conf_level: ConfLevel;
  floor_mw: number;
  kpis: SummaryKPIs;
  ladder: LadderStep[];
  a10_agreement: AgreementStats;
  calibration: CalibrationModel;
  daily: DailyPoint[];
}

export interface TimelineDayResponse {
  region: string;
  from: string;
  to: string;
  bucket: "day";
  calibration: CalibrationModel;
  series: DailyPoint[];
}

export interface Bucket6h {
  bucket_ts: string;
  modis_px: number;
  viirs_px: number;
  cells_union: number;
  modis_frp: number;
  viirs_meq_frp: number;
  harmonized_frp: number;
}

export interface Timeline6hResponse {
  region: string;
  from: string;
  to: string;
  bucket: "6h";
  series: Bucket6h[] | null;
}

export interface HourlyBucket {
  hour: number;
  modis_px: number;
  viirs_px: number;
  modis_frp: number;
  viirs_frp: number;
  modis_day_px: number;
  modis_night_px: number;
  viirs_day_px: number;
  viirs_night_px: number;
}

export interface HourlyResponse {
  region: string;
  from: string;
  to: string;
  conf_level: ConfLevel;
  hours: HourlyBucket[];
}

export interface CalendarYearSummary {
  year: number;
  days: number;
  total_frp_mw: number;
  max_z: number;
  era: string;
}

export interface EraBracket {
  era: string;
  from_year: number;
  to_year: number;
}

export interface CalendarResponse {
  region: string;
  region_name: string;
  mode: CalendarTab;
  has_history: boolean;
  from: string;
  to: string;
  conf_level: ConfLevel;
  calibration: CalibrationModel;
  era_brackets: EraBracket[] | null;
  years: CalendarYearSummary[];
  days: DailyPoint[];
}

export interface RawDetectionRow {
  id: number;
  sensor: "MODIS" | "VIIRS";
  satellite: string;
  lat: number;
  lon: number;
  bt_mwir_k: number;
  bt_lwir_k: number;
  scan_km: number;
  track_km: number;
  acq_ts: string;
  acq_date: string;
  conf_rank: number;
  conf_raw: string;
  frp_mw: number;
  daynight: "D" | "N";
  status: "NRT" | "SP";
  version: string;
  overpass_frp_mw: number;
  kept: boolean;
}

export interface CellResponse {
  cell_a1: number;
  cell_a10: number;
  center: [number, number];
  bounds: [number, number, number, number];
  area_km2: number;
  from: string;
  to: string;
  conf_level: ConfLevel;
  floor_mw: number;
  summary: {
    modis_px: number;
    viirs_px: number;
    modis_frp: number | null;
    viirs_frp_all: number | null;
    viirs_frp_meq: number | null;
    harmonized_frp: number | null;
  };
  detections: RawDetectionRow[];
}
