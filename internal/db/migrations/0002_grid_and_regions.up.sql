CREATE OR REPLACE FUNCTION grid_bounds(id BIGINT, res DOUBLE PRECISION)
RETURNS TABLE (lat0 DOUBLE PRECISION, lon0 DOUBLE PRECISION, lat1 DOUBLE PRECISION, lon1 DOUBLE PRECISION)
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  WITH p AS (
    SELECT (id / 40000)::int AS r, (id % 40000)::int AS c
  ), q AS (
    SELECT r, c,
           GREATEST(1, FLOOR(360.0 * COS(RADIANS((r + 0.5) * res - 90.0)) / res + 0.5))::int AS n
    FROM p
  )
  SELECT r * res - 90.0,
         c * (360.0 / n) - 180.0,
         r * res - 90.0 + res,
         c * (360.0 / n) - 180.0 + (360.0 / n)
  FROM q
$$;

CREATE OR REPLACE FUNCTION grid_center(id BIGINT, res DOUBLE PRECISION)
RETURNS TABLE (clat DOUBLE PRECISION, clon DOUBLE PRECISION)
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT (lat0 + lat1) / 2.0, (lon0 + lon1) / 2.0 FROM grid_bounds(id, res)
$$;

CREATE OR REPLACE FUNCTION make_bbox_multipolygon(
  w DOUBLE PRECISION,
  s DOUBLE PRECISION,
  e DOUBLE PRECISION,
  n DOUBLE PRECISION
) RETURNS geometry(MultiPolygon, 4326)
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT CASE
    WHEN w <= e THEN ST_Multi(ST_MakeEnvelope(w, s, e, n, 4326))
    ELSE ST_Multi(ST_Union(
      ST_MakeEnvelope(w, s, 180.0, n, 4326),
      ST_MakeEnvelope(-180.0, s, e, n, 4326)
    ))
  END
$$;

INSERT INTO regions (id, name, sort_order, west, south, east, north, geom)
VALUES
  ('global', 'Global', 1, -180.0, -90.0, 180.0, 90.0, make_bbox_multipolygon(-180.0, -90.0, 180.0, 90.0)),
  ('pakistan-indus', 'Pakistan & Indus Basin', 2, 60.8, 23.6, 77.8, 37.0, make_bbox_multipolygon(60.8, 23.6, 77.8, 37.0)),
  ('california', 'California', 3, -124.5, 32.5, -114.0, 42.1, make_bbox_multipolygon(-124.5, 32.5, -114.0, 42.1)),
  ('amazon', 'Amazon Basin', 4, -75.0, -20.0, -45.0, 5.0, make_bbox_multipolygon(-75.0, -20.0, -45.0, 5.0)),
  ('central-africa', 'Central Africa', 5, 10.0, -15.0, 35.0, 5.0, make_bbox_multipolygon(10.0, -15.0, 35.0, 5.0)),
  ('southeast-asia', 'Southeast Asia', 6, 92.0, -11.0, 141.0, 24.0, make_bbox_multipolygon(92.0, -11.0, 141.0, 24.0))
ON CONFLICT (id) DO NOTHING;
