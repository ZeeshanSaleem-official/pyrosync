"use client";

import maplibregl from "maplibre-gl";
import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import { tileUrlTemplate } from "@/lib/api";
import { formatMW, formatNumber, INFERNO_STOPS, SENSOR_COLORS } from "@/lib/colors";
import type { DashboardParams, ViewMode } from "@/lib/types";

export interface MapViewportHandle {
  fitBounds: (bbox: [number, number, number, number]) => void;
  getViewportBBox: () => [number, number, number, number] | null;
}

interface MapViewportProps {
  params: DashboardParams;
  floorMW: number;
  onSelectCell: (cellId: number) => void;
}

interface HoverFeature {
  kind: "cells10" | "cells" | "det";
  lng: number;
  lat: number;
  title: string;
  lines: string[];
}

function activeTierLabel(zoom: number): string {
  if (zoom < 6) return "A10 Grid (0.10 deg / ~123 km2)";
  if (zoom < 11) return "A1 Equal-Area Grid (0.01 deg / ~1.2 km2)";
  return "Sensor Pixel Footprints (scan x track km)";
}

function buildHeatmapFill(
  prop: string,
  stops: [number, string][],
): maplibregl.ExpressionSpecification {
  const expr: unknown[] = ["interpolate", ["linear"], ["coalesce", ["get", prop], 0]];
  for (const [val, color] of stops) {
    expr.push(val, color);
  }
  return expr as maplibregl.ExpressionSpecification;
}

function cellsFillExpression(
  mode: ViewMode,
  isA10: boolean,
): maplibregl.ExpressionSpecification {
  if (mode === "split") {
    return [
      "case",
      ["all", [">", ["coalesce", ["get", "modis_px"], 0], 0], [">", ["coalesce", ["get", "viirs_px"], 0], 0]],
      SENSOR_COLORS.both,
      [">", ["coalesce", ["get", "modis_px"], 0], 0],
      SENSOR_COLORS.modis,
      [">", ["coalesce", ["get", "viirs_px"], 0], 0],
      SENSOR_COLORS.viirs,
      SENSOR_COLORS.zero,
    ] as maplibregl.ExpressionSpecification;
  }

  if (mode === "raw") {
    const maxStops: [number, string][] = isA10
      ? [
          [1, INFERNO_STOPS[0]],
          [5, INFERNO_STOPS[1]],
          [20, INFERNO_STOPS[2]],
          [60, INFERNO_STOPS[3]],
          [200, INFERNO_STOPS[4]],
          [800, INFERNO_STOPS[5]],
        ]
      : [
          [1, INFERNO_STOPS[0]],
          [2, INFERNO_STOPS[1]],
          [5, INFERNO_STOPS[2]],
          [12, INFERNO_STOPS[3]],
          [25, INFERNO_STOPS[4]],
          [60, INFERNO_STOPS[5]],
        ];
    return buildHeatmapFill("raw_px", maxStops);
  }

  const frpStops: [number, string][] = isA10
    ? [
        [0, INFERNO_STOPS[0]],
        [25, INFERNO_STOPS[1]],
        [100, INFERNO_STOPS[2]],
        [400, INFERNO_STOPS[3]],
        [1500, INFERNO_STOPS[4]],
        [5000, INFERNO_STOPS[5]],
      ]
    : [
        [0, INFERNO_STOPS[0]],
        [15, INFERNO_STOPS[1]],
        [40, INFERNO_STOPS[2]],
        [120, INFERNO_STOPS[3]],
        [350, INFERNO_STOPS[4]],
        [1000, INFERNO_STOPS[5]],
      ];
  return buildHeatmapFill("harmonized_frp", frpStops);
}

function detFillExpression(mode: ViewMode): maplibregl.ExpressionSpecification {
  if (mode === "split") {
    return [
      "case",
      ["==", ["get", "sensor"], "MODIS"],
      SENSOR_COLORS.modis,
      SENSOR_COLORS.viirs,
    ] as maplibregl.ExpressionSpecification;
  }
  return buildHeatmapFill("frp_mw", [
    [0, INFERNO_STOPS[0]],
    [10, INFERNO_STOPS[1]],
    [30, INFERNO_STOPS[2]],
    [80, INFERNO_STOPS[3]],
    [200, INFERNO_STOPS[4]],
    [600, INFERNO_STOPS[5]],
  ]);
}

function detFillOpacity(mode: ViewMode): maplibregl.ExpressionSpecification {
  if (mode === "split") {
    return [
      "case",
      ["==", ["get", "sensor"], "MODIS"],
      0.2,
      ["==", ["get", "kept"], true],
      0.42,
      0.16,
    ] as maplibregl.ExpressionSpecification;
  }
  return [
    "case",
    ["==", ["get", "kept"], true],
    0.72,
    0.3,
  ] as maplibregl.ExpressionSpecification;
}

export const MapViewport = forwardRef<MapViewportHandle, MapViewportProps>(
  function MapViewport({ params, floorMW, onSelectCell }, ref) {
    const containerRef = useRef<HTMLDivElement | null>(null);
    const mapRef = useRef<maplibregl.Map | null>(null);
    const onSelectCellRef = useRef(onSelectCell);
    onSelectCellRef.current = onSelectCell;

    const [ready, setReady] = useState(false);
    const [zoom, setZoom] = useState(1.8);
    const [center, setCenter] = useState<[number, number]>([20, 15]);
    const [hover, setHover] = useState<HoverFeature | null>(null);

    const tileUrls = useMemo(
      () => ({
        cells10: tileUrlTemplate("cells10", params, params.mode),
        cells: tileUrlTemplate("cells", params, params.mode),
        det: tileUrlTemplate("det", params, params.mode),
      }),
      [params],
    );

    useImperativeHandle(ref, () => ({
      fitBounds(bbox: [number, number, number, number]) {
        const map = mapRef.current;
        if (!map) return;
        const [w, s, e, n] = bbox;
        if (w <= -179 && e >= 179 && s <= -85 && n >= 85) {
          map.easeTo({ center: [20, 15], zoom: 1.8, duration: 450 });
          return;
        }
        const safeW = Math.max(-180, Math.min(180, w));
        const safeE = Math.max(-180, Math.min(180, e));
        const safeS = Math.max(-84, Math.min(84, s));
        const safeN = Math.max(-84, Math.min(84, n));
        if (safeW <= safeE) {
          map.fitBounds(
            [
              [safeW, safeS],
              [safeE, safeN],
            ],
            { padding: 28, duration: 500, maxZoom: 12 },
          );
        }
      },
      getViewportBBox() {
        const map = mapRef.current;
        if (!map) return null;
        const b = map.getBounds();
        return [
          Math.max(-180, b.getWest()),
          Math.max(-89, b.getSouth()),
          Math.min(180, b.getEast()),
          Math.min(89, b.getNorth()),
        ];
      },
    }));

    useEffect(() => {
      if (!containerRef.current || mapRef.current) return;

      const map = new maplibregl.Map({
        container: containerRef.current,
        style: "/style.json",
        center: [20, 15],
        zoom: 1.8,
        minZoom: 0,
        maxZoom: 16,
        attributionControl: { compact: true },
      });

      map.addControl(new maplibregl.NavigationControl({ showCompass: false }), "top-right");
      map.addControl(new maplibregl.ScaleControl({ maxWidth: 120, unit: "metric" }), "bottom-right");

      map.on("load", () => {
        map.addSource("pyrosync-cells10", {
          type: "vector",
          tiles: [tileUrls.cells10],
          minzoom: 0,
          maxzoom: 5,
        });
        map.addSource("pyrosync-cells", {
          type: "vector",
          tiles: [tileUrls.cells],
          minzoom: 4,
          maxzoom: 10,
        });
        map.addSource("pyrosync-det", {
          type: "vector",
          tiles: [tileUrls.det],
          minzoom: 9,
          maxzoom: 14,
        });

        map.addLayer({
          id: "cells10-fill",
          type: "fill",
          source: "pyrosync-cells10",
          "source-layer": "cells10",
          minzoom: 0,
          maxzoom: 6,
          paint: {
            "fill-color": cellsFillExpression(params.mode, true),
            "fill-opacity": 0.78,
          },
        });
        map.addLayer({
          id: "cells10-line",
          type: "line",
          source: "pyrosync-cells10",
          "source-layer": "cells10",
          minzoom: 3,
          maxzoom: 6,
          paint: {
            "line-color": "#101214",
            "line-width": 0.5,
            "line-opacity": 0.65,
          },
        });

        map.addLayer({
          id: "cells-fill",
          type: "fill",
          source: "pyrosync-cells",
          "source-layer": "cells",
          minzoom: 6,
          maxzoom: 11,
          paint: {
            "fill-color": cellsFillExpression(params.mode, false),
            "fill-opacity": 0.8,
          },
        });
        map.addLayer({
          id: "cells-line",
          type: "line",
          source: "pyrosync-cells",
          "source-layer": "cells",
          minzoom: 6,
          maxzoom: 11,
          paint: {
            "line-color": "#101214",
            "line-width": 0.6,
            "line-opacity": 0.7,
          },
        });

        map.addLayer({
          id: "det-fill",
          type: "fill",
          source: "pyrosync-det",
          "source-layer": "det",
          minzoom: 11,
          maxzoom: 22,
          paint: {
            "fill-color": detFillExpression(params.mode),
            "fill-opacity": detFillOpacity(params.mode),
          },
        });
        map.addLayer({
          id: "det-outline-modis",
          type: "line",
          source: "pyrosync-det",
          "source-layer": "det",
          minzoom: 11,
          maxzoom: 22,
          filter: ["==", ["get", "sensor"], "MODIS"],
          paint: {
            "line-color": params.mode === "split" ? SENSOR_COLORS.modis : "#E8EAEB",
            "line-width": 1.5,
            "line-opacity": 0.9,
          },
        });
        map.addLayer({
          id: "det-outline-viirs",
          type: "line",
          source: "pyrosync-det",
          "source-layer": "det",
          minzoom: 11,
          maxzoom: 22,
          filter: ["==", ["get", "sensor"], "VIIRS"],
          paint: {
            "line-color": params.mode === "split" ? SENSOR_COLORS.viirs : "#101214",
            "line-width": 1.3,
            "line-dasharray": ["literal", [2, 1.5]],
            "line-opacity": 0.9,
          },
        });

        map.addLayer({
          id: "cells-selected",
          type: "line",
          source: "pyrosync-cells",
          "source-layer": "cells",
          minzoom: 6,
          maxzoom: 22,
          filter: ["==", ["get", "cell_a1"], -1],
          paint: {
            "line-color": "#E8EAEB",
            "line-width": 2,
          },
        });

        setReady(true);
      });

      map.on("moveend", () => {
        const c = map.getCenter();
        setZoom(map.getZoom());
        setCenter([c.lat, c.lng]);
      });

      map.on("mousemove", (e) => {
        const layers = ["det-fill", "cells-fill", "cells10-fill"].filter((id) =>
          Boolean(map.getLayer(id)),
        );
        if (layers.length === 0) return;
        const feats = map.queryRenderedFeatures(e.point, { layers });
        if (!feats.length) {
          map.getCanvas().style.cursor = "";
          setHover(null);
          return;
        }
        map.getCanvas().style.cursor = "pointer";
        const f = feats[0];
        const p = f.properties || {};
        if (f.layer.id === "det-fill") {
          setHover({
            kind: "det",
            lng: e.lngLat.lng,
            lat: e.lngLat.lat,
            title: `${String(p.sensor || "")} / ${String(p.satellite || "")}`,
            lines: [
              `FRP: ${formatMW(Number(p.frp_mw))} (${p.kept ? "Kept" : "< Floor"})`,
              `Footprint: ${Number(p.scan_km || 0).toFixed(2)} x ${Number(p.track_km || 0).toFixed(2)} km`,
              `UTC: ${String(p.acq_ts || "")}`,
            ],
          });
        } else if (f.layer.id === "cells-fill") {
          setHover({
            kind: "cells",
            lng: e.lngLat.lng,
            lat: e.lngLat.lat,
            title: `A1 Cell #${String(p.cell_a1 || "")}`,
            lines: [
              `Harmonized FRP: ${formatMW(Number(p.harmonized_frp))}`,
              `MODIS: ${formatNumber(Number(p.modis_px))} px | VIIRS: ${formatNumber(Number(p.viirs_px))} px`,
              "Click to audit raw satellite passes",
            ],
          });
        } else {
          setHover({
            kind: "cells10",
            lng: e.lngLat.lng,
            lat: e.lngLat.lat,
            title: `A10 Cell #${String(p.cell_a10 || "")}`,
            lines: [
              `Harmonized FRP: ${formatMW(Number(p.harmonized_frp))}`,
              `MODIS: ${formatNumber(Number(p.modis_px))} px | VIIRS: ${formatNumber(Number(p.viirs_px))} px`,
              "Click to zoom into A1 grid",
            ],
          });
        }
      });

      map.on("mouseleave", () => {
        map.getCanvas().style.cursor = "";
        setHover(null);
      });

      map.on("click", (e) => {
        const layers = ["det-fill", "cells-fill", "cells10-fill"].filter((id) =>
          Boolean(map.getLayer(id)),
        );
        if (layers.length === 0) return;
        const feats = map.queryRenderedFeatures(e.point, { layers });
        if (!feats.length) return;
        const f = feats[0];
        const p = f.properties || {};
        if (f.layer.id === "det-fill" || f.layer.id === "cells-fill") {
          const cid = Number(p.cell_a1);
          if (Number.isFinite(cid) && cid >= 0) {
            onSelectCellRef.current(cid);
          }
          return;
        }
        if (f.layer.id === "cells10-fill") {
          map.easeTo({
            center: [e.lngLat.lng, e.lngLat.lat],
            zoom: 7.5,
            duration: 450,
          });
        }
      });

      mapRef.current = map;
      return () => {
        map.remove();
        mapRef.current = null;
      };
      // Initialize MapLibre once on mount; dynamic updates flow through the reactive effect below.
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    // Keep tile URLs, paint styles, and selected cell outline synced without recreating the map.
    useEffect(() => {
      const map = mapRef.current;
      if (!map || !ready) return;

      const s10 = map.getSource("pyrosync-cells10") as maplibregl.VectorTileSource | undefined;
      const s1 = map.getSource("pyrosync-cells") as maplibregl.VectorTileSource | undefined;
      const sDet = map.getSource("pyrosync-det") as maplibregl.VectorTileSource | undefined;
      s10?.setTiles([tileUrls.cells10]);
      s1?.setTiles([tileUrls.cells]);
      sDet?.setTiles([tileUrls.det]);

      if (map.getLayer("cells10-fill")) {
        map.setPaintProperty("cells10-fill", "fill-color", cellsFillExpression(params.mode, true));
      }
      if (map.getLayer("cells-fill")) {
        map.setPaintProperty("cells-fill", "fill-color", cellsFillExpression(params.mode, false));
      }
      if (map.getLayer("det-fill")) {
        map.setPaintProperty("det-fill", "fill-color", detFillExpression(params.mode));
        map.setPaintProperty("det-fill", "fill-opacity", detFillOpacity(params.mode));
      }
      if (map.getLayer("det-outline-modis")) {
        map.setPaintProperty(
          "det-outline-modis",
          "line-color",
          params.mode === "split" ? SENSOR_COLORS.modis : "#E8EAEB",
        );
      }
      if (map.getLayer("det-outline-viirs")) {
        map.setPaintProperty(
          "det-outline-viirs",
          "line-color",
          params.mode === "split" ? SENSOR_COLORS.viirs : "#101214",
        );
      }
      if (map.getLayer("cells-selected")) {
        map.setFilter(
          "cells-selected",
          params.cellId !== null
            ? ["==", ["get", "cell_a1"], params.cellId]
            : ["==", ["get", "cell_a1"], -1],
        );
      }
    }, [ready, tileUrls, params.mode, params.cellId]);

    const gradientCSS = `linear-gradient(to right, ${INFERNO_STOPS.join(", ")})`;

    return (
      <div className="ps-map-wrap">
        <div
          ref={containerRef}
          className="ps-map-canvas"
          aria-label="Interactive MODIS and VIIRS active-fire vector map"
        />

        {/* Top-left coordinate and zoom tier readout */}
        <div className="ps-hud-box ps-hud-top-left mono">
          <span>Z{zoom.toFixed(1)}</span>
          <span style={{ color: "var(--text-3)" }}>|</span>
          <span>
            {center[0].toFixed(2)}, {center[1].toFixed(2)}
          </span>
          <span style={{ color: "var(--text-3)" }}>|</span>
          <span style={{ fontFamily: "var(--font-sans)", color: "var(--text-2)" }}>
            {activeTierLabel(zoom)}
          </span>
        </div>

        {/* Hover feature telemetry */}
        {hover && (
          <div className="ps-hud-box ps-hud-hover">
            <div style={{ fontWeight: 600, marginBottom: "3px" }}>{hover.title}</div>
            {hover.lines.map((line, i) => (
              <div key={i} className="mono" style={{ fontSize: "11px", color: "var(--text-2)" }}>
                {line}
              </div>
            ))}
          </div>
        )}

        {/* Bottom-left dynamic legend */}
        <div className="ps-hud-box ps-hud-bottom-left" aria-label="Map layer legend">
          {params.mode === "split" ? (
            <div>
              <div style={{ fontSize: "10px", fontWeight: 600, letterSpacing: "0.05em", textTransform: "uppercase", color: "var(--text-2)", marginBottom: "5px" }}>
                Okabe-Ito Sensor Audit
              </div>
              <div style={{ display: "flex", flexDirection: "column", gap: "4px", fontSize: "11px" }}>
                <div style={{ display: "flex", alignItems: "center", gap: "7px" }}>
                  <span style={{ width: "14px", height: "10px", backgroundColor: SENSOR_COLORS.modis, border: "1px solid #E8EAEB", display: "inline-block" }} />
                  <span>MODIS (1 km, solid stroke)</span>
                </div>
                <div style={{ display: "flex", alignItems: "center", gap: "7px" }}>
                  <span style={{ width: "14px", height: "10px", backgroundColor: SENSOR_COLORS.viirs, border: "1px dashed #101214", display: "inline-block" }} />
                  <span>VIIRS (375 m, dashed stroke)</span>
                </div>
                <div style={{ display: "flex", alignItems: "center", gap: "7px" }}>
                  <span style={{ width: "14px", height: "10px", backgroundColor: SENSOR_COLORS.both, border: "1px double #E8EAEB", display: "inline-block" }} />
                  <span>Both Verified (Cell Coincidence)</span>
                </div>
              </div>
            </div>
          ) : (
            <div>
              <div style={{ display: "flex", justifyContent: "space-between", fontSize: "10px", fontWeight: 600, letterSpacing: "0.05em", textTransform: "uppercase", color: "var(--text-2)" }}>
                <span>
                  {params.mode === "raw" ? "Raw Detections (px)" : "Harmonized FRP (MW)"}
                </span>
                <span className="mono" style={{ color: "var(--text-3)" }}>
                  {params.mode === "raw" ? "No Floor" : `Floor >= ${floorMW.toFixed(1)} MW`}
                </span>
              </div>
              <div className="ps-legend-ramp" style={{ background: gradientCSS }} />
              <div className="ps-legend-ticks mono">
                {params.mode === "raw" ? (
                  <>
                    <span>1</span>
                    <span>20</span>
                    <span>200</span>
                    <span>800+ px</span>
                  </>
                ) : (
                  <>
                    <span>0</span>
                    <span>100</span>
                    <span>1,000</span>
                    <span>5,000+ MW</span>
                  </>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
    );
  },
);
