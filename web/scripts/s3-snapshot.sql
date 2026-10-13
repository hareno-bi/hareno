-- S3 sample data for a fictional air cargo terminal, computed by the dev mart's Postgres. Airlines,
-- flights and figures are invented and generated deterministically, so every run gives the same
-- numbers. Prints two JSON lines: the chart sample set, then the matrix rows with server-side
-- subtotals. Amounts are in an unnamed currency.
\set ON_ERROR_STOP on

-- Twelve fictional airlines. Freighters carry far more per flight than passenger bellies.
CREATE TEMP TABLE airline (id int, name text, daily int, freighter bool);
INSERT INTO airline VALUES
    (1, 'Aurora Cargo', 3, true), (2, 'Pacific Crest', 2, false), (3, 'Meridian Air', 2, false),
    (4, 'Skyline Freight', 2, true), (5, 'Bluevale', 1, false), (6, 'Orion Cargo', 2, true),
    (7, 'Tasman Star', 1, false), (8, 'Kestrel Air', 1, false), (9, 'Novaris', 1, false),
    (10, 'Horizon Atlantic', 1, true), (11, 'Coral Line', 1, false), (12, 'Zephyr Express', 1, true);

CREATE TEMP TABLE cargo_type (name text, share numeric, yield numeric);  -- yield: revenue per kg
INSERT INTO cargo_type VALUES
    ('General', 0.50, 1.10), ('E-commerce', 0.22, 1.35), ('Perishables', 0.12, 1.80),
    ('Pharma', 0.08, 3.20), ('Dangerous goods', 0.08, 2.60);

-- One row per flight, flow and cargo type, over 24 months to the end of September 2026.
CREATE TEMP TABLE f AS
WITH flight AS (
    SELECT a.name AS airline, a.freighter, d::date AS day, n,
           a.name || ' ' || (100 * a.id + 2 * n + 1) AS flight_no,
           (d::date - date '2024-10-01') AS di,
           hashint4(a.id * 100000 + (d::date - date '2024-10-01') * 10 + n) AS h
    FROM airline a, generate_series(date '2024-10-01', date '2026-09-30', interval '1 day') d, generate_series(1, a.daily) n
),
operated AS (  -- the schedule grows: about 82% of slots flown at the start, all by the end
    SELECT * FROM flight WHERE abs(h) % 100 < 82 + di * 18 / 730
),
scaled AS (
    SELECT *, ((1 + di / 1460.0)                                   -- growth
              * (1 + 0.18 * cos((extract(month FROM day) - 11) * pi() / 6))  -- peak in Q4
              * (CASE WHEN freighter THEN 4 ELSE 1 END))::numeric AS scale
    FROM operated
)
SELECT airline, day, to_char(day, 'YYYY-MM') AS month, flight_no || ' · ' || to_char(day, 'DD Mon YY') AS flight,
       flow, ct.name AS cargo_type,
       round(t * ct.share * (0.8 + (abs(hashint4(h + length(ct.name))) % 40) / 100.0), 2) AS tonnes,
       -- yield varies by flow (imports pay storage, transhipment pays less) and by airline contract
       round(t * ct.share * ct.yield * 1000 * fy * (0.85 + (abs(h >> 12) % 30) / 100.0)
             * (0.95 + (abs(hashint4(h + 7 * length(ct.name))) % 10) / 100.0)) AS revenue,
       -- ULDs built up before the airline's cut-off: dips in peak season
       (0.955 - 0.025 * (1 + cos((extract(month FROM day) - 11) * pi() / 6))        -- worst in the Q4 peak
              - 0.01 * sin(di / 45.0) - (abs(h) % 30) / 1000.0)::numeric AS on_time
FROM scaled,
     LATERAL (VALUES ('Export', scale * (8 + abs(h) % 10), 1.0),
                     ('Import', scale * (6 + abs(h >> 4) % 8), 1.15),
                     ('Transhipment', scale * (3 + abs(h >> 8) % 5), 0.75)) v(flow, t, fy),
     cargo_type ct;

-- Chart sample set.
WITH months AS (SELECT DISTINCT month FROM f ORDER BY month),
by_dim AS (
    SELECT 'flow' AS dim, flow AS name, month, sum(tonnes) AS v FROM f GROUP BY flow, month
    UNION ALL
    SELECT 'cargo', cargo_type, month, sum(tonnes) FROM f GROUP BY cargo_type, month
),
series AS (SELECT dim, name, json_agg(round(v) ORDER BY month) AS data FROM by_dim GROUP BY dim, name),
monthly AS (
    SELECT month, sum(tonnes) AS tonnes, sum(on_time * tonnes) / sum(tonnes) AS on_time FROM f GROUP BY month
),
periods AS (
    SELECT month > '2025-09' AS current, sum(tonnes) AS tonnes, count(DISTINCT flight) AS flights,
           sum(revenue) AS revenue, sum(on_time * tonnes) / sum(tonnes) AS on_time
    FROM f GROUP BY 1
),
airlines AS (
    SELECT airline, sum(tonnes) AS tonnes FROM f WHERE month > '2025-09' GROUP BY airline ORDER BY tonnes DESC LIMIT 10
),
-- 400 sample shipments: weight against dwell time in the terminal, by flow.
shipments AS (
    SELECT round(exp(4 + (abs(hashint4(s)) % 1000) / 1000.0 * 4.5)) AS kg,
           round((CASE fl WHEN 'Export' THEN 6 WHEN 'Import' THEN 12 ELSE 9 END
                  + (abs(hashint4(s * 3)) % 1000) / 1000.0 * (CASE fl WHEN 'Import' THEN 48 ELSE 30 END))::numeric, 1) AS hours,
           fl AS flow
    FROM generate_series(1, 400) s, LATERAL (SELECT (ARRAY['Export', 'Import', 'Transhipment'])[1 + s % 3] AS fl) x
),
-- Truck arrivals at the landside docks by weekday and hour, averaged over 12 weeks.
trucks AS (
    SELECT dow, hr, round((CASE WHEN dow >= 5 THEN 0.45 ELSE 1 END)
                 * (4 + 22 * exp(-((hr - 9.5) ^ 2) / 6) + 16 * exp(-((hr - 17) ^ 2) / 5))
                 * (0.9 + (abs(hashint4(dow * 24 + hr)) % 20) / 100.0)) AS trucks
    FROM generate_series(0, 6) dow, generate_series(0, 23) hr
)
SELECT json_build_object(
    'months', (SELECT json_agg(month) FROM months),
    'byFlow', (SELECT json_agg(json_build_object('name', name, 'data', data) ORDER BY name) FROM series WHERE dim = 'flow'),
    'byCargo', (SELECT json_agg(json_build_object('name', s.name, 'data', data) ORDER BY ct.share DESC)
                FROM series s JOIN cargo_type ct ON ct.name = s.name WHERE dim = 'cargo'),
    'monthly', (SELECT json_agg(json_build_object('tonnes', round(tonnes), 'onTime', round(on_time, 4)) ORDER BY month) FROM monthly),
    'kpi', (SELECT json_object_agg(CASE WHEN current THEN 'current' ELSE 'prior' END,
                json_build_object('tonnes', round(tonnes), 'flights', flights, 'revenue', round(revenue), 'onTime', round(on_time, 4)))
            FROM periods),
    'airlines', (SELECT json_agg(json_build_object('name', airline, 'tonnes', round(tonnes)) ORDER BY tonnes DESC) FROM airlines),
    'shipments', (SELECT json_agg(json_build_array(kg, hours, flow)) FROM shipments),
    'trucks', (SELECT json_agg(json_build_array(hr, dow, trucks)) FROM trucks),
    'occupancy', 0.78
);

-- Matrix: airline > flight rows, flow columns, with subtotals and grand total from ROLLUP. Yield
-- (revenue per kg) is recomputed from sums at every level, never averaged. lvl: 0 flight,
-- 1 airline, 3 total.
SELECT json_agg(json_build_array(lvl, airline, flight, ex_t, ex_y, im_t, im_y, tr_t, tr_y, t_t, t_y)
                ORDER BY airline IS NULL, airline, flight IS NOT NULL, day DESC, flight)
FROM (
    SELECT GROUPING(airline, flight) AS lvl, airline, flight, min(day) AS day,
           round(sum(tonnes) FILTER (WHERE flow = 'Export'), 1) AS ex_t,
           round(sum(revenue) FILTER (WHERE flow = 'Export') / nullif(sum(tonnes) FILTER (WHERE flow = 'Export') * 1000, 0), 3) AS ex_y,
           round(sum(tonnes) FILTER (WHERE flow = 'Import'), 1) AS im_t,
           round(sum(revenue) FILTER (WHERE flow = 'Import') / nullif(sum(tonnes) FILTER (WHERE flow = 'Import') * 1000, 0), 3) AS im_y,
           round(sum(tonnes) FILTER (WHERE flow = 'Transhipment'), 1) AS tr_t,
           round(sum(revenue) FILTER (WHERE flow = 'Transhipment') / nullif(sum(tonnes) FILTER (WHERE flow = 'Transhipment') * 1000, 0), 3) AS tr_y,
           round(sum(tonnes), 1) AS t_t,
           round(sum(revenue) / (sum(tonnes) * 1000), 3) AS t_y
    FROM f GROUP BY ROLLUP (airline, flight)
) x;
