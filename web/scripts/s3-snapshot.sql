-- S3 sample data, computed by the dev mart's Postgres. Synthetic sales (12,000 customers, 96,000
-- lines over 24 months) are generated deterministically, so every run gives the same numbers.
-- Prints two JSON lines: the chart sample set, then the matrix rows with server-side subtotals.
\set ON_ERROR_STOP on
CREATE TEMP TABLE f AS
SELECT region, customer, category, to_char(date '2024-10-01' + make_interval(months => m), 'YYYY-MM') AS month,
       revenue, round(revenue * (base + (abs(hashint4(c * 17 + k)) % 20) / 100.0), 2) AS cost
FROM (
    SELECT c, k, m,
           (ARRAY['North', 'South', 'West'])[1 + c % 3] AS region,
           'Customer ' || lpad(c::text, 5, '0') AS customer,
           (ARRAY['Hardware', 'Services', 'Accessories'])[1 + abs(hashint4(c * 7 + k)) % 3] AS category,
           (ARRAY[0.62, 0.35, 0.48])[1 + abs(hashint4(c * 7 + k)) % 3] AS base,
           round(((40 + abs(hashint4(c * 31 + k)) % 960)
                  * (1 + m / 30.0)                          -- growth
                  * (1 + 0.15 * sin(m * pi() / 6))          -- seasonality
                  * (ARRAY[1.2, 0.9, 1.0])[1 + c % 3])::numeric, 2) AS revenue
    FROM generate_series(1, 12000) c, generate_series(1, 8) k,
         LATERAL (SELECT abs(hashint4(c * 13 + k)) % 24 AS m) mm
) g;

-- Chart sample set.
WITH months AS (SELECT DISTINCT month FROM f ORDER BY month),
series AS (
    SELECT dim, name, json_agg(round(coalesce(v, 0)) ORDER BY month) AS data FROM (
        SELECT 'region' AS dim, region AS name, months.month, (SELECT sum(revenue) FROM f WHERE f.region = r.region AND f.month = months.month) AS v
        FROM (SELECT DISTINCT region FROM f) r, months
        UNION ALL
        SELECT 'category', category, months.month, (SELECT sum(revenue) FROM f WHERE f.category = c.category AND f.month = months.month)
        FROM (SELECT DISTINCT category FROM f) c, months
    ) x GROUP BY dim, name
),
monthly AS (SELECT month, sum(revenue) AS revenue, sum(revenue - cost) / sum(revenue) AS margin FROM f GROUP BY month),
periods AS (
    SELECT month > '2025-09' AS current, sum(revenue) AS revenue, count(*) AS orders, sum(revenue - cost) / sum(revenue) AS margin
    FROM f GROUP BY 1
),
customers AS (
    SELECT customer, region, sum(revenue) AS revenue, sum(revenue - cost) / sum(revenue) AS margin
    FROM f GROUP BY customer, region ORDER BY revenue DESC LIMIT 400
)
SELECT json_build_object(
    'months', (SELECT json_agg(month) FROM months),
    'byRegion', (SELECT json_agg(json_build_object('name', name, 'data', data) ORDER BY name) FROM series WHERE dim = 'region'),
    'byCategory', (SELECT json_agg(json_build_object('name', name, 'data', data) ORDER BY name) FROM series WHERE dim = 'category'),
    'monthly', (SELECT json_agg(json_build_object('revenue', round(revenue), 'margin', round(margin, 4)) ORDER BY month) FROM monthly),
    'kpi', (SELECT json_object_agg(CASE WHEN current THEN 'current' ELSE 'prior' END,
                json_build_object('revenue', round(revenue), 'orders', orders, 'margin', round(margin, 4), 'aov', round(revenue / orders, 2)))
            FROM periods),
    'customers', (SELECT json_agg(json_build_array(round(revenue), round(margin, 4), customer, region)) FROM customers)
);

-- Matrix: region > customer rows, category columns, with subtotals and grand total from ROLLUP.
-- Margin % is recomputed from sums at every level, never averaged. lvl: 0 customer, 1 region, 3 total.
SELECT json_agg(json_build_array(lvl, region, customer, hw_r, hw_m, sv_r, sv_m, ac_r, ac_m, t_r, t_m)
                ORDER BY region IS NULL, region, customer IS NOT NULL, customer)
FROM (
    SELECT GROUPING(region, customer) AS lvl, region, customer,
           sum(revenue) FILTER (WHERE category = 'Hardware') AS hw_r,
           round(sum(revenue - cost) FILTER (WHERE category = 'Hardware') / nullif(sum(revenue) FILTER (WHERE category = 'Hardware'), 0), 4) AS hw_m,
           sum(revenue) FILTER (WHERE category = 'Services') AS sv_r,
           round(sum(revenue - cost) FILTER (WHERE category = 'Services') / nullif(sum(revenue) FILTER (WHERE category = 'Services'), 0), 4) AS sv_m,
           sum(revenue) FILTER (WHERE category = 'Accessories') AS ac_r,
           round(sum(revenue - cost) FILTER (WHERE category = 'Accessories') / nullif(sum(revenue) FILTER (WHERE category = 'Accessories'), 0), 4) AS ac_m,
           sum(revenue) AS t_r,
           round(sum(revenue - cost) / sum(revenue), 4) AS t_m
    FROM f GROUP BY ROLLUP (region, customer)
) x;
