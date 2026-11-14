CREATE OR REPLACE FUNCTION grid_cell_id(lat DOUBLE PRECISION, lon DOUBLE PRECISION, res DOUBLE PRECISION)
RETURNS BIGINT
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  WITH r AS (
    SELECT LEAST(
      GREATEST(0, FLOOR((lat + 90.0) / res + 1e-9)::int),
      ROUND(180.0 / res)::int - 1
    ) AS row_idx
  ), n AS (
    SELECT row_idx,
           GREATEST(1, FLOOR(360.0 * COS(RADIANS((row_idx + 0.5) * res - 90.0)) / res + 0.5))::int AS num_cols
    FROM r
  )
  SELECT row_idx::bigint * 40000 + LEAST(
    GREATEST(0, FLOOR((lon + 180.0) / (360.0 / num_cols))::int),
    num_cols - 1
  )::bigint
  FROM n
$$;
