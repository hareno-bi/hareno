import type { TileDef } from './chart'

// Example tiles for testers, on the dev mart. Each runs under the signed-in user's own row scope,
// so alice, bob and erin see different rows from the same query.
export const examples: TileDef[] = [
  {
    title: 'Revenue by region', show: 'chart', wide: false,
    chart: { kind: 'bar', x: 'region', ys: ['revenue'], split: '' },
    sql: `SELECT r.region_name AS region, round(sum(li.quantity * li.unit_price * (1 - coalesce(li.discount, 0))), 2) AS revenue
FROM mart.fact_order o
JOIN mart.dim_customer c USING (customer_id)
JOIN mart.dim_region r USING (region_id)
JOIN mart.fact_order_line li USING (order_id)
WHERE o.status = 'complete'
GROUP BY 1
ORDER BY 2 DESC`,
  },
  {
    title: 'Monthly revenue trend', show: 'chart', wide: true,
    chart: { kind: 'line', x: 'month', ys: ['revenue'], split: '' },
    sql: `SELECT to_char(o.ordered_at, 'YYYY-MM') AS month, round(sum(li.quantity * li.unit_price * (1 - coalesce(li.discount, 0))), 2) AS revenue
FROM mart.fact_order o
JOIN mart.fact_order_line li USING (order_id)
WHERE o.status = 'complete'
GROUP BY 1
ORDER BY 1`,
  },
  {
    title: 'Revenue by month and region', show: 'chart', wide: true,
    chart: { kind: 'stacked', x: 'month', ys: ['revenue'], split: 'region' },
    sql: `SELECT to_char(o.ordered_at, 'YYYY-MM') AS month, r.region_name AS region, round(sum(li.quantity * li.unit_price * (1 - coalesce(li.discount, 0))), 2) AS revenue
FROM mart.fact_order o
JOIN mart.dim_customer c USING (customer_id)
JOIN mart.dim_region r USING (region_id)
JOIN mart.fact_order_line li USING (order_id)
WHERE o.status = 'complete'
GROUP BY 1, 2
ORDER BY 1, 2`,
  },
  {
    title: 'Revenue share by product category', show: 'chart', wide: false,
    chart: { kind: 'pie', x: 'category', ys: ['revenue'], split: '' },
    sql: `SELECT p.category, round(sum(li.quantity * li.unit_price * (1 - coalesce(li.discount, 0))), 2) AS revenue
FROM mart.fact_order o
JOIN mart.fact_order_line li USING (order_id)
JOIN mart.dim_product p USING (product_id)
WHERE o.status = 'complete'
GROUP BY 1
ORDER BY 2 DESC`,
  },
  {
    title: 'Top customers', show: 'chart', wide: false,
    chart: { kind: 'hbar', x: 'customer', ys: ['revenue'], split: '' },
    sql: `SELECT c.customer_name AS customer, round(sum(li.quantity * li.unit_price * (1 - coalesce(li.discount, 0))), 2) AS revenue
FROM mart.fact_order o
JOIN mart.dim_customer c USING (customer_id)
JOIN mart.fact_order_line li USING (order_id)
WHERE o.status = 'complete'
GROUP BY 1
ORDER BY 2 DESC
LIMIT 10`,
  },
  {
    title: 'Orders by status', show: 'chart', wide: false,
    chart: { kind: 'bar', x: 'status', ys: ['orders'], split: '' },
    sql: `SELECT o.status, count(*) AS orders
FROM mart.fact_order o
GROUP BY 1
ORDER BY 2 DESC`,
  },
  {
    title: 'Customers by segment and region', show: 'chart', wide: false,
    chart: { kind: 'stacked', x: 'segment', ys: ['customers'], split: 'region' },
    sql: `SELECT c.segment, r.region_name AS region, count(*) AS customers
FROM mart.dim_customer c
JOIN mart.dim_region r USING (region_id)
GROUP BY 1, 2
ORDER BY 1, 2`,
  },
  {
    title: 'Product margin', show: 'chart', wide: false,
    chart: { kind: 'scatter', x: 'revenue', ys: ['margin'], split: '' },
    sql: `SELECT p.product_name AS product,
       round(sum(li.quantity * li.unit_price * (1 - coalesce(li.discount, 0))), 2) AS revenue,
       round(sum(li.quantity * (li.unit_price * (1 - coalesce(li.discount, 0)) - p.unit_cost)), 2) AS margin
FROM mart.fact_order o
JOIN mart.fact_order_line li USING (order_id)
JOIN mart.dim_product p USING (product_id)
WHERE o.status = 'complete'
GROUP BY 1
ORDER BY 2 DESC`,
  },
  {
    title: 'Order list', show: 'table', wide: true,
    chart: { kind: 'bar', x: '', ys: [], split: '' },
    sql: `SELECT o.order_id, to_char(o.ordered_at, 'YYYY-MM-DD') AS ordered, c.customer_name AS customer, r.region_name AS region, o.status,
       round(sum(li.quantity * li.unit_price * (1 - coalesce(li.discount, 0))), 2) AS amount
FROM mart.fact_order o
JOIN mart.dim_customer c USING (customer_id)
JOIN mart.dim_region r USING (region_id)
JOIN mart.fact_order_line li USING (order_id)
GROUP BY 1, 2, 3, 4, 5
ORDER BY 1`,
  },
  {
    title: 'Data freshness', show: 'table', wide: false,
    chart: { kind: 'bar', x: '', ys: [], split: '' },
    sql: `SELECT table_name, loaded_at
FROM mart.etl_watermark
ORDER BY 1`,
  },
]
