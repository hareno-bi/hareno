-- Known answers for the development mart. Expected values were computed by hand from
-- init.sql; the reference SQL must agree. Hareno's compiled SQL is tested against the
-- same expected values (internal/query), and end to end through the API on view, preview and
-- export (internal/sqlapi/exit_test.go). Run: dev/mart/check.sh
--
-- Measures used throughout:
--   revenue = quantity * unit_price * (1 - coalesce(discount, 0))
--   cost    = quantity * unit_cost
--   "complete" = status = 'complete' (cancelled orders excluded unless stated)
\set ON_ERROR_STOP on
\o /dev/null
SET search_path = mart;
SET client_min_messages = notice;

CREATE FUNCTION pg_temp.ka(name text, actual text, expected text) RETURNS void AS $$
BEGIN
    IF actual IS DISTINCT FROM expected THEN
        RAISE EXCEPTION E'% FAILED\n  expected: %\n  actual:   %', name, expected, actual;
    END IF;
    RAISE NOTICE 'ok  %', name;
END $$ LANGUAGE plpgsql;

-- Line grain with dimensions attached. Fine for line-grain measures only.
CREATE TEMP VIEW line AS
SELECT l.order_id, o.ordered_at, o.status, c.customer_id, c.customer_name, c.segment,
       r.region_name, p.category,
       l.quantity * l.unit_price * (1 - coalesce(l.discount, 0)) AS revenue,
       l.quantity * l.unit_price * coalesce(l.discount, 0)       AS discount_amount,
       l.quantity * p.unit_cost                                  AS cost,
       o.shipping_cost                                           -- fans out: one copy per line
FROM fact_order_line l
JOIN fact_order o   USING (order_id)
JOIN dim_customer c USING (customer_id)
JOIN dim_region r   USING (region_id)
JOIN dim_product p  USING (product_id);

CREATE TEMP VIEW done AS SELECT * FROM line WHERE status = 'complete';

-- Q01 Basic totals.
SELECT pg_temp.ka('Q01 total revenue (complete)',
    (SELECT round(sum(revenue), 2)::text FROM done), '587.75');
SELECT pg_temp.ka('Q01 orders / customers / lines (complete)',
    (SELECT format('%s/%s/%s', count(DISTINCT order_id), count(DISTINCT customer_id), count(*)) FROM done),
    '7/4/13');

-- Q02 Status filter.
SELECT pg_temp.ka('Q02 revenue all statuses',
    (SELECT round(sum(revenue), 2)::text FROM line), '607.75');
SELECT pg_temp.ka('Q02 revenue cancelled',
    (SELECT round(sum(revenue), 2)::text FROM line WHERE status = 'cancelled'), '20.00');

-- Q03 Revenue by region; groups sum to the total.
SELECT pg_temp.ka('Q03 revenue by region',
    (SELECT string_agg(format('%s=%s', region_name, rev), ', ' ORDER BY region_name)
     FROM (SELECT region_name, round(sum(revenue), 2) rev FROM done GROUP BY 1) t),
    'North=238.50, South=175.50, West=173.75');

-- Q04 FAN-OUT TRAP. Shipping is order grain. Summing it through order lines gives 87.00.
SELECT pg_temp.ka('Q04 total shipping (correct, order grain)',
    (SELECT sum(shipping_cost)::text FROM fact_order WHERE status = 'complete'), '42.75');
SELECT pg_temp.ka('Q04 total shipping (WRONG, summed across lines)',
    (SELECT sum(shipping_cost)::text FROM done), '87.00');
-- Shipping by product category has no correct answer without an allocation rule:
-- the model must reject it, not return a number. (Not executable here.)

-- Q05 Shipping by region: dimension on the "one" side of the join, so no fan-out.
SELECT pg_temp.ka('Q05 shipping by region',
    (SELECT string_agg(format('%s=%s', region_name, s), ', ' ORDER BY region_name)
     FROM (SELECT r.region_name, sum(o.shipping_cost) s
           FROM fact_order o JOIN dim_customer USING (customer_id) JOIN dim_region r USING (region_id)
           WHERE o.status = 'complete' GROUP BY 1) t),
    'North=15.50, South=19.25, West=8.00');

-- Q06 Nulls in a measure: AVG ignores NULL; COUNT(column) differs from COUNT(*).
SELECT pg_temp.ka('Q06 avg shipping ignores null',
    (SELECT round(avg(shipping_cost), 4)::text FROM fact_order WHERE status = 'complete'), '7.1250');
SELECT pg_temp.ka('Q06 orders / orders with shipping',
    (SELECT format('%s/%s', count(*), count(shipping_cost)) FROM fact_order WHERE status = 'complete'),
    '7/6');

-- Q07 Null dimension value forms its own group; it is not dropped.
SELECT pg_temp.ka('Q07 revenue by segment',
    (SELECT string_agg(format('%s=%s', coalesce(segment, '(null)'), rev), ', ' ORDER BY segment NULLS LAST)
     FROM (SELECT segment, round(sum(revenue), 2) rev FROM done GROUP BY 1) t),
    'Enterprise=186.00, SMB=226.25, (null)=175.50');

-- Q08 Revenue by category.
SELECT pg_temp.ka('Q08 revenue by category',
    (SELECT string_agg(format('%s=%s', category, rev), ', ' ORDER BY category)
     FROM (SELECT category, round(sum(revenue), 2) rev FROM done GROUP BY 1) t),
    'Accessories=16.25, Hardware=271.50, Services=300.00');

-- Q09 Distinct count is not additive: per-category counts sum to 9, the total is 4.
SELECT pg_temp.ka('Q09 distinct customers by category',
    (SELECT string_agg(format('%s=%s', category, n), ', ' ORDER BY category)
     FROM (SELECT category, count(DISTINCT customer_id) n FROM done GROUP BY 1) t),
    'Accessories=2, Hardware=4, Services=3');
SELECT pg_temp.ka('Q09 distinct customers total',
    (SELECT count(DISTINCT customer_id)::text FROM done), '4');

-- Q10 Ratio totals recompute the ratio. Averaging visible percentages gives 0.6481 (wrong).
SELECT pg_temp.ka('Q10 margin % by category',
    (SELECT string_agg(format('%s=%s', category, m), ', ' ORDER BY category)
     FROM (SELECT category, round((sum(revenue) - sum(cost)) / sum(revenue), 4) m FROM done GROUP BY 1) t),
    'Accessories=0.6000, Hardware=0.3444, Services=1.0000');
SELECT pg_temp.ka('Q10 margin % total',
    (SELECT round((sum(revenue) - sum(cost)) / sum(revenue), 4)::text FROM done), '0.6861');
SELECT pg_temp.ka('Q10 profit total',
    (SELECT round(sum(revenue) - sum(cost), 2)::text FROM done), '403.25');

-- Q11 Discounts: NULL discount means none.
SELECT pg_temp.ka('Q11 gross / discount / net revenue',
    (SELECT format('%s/%s/%s', round(sum(revenue + discount_amount), 2),
                   round(sum(discount_amount), 2), round(sum(revenue), 2)) FROM done),
    '616.25/28.50/587.75');

-- Q12 TIME ZONE TRAP. Order 105 is March in UTC but April in Hong Kong.
SELECT pg_temp.ka('Q12 revenue by month (HKT, correct)',
    (SELECT string_agg(format('%s=%s', m, rev), ', ' ORDER BY m)
     FROM (SELECT to_char(ordered_at AT TIME ZONE 'Asia/Hong_Kong', 'YYYY-MM') m, round(sum(revenue), 2) rev
           FROM done GROUP BY 1) t),
    '2026-01=86.00, 2026-02=152.50, 2026-03=85.50, 2026-04=93.75, 2026-05=170.00');
SELECT pg_temp.ka('Q12 revenue by month (UTC, WRONG)',
    (SELECT string_agg(format('%s=%s', m, rev), ', ' ORDER BY m)
     FROM (SELECT to_char(ordered_at AT TIME ZONE 'UTC', 'YYYY-MM') m, round(sum(revenue), 2) rev
           FROM done GROUP BY 1) t),
    '2026-01=86.00, 2026-02=152.50, 2026-03=175.50, 2026-04=3.75, 2026-05=170.00');

-- Q13 Fiscal calendar via dim_date on the HKT business date.
SELECT pg_temp.ka('Q13 revenue by fiscal year/quarter',
    (SELECT string_agg(format('FY%sQ%s=%s', fiscal_year, fiscal_quarter, rev), ', ' ORDER BY fiscal_year, fiscal_quarter)
     FROM (SELECT d.fiscal_year, d.fiscal_quarter, round(sum(revenue), 2) rev
           FROM done JOIN dim_date d ON d.date_key = (ordered_at AT TIME ZONE 'Asia/Hong_Kong')::date
           GROUP BY 1, 2) t),
    'FY2026Q4=324.00, FY2027Q1=263.75');
SELECT pg_temp.ka('Q13 fiscal calendar boundaries',
    (SELECT string_agg(format('%s:FY%sQ%s', date_key, fiscal_year, fiscal_quarter), ', ' ORDER BY date_key)
     FROM dim_date WHERE date_key IN ('2025-03-31', '2025-04-01', '2025-12-31', '2026-01-01', '2026-03-31', '2026-04-01')),
    '2025-03-31:FY2025Q4, 2025-04-01:FY2026Q1, 2025-12-31:FY2026Q3, 2026-01-01:FY2026Q4, 2026-03-31:FY2026Q4, 2026-04-01:FY2027Q1');

-- Q14 Period comparison and running total by HKT month. January has no prior month: NULL, not 0.
CREATE TEMP VIEW month_hkt AS
SELECT m, rev, rev - lag(rev) OVER (ORDER BY m) AS change, sum(rev) OVER (ORDER BY m) AS running
FROM (SELECT to_char(ordered_at AT TIME ZONE 'Asia/Hong_Kong', 'YYYY-MM') m, round(sum(revenue), 2) rev
      FROM done GROUP BY 1) t;
SELECT pg_temp.ka('Q14 month-over-month change',
    (SELECT string_agg(format('%s=%s', m, coalesce(change::text, 'null')), ', ' ORDER BY m) FROM month_hkt),
    '2026-01=null, 2026-02=66.50, 2026-03=-67.00, 2026-04=8.25, 2026-05=76.25');
SELECT pg_temp.ka('Q14 running total',
    (SELECT string_agg(format('%s=%s', m, running), ', ' ORDER BY m) FROM month_hkt),
    '2026-01=86.00, 2026-02=238.50, 2026-03=324.00, 2026-04=417.75, 2026-05=587.75');

-- Q15 Matrix: region x category with subtotals and grand total (ROLLUP). Empty cells absent.
SELECT pg_temp.ka('Q15 region x category rollup',
    (SELECT string_agg(format('%s|%s=%s', coalesce(region_name, 'ALL'), coalesce(category, 'ALL'), rev), ', '
                       ORDER BY region_name NULLS LAST, category NULLS LAST)
     FROM (SELECT region_name, category, round(sum(revenue), 2) rev FROM done GROUP BY ROLLUP (1, 2)) t),
    'North|Accessories=12.50, North|Hardware=126.00, North|Services=100.00, North|ALL=238.50, '
    'South|Hardware=95.50, South|Services=80.00, South|ALL=175.50, '
    'West|Accessories=3.75, West|Hardware=50.00, West|Services=120.00, West|ALL=173.75, '
    'ALL|ALL=587.75');
SELECT pg_temp.ka('Q15 category column totals',
    (SELECT string_agg(format('%s=%s', category, rev), ', ' ORDER BY category)
     FROM (SELECT category, round(sum(revenue), 2) rev FROM done GROUP BY 1) t),
    'Accessories=16.25, Hardware=271.50, Services=300.00');

-- Q16 Ratio of order-grain counts: total AOV is 587.75 / 7, not the average of customer AOVs.
-- A customer with no orders yields NULL, never a division error.
SELECT pg_temp.ka('Q16 average order value by customer',
    (SELECT string_agg(format('%s=%s', customer_name, coalesce(round(aov, 4)::text, 'null')), ', ' ORDER BY customer_name)
     FROM (SELECT c.customer_name, sum(d.revenue) / nullif(count(DISTINCT d.order_id), 0) aov
           FROM dim_customer c LEFT JOIN done d USING (customer_id) GROUP BY 1) t),
    'Acme=93.0000, Birch=52.5000, Cobalt=87.7500, Delta=86.8750, Ember=null');
SELECT pg_temp.ka('Q16 average order value total',
    (SELECT round(sum(revenue) / count(DISTINCT order_id), 4)::text FROM done), '83.9643');

-- Q17 Row policy: a viewer restricted to North sees North-only totals everywhere.
SELECT pg_temp.ka('Q17 North-only totals',
    (SELECT format('%s/%s', round(sum(revenue), 2), count(DISTINCT customer_id)) FROM done WHERE region_name = 'North'),
    '238.50/2');
SELECT pg_temp.ka('Q17 North-only by category',
    (SELECT string_agg(format('%s=%s', category, rev), ', ' ORDER BY category)
     FROM (SELECT category, round(sum(revenue), 2) rev FROM done WHERE region_name = 'North' GROUP BY 1) t),
    'Accessories=12.50, Hardware=126.00, Services=100.00');

-- Q18 Freshness watermark.
SELECT pg_temp.ka('Q18 watermark',
    (SELECT (max(loaded_at) AT TIME ZONE 'UTC')::text FROM etl_watermark), '2026-10-12 06:00:00');

-- Q19 Read credential is read-only.
SELECT pg_temp.ka('Q19 reader can select, cannot write',
    (SELECT format('%s/%s/%s', has_table_privilege('hareno_reader', 'mart.fact_order', 'SELECT'),
                   has_table_privilege('hareno_reader', 'mart.fact_order', 'INSERT'),
                   has_table_privilege('hareno_reader', 'mart.fact_order', 'UPDATE'))),
    't/f/f');

\echo 'all known answers ok'
