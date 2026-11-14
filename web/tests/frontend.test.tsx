import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import AboutPage from "@/app/about/page";
import { CalendarDock } from "@/components/CalendarDock";
import { HourlyCycleChart, Inspector, ProofLadder } from "@/components/Inspector";
import { detectionsToCSV, tileUrlTemplate } from "@/lib/api";
import {
  contrastRatio,
  INFERNO_STOPS,
  infernoColor,
  robustZColor,
  SENSOR_COLORS,
} from "@/lib/colors";
import type {
  CalendarResponse,
  CellResponse,
  HourlyResponse,
  RegionInfo,
  SummaryResponse,
} from "@/lib/types";
import {
  DEFAULT_PARAMS,
  parseDashboardParams,
  serializeDashboardParams,
} from "@/lib/urlState";

describe("Scientific color scales & WCAG contrast (T-14)", () => {
  test("all Tactical Graphite text tokens satisfy >= 4.5:1 AA contrast", () => {
    const surfaces = ["#101214", "#171A1D", "#1F2327"];
    const textTokens = ["#E8EAEB", "#A3AAB0", "#8A9299"];
    for (const bg of surfaces) {
      for (const fg of textTokens) {
        expect(contrastRatio(fg, bg)).toBeGreaterThanOrEqual(4.5);
      }
    }
  });

  test("inferno and robustZ palettes map boundary values cleanly", () => {
    expect(infernoColor(0, 1000)).toBe(SENSOR_COLORS.zero);
    expect(infernoColor(500, 1000)).toMatch(/^(#|rgb)/);
    expect(INFERNO_STOPS).toHaveLength(6);
    expect(robustZColor(null)).toBe(SENSOR_COLORS.zero);
    expect(robustZColor(-2.5)).toBe("#2C7BB6");
    expect(robustZColor(0.2)).toBe("#3A4046");
    expect(robustZColor(3.4)).toBe("#D73027");
  });
});

describe("URL state serialization & API builders", () => {
  test("roundtrips regional and custom bbox query params", () => {
    const parsed = parseDashboardParams(
      "?region=pakistan-indus&from=2026-11-12&to=2026-11-14&conf=3&mode=split&tab=history&metric=zscore&cell=486021681",
    );
    expect(parsed).toEqual({
      region: "pakistan-indus",
      bbox: null,
      from: "2026-11-12",
      to: "2026-11-14",
      conf: 3,
      mode: "split",
      tab: "history",
      metric: "zscore",
      cellId: 486021681,
    });
    const encoded = serializeDashboardParams(parsed);
    expect(parseDashboardParams(`?${encoded}`)).toEqual(parsed);

    const badBBox = parseDashboardParams("?bbox=10,40,20,30"); // south >= north rejected
    expect(badBBox.bbox).toBeNull();
  });

  test("builds MVT tile URLs and escapes CSV exports safely", () => {
    const url = tileUrlTemplate("cells", DEFAULT_PARAMS, "split");
    expect(url).toContain("/tiles/cells/{z}/{x}/{y}.mvt");
    expect(url).toContain("mode=split");
    expect(url).toContain("conf=2");

    const cell: CellResponse = {
      cell_a1: 486021681,
      cell_a10: 4860025,
      center: [31.505, 74.305],
      bounds: [74.3, 31.5, 74.31, 31.51],
      area_km2: 1.24,
      from: "2026-11-13",
      to: "2026-11-14",
      conf_level: 2,
      floor_mw: 6.2,
      summary: {
        modis_px: 1,
        viirs_px: 2,
        modis_frp: 320,
        viirs_frp_all: 305,
        viirs_frp_meq: 305,
        harmonized_frp: 312.5,
      },
      detections: [
        {
          id: 101,
          sensor: "MODIS",
          satellite: "AQUA",
          lat: 31.5,
          lon: 74.3,
          bt_mwir_k: 365,
          bt_lwir_k: 301,
          scan_km: 1.0,
          track_km: 1.0,
          acq_ts: "2026-11-14T08:15:00Z",
          acq_date: "2026-11-14",
          conf_rank: 3,
          conf_raw: "95",
          frp_mw: 320,
          daynight: "D",
          status: "NRT",
          version: "6.1NRT",
          overpass_frp_mw: 320,
          kept: true,
        },
      ],
    };
    const csv = detectionsToCSV(cell);
    expect(csv.split("\n")).toHaveLength(2);
    expect(csv).toContain("486021681,MODIS,AQUA");
  });
});

describe("React component rendering", () => {
  const sampleSummary: SummaryResponse = {
    region: "global",
    region_name: "Global",
    from: "2026-11-12",
    to: "2026-11-14",
    conf_level: 2,
    floor_mw: 6.2,
    kpis: {
      raw_pixels_modis: 18306,
      raw_pixels_viirs: 87068,
      raw_pixels_total: 105374,
      harmonized_cells_union: 43638,
      harmonized_frp_mw: 1214284,
      modis_frp_mw: 550033,
      viirs_all_frp_mw: 826014,
      viirs_meq_frp_mw: 765124,
      days: 3,
      any_partial: true,
    },
    ladder: [
      { step: 1, key: "raw_pixels", label: "Raw Pixels", modis_value: 18306, viirs_value: 87068, unit: "px", ratio: 4.76 },
      { step: 2, key: "equal_area_cells", label: "Equal-Area Cells (A1)", modis_value: 16895, viirs_value: 55510, unit: "cells", ratio: 3.29 },
      { step: 3, key: "modis_floor_cells", label: "Floor-Aligned Cells (>= 6.2 MW)", modis_value: 16895, viirs_value: 29715, unit: "cells", ratio: 1.76 },
      { step: 4, key: "radiative_power", label: "Floor-Aligned FRP", modis_value: 550033, viirs_value: 765124, unit: "MW", ratio: 1.39 },
    ],
    a10_agreement: {
      modis_cell_days: 8732,
      both_cell_days: 4815,
      overall_pct: 55.1,
      full_day_modis_cell_days: 7900,
      full_day_both_cell_days: 4526,
      full_day_pct: 57.3,
    },
    calibration: {
      scope: "live",
      k: 1,
      ci_low: 0.9,
      ci_high: 1.1,
      n_days: 2,
      reliable: false,
      k_applied: 1,
      params_version: 1,
      label: "Gain: 1.00x (Unverified - 2 overlap days < 14)",
    },
    daily: [
      {
        date: "2026-11-12",
        modis_frp: 250000,
        viirs_meq_frp: 350000,
        harmonized_frp: 608878,
        cell_union_frp: 608878,
        modis_px: 8800,
        viirs_px: 42000,
        cells_modis: 8000,
        cells_viirs_meq: 14000,
        cells_union: 21754,
        sats_modis: 2,
        sats_viirs: 3,
        is_partial: false,
      },
      {
        date: "2026-11-14",
        modis_frp: 22000,
        viirs_meq_frp: 31000,
        harmonized_frp: 54240,
        cell_union_frp: 54240,
        modis_px: 700,
        viirs_px: 3800,
        cells_modis: 650,
        cells_viirs_meq: 1200,
        cells_union: 1804,
        sats_modis: 2,
        sats_viirs: 2,
        is_partial: true,
      },
    ],
  };

  test("Inspector and ProofLadder render exact golden ladder ratios", () => {
    const ladderHtml = renderToStaticMarkup(<ProofLadder steps={sampleSummary.ladder} />);
    expect(ladderHtml).toContain("4.76x");
    expect(ladderHtml).toContain("3.29x");
    expect(ladderHtml).toContain("1.76x");
    expect(ladderHtml).toContain("1.39x");

    const hourlyData: HourlyResponse = {
      region: "global",
      from: "2026-11-12",
      to: "2026-11-14",
      conf_level: 2,
      hours: Array.from({ length: 24 }, (_, hour) => ({
        hour,
        modis_px: hour === 8 ? 1200 : 20,
        viirs_px: hour === 8 ? 4800 : 50,
        modis_frp: 500,
        viirs_frp: 620,
        modis_day_px: 15,
        modis_night_px: 5,
        viirs_day_px: 40,
        viirs_night_px: 10,
      })),
    };
    const chartHtml = renderToStaticMarkup(<HourlyCycleChart hourly={hourlyData} />);
    expect(chartHtml).toContain("00Z");
    expect(chartHtml).toContain("18Z");

    const inspHtml = renderToStaticMarkup(
      <Inspector
        summary={sampleSummary}
        summaryLoading={false}
        summaryError={null}
        hourly={hourlyData}
        hourlyLoading={false}
        cellId={null}
        cellLoading={false}
        cellError={null}
        onClearCell={() => {}}
      />,
    );
    expect(inspHtml).toContain("55.1%");
    expect(inspHtml).toContain("Includes partial UTC boundary day");
  });

  test("CalendarDock marks partial live days and renders history era brackets", () => {
    const regions: RegionInfo[] = [
      { id: "global", name: "Global", bbox: [-180, -90, 180, 90], has_history: false },
      {
        id: "pakistan-indus",
        name: "Pakistan & Indus Basin",
        bbox: [60.8, 23.6, 77.8, 37.0],
        has_history: true,
        history_from: "2010-01-01",
        history_to: "2026-11-14",
      },
    ];

    const liveHtml = renderToStaticMarkup(
      <CalendarDock
        params={DEFAULT_PARAMS}
        regions={regions}
        timelineDay={{
          region: "global",
          from: "2026-11-12",
          to: "2026-11-14",
          bucket: "day",
          calibration: sampleSummary.calibration,
          series: sampleSummary.daily,
        }}
        liveLoading={false}
        liveError={null}
        calendarLoading={false}
        calendarError={null}
        onChangeTab={() => {}}
        onChangeMetric={() => {}}
        onSelectDateRange={() => {}}
        onResetLiveDates={() => {}}
        onSelectRegion={() => {}}
      />,
    );
    expect(liveHtml).toContain('data-partial="true"');
    expect(liveHtml).toContain("2026-11-14");

    const histCal: CalendarResponse = {
      region: "pakistan-indus",
      region_name: "Pakistan & Indus Basin",
      mode: "history",
      has_history: true,
      from: "2010-01-01",
      to: "2026-11-14",
      conf_level: 2,
      calibration: { ...sampleSummary.calibration, scope: "history", reliable: true, n_days: 300 },
      era_brackets: [
        { era: "MODIS Only", from_year: 2010, to_year: 2011 },
        { era: "MODIS + VIIRS Constellation", from_year: 2012, to_year: 2026 },
      ],
      years: [
        { year: 2010, days: 365, total_frp_mw: 12000, max_z: 2.8, era: "MODIS Only" },
        { year: 2026, days: 3, total_frp_mw: 5400, max_z: 3.1, era: "MODIS + VIIRS Constellation" },
      ],
      days: sampleSummary.daily,
    };

    const histHtml = renderToStaticMarkup(
      <CalendarDock
        params={{ ...DEFAULT_PARAMS, region: "pakistan-indus", tab: "history" }}
        regions={regions}
        liveLoading={false}
        liveError={null}
        calendar={histCal}
        calendarLoading={false}
        calendarError={null}
        onChangeTab={() => {}}
        onChangeMetric={() => {}}
        onSelectDateRange={() => {}}
        onResetLiveDates={() => {}}
        onSelectRegion={() => {}}
      />,
    );
    expect(histHtml).toContain("MODIS Only (2010-2011)");
    expect(histHtml).toContain("MODIS + VIIRS Constellation (2012-2026)");
  });

  test("AboutPage contains NASA FIRMS and AI tool declarations", () => {
    const html = renderToStaticMarkup(<AboutPage />);
    expect(html).toContain("Space Agency Data Declaration");
    expect(html).toContain("MODIS Collection 6.1");
    expect(html).toContain("VIIRS 375 m Collection 2");
    expect(html).toContain("AI Tool Declaration");
  });
});
