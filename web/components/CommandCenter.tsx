"use client";

import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  fetchCalendar,
  fetchCell,
  fetchHourly,
  fetchMeta,
  fetchRegions,
  fetchSummary,
  fetchTimeline6h,
  fetchTimelineDay,
} from "@/lib/api";
import type {
  CalendarTab,
  ConfLevel,
  DashboardParams,
  HistoryMetric,
  RegionInfo,
  ViewMode,
} from "@/lib/types";
import { DEFAULT_PARAMS, parseDashboardParams, serializeDashboardParams } from "@/lib/urlState";
import { CalendarDock } from "./CalendarDock";
import { Inspector } from "./Inspector";
import { MapViewport, type MapViewportHandle } from "./MapViewport";
import { TopBar } from "./TopBar";

export function CommandCenter() {
  const [params, setParams] = useState<DashboardParams>(DEFAULT_PARAMS);
  const [hydrated, setHydrated] = useState(false);
  const mapRef = useRef<MapViewportHandle | null>(null);
  const initialFitDone = useRef(false);

  // Sync URL query string on mount without causing SSR hydration mismatch.
  useEffect(() => {
    const parsed = parseDashboardParams(window.location.search);
    setParams(parsed);
    setHydrated(true);
  }, []);

  useEffect(() => {
    if (!hydrated) return;
    const qs = serializeDashboardParams(params);
    const nextUrl = qs ? `${window.location.pathname}?${qs}` : window.location.pathname;
    window.history.replaceState(null, "", nextUrl);
  }, [params, hydrated]);

  const metaQuery = useQuery({
    queryKey: ["meta"],
    queryFn: ({ signal }) => fetchMeta(signal),
    staleTime: 120_000,
  });

  const regionsQuery = useQuery({
    queryKey: ["regions"],
    queryFn: ({ signal }) => fetchRegions(signal),
    staleTime: 300_000,
  });

  const regions: RegionInfo[] = useMemo(
    () => regionsQuery.data?.regions || [],
    [regionsQuery.data],
  );

  // If loaded with a deep-linked non-global region or bbox, fit viewport once data is ready.
  useEffect(() => {
    if (!hydrated || initialFitDone.current) return;
    if (params.bbox) {
      mapRef.current?.fitBounds(params.bbox);
      initialFitDone.current = true;
      return;
    }
    if (params.region !== "global" && regions.length > 0) {
      const target = regions.find((r) => r.id === params.region);
      if (target) {
        mapRef.current?.fitBounds(target.bbox);
        initialFitDone.current = true;
      }
    }
  }, [hydrated, params.region, params.bbox, regions]);

  const filterKey = useMemo(
    () => [
      params.region,
      params.bbox ? params.bbox.join(",") : "",
      params.from,
      params.to,
      params.conf,
    ],
    [params.region, params.bbox, params.from, params.to, params.conf],
  );

  const summaryQuery = useQuery({
    queryKey: ["summary", ...filterKey],
    queryFn: ({ signal }) => fetchSummary(params, signal),
    enabled: hydrated,
  });

  const hourlyQuery = useQuery({
    queryKey: ["hourly", ...filterKey],
    queryFn: ({ signal }) => fetchHourly(params, signal),
    enabled: hydrated,
  });

  const timelineDayQuery = useQuery({
    queryKey: ["timeline-day", ...filterKey],
    queryFn: ({ signal }) => fetchTimelineDay(params, signal),
    enabled: hydrated && params.tab === "live",
  });

  const timeline6hQuery = useQuery({
    queryKey: ["timeline-6h", ...filterKey],
    queryFn: ({ signal }) => fetchTimeline6h(params, signal),
    enabled: hydrated && params.tab === "live",
  });

  const calendarQuery = useQuery({
    queryKey: [
      "calendar-history",
      params.region,
      params.bbox ? params.bbox.join(",") : "",
      params.conf,
    ],
    queryFn: ({ signal }) => fetchCalendar(params, "history", signal),
    enabled: hydrated && params.tab === "history",
  });

  const cellQuery = useQuery({
    queryKey: ["cell", params.cellId, params.from, params.to, params.conf],
    queryFn: ({ signal }) => fetchCell(params.cellId!, params, signal),
    enabled: hydrated && params.cellId !== null,
  });

  const handleSelectRegion = useCallback(
    (regionId: string) => {
      setParams((prev) => ({
        ...prev,
        region: regionId,
        bbox: null,
        cellId: null,
      }));
      const found = regions.find((r) => r.id === regionId);
      if (found) {
        mapRef.current?.fitBounds(found.bbox);
      }
    },
    [regions],
  );

  const handleApplyBBox = useCallback((bbox: [number, number, number, number]) => {
    setParams((prev) => ({
      ...prev,
      region: "custom",
      bbox,
      cellId: null,
    }));
    mapRef.current?.fitBounds(bbox);
  }, []);

  const handleUseViewportBBox = useCallback(() => {
    const viewBBox = mapRef.current?.getViewportBBox();
    if (!viewBBox) return;
    setParams((prev) => ({
      ...prev,
      region: "custom",
      bbox: viewBBox,
      cellId: null,
    }));
  }, []);

  const handleChangeDates = useCallback((from: string, to: string) => {
    setParams((prev) => ({ ...prev, from, to }));
  }, []);

  const handleResetLiveDates = useCallback(() => {
    setParams((prev) => ({ ...prev, from: "", to: "" }));
  }, []);

  const handleChangeConf = useCallback((conf: ConfLevel) => {
    setParams((prev) => ({ ...prev, conf }));
  }, []);

  const handleChangeMode = useCallback((mode: ViewMode) => {
    setParams((prev) => ({ ...prev, mode }));
  }, []);

  const handleChangeTab = useCallback((tab: CalendarTab) => {
    setParams((prev) => ({ ...prev, tab }));
  }, []);

  const handleChangeMetric = useCallback((metric: HistoryMetric) => {
    setParams((prev) => ({ ...prev, metric }));
  }, []);

  const handleSelectCell = useCallback((cellId: number) => {
    setParams((prev) => ({ ...prev, cellId }));
  }, []);

  const handleClearCell = useCallback(() => {
    setParams((prev) => ({ ...prev, cellId: null }));
  }, []);

  const resolvedFrom =
    summaryQuery.data?.from || metaQuery.data?.live_min_date || "";
  const resolvedTo =
    summaryQuery.data?.to || metaQuery.data?.live_max_date || "";
  const floorMW = summaryQuery.data?.floor_mw || metaQuery.data?.floor_mw || 6.2;

  return (
    <div className="ps-shell">
      <TopBar
        params={params}
        regions={regions}
        meta={metaQuery.data}
        anyPartial={summaryQuery.data?.kpis.any_partial}
        resolvedFrom={resolvedFrom}
        resolvedTo={resolvedTo}
        onSelectRegion={handleSelectRegion}
        onApplyBBox={handleApplyBBox}
        onUseViewportBBox={handleUseViewportBBox}
        onChangeDates={handleChangeDates}
        onResetLiveDates={handleResetLiveDates}
        onChangeConf={handleChangeConf}
        onChangeMode={handleChangeMode}
      />

      <main className="ps-main">
        <MapViewport
          ref={mapRef}
          params={params}
          floorMW={floorMW}
          onSelectCell={handleSelectCell}
        />

        <Inspector
          summary={summaryQuery.data}
          summaryLoading={!hydrated || summaryQuery.isLoading}
          summaryError={summaryQuery.error instanceof Error ? summaryQuery.error.message : null}
          hourly={hourlyQuery.data}
          hourlyLoading={!hydrated || hourlyQuery.isLoading}
          cellId={params.cellId}
          cellData={cellQuery.data}
          cellLoading={cellQuery.isLoading}
          cellError={cellQuery.error instanceof Error ? cellQuery.error.message : null}
          onClearCell={handleClearCell}
        />
      </main>

      <CalendarDock
        params={params}
        regions={regions}
        timelineDay={timelineDayQuery.data}
        timeline6h={timeline6hQuery.data}
        liveLoading={!hydrated || timelineDayQuery.isLoading || timeline6hQuery.isLoading}
        liveError={
          timelineDayQuery.error instanceof Error
            ? timelineDayQuery.error.message
            : timeline6hQuery.error instanceof Error
              ? timeline6hQuery.error.message
              : null
        }
        calendar={calendarQuery.data}
        calendarLoading={!hydrated || calendarQuery.isLoading}
        calendarError={calendarQuery.error instanceof Error ? calendarQuery.error.message : null}
        onChangeTab={handleChangeTab}
        onChangeMetric={handleChangeMetric}
        onSelectDateRange={handleChangeDates}
        onResetLiveDates={handleResetLiveDates}
        onSelectRegion={handleSelectRegion}
      />
    </div>
  );
}
