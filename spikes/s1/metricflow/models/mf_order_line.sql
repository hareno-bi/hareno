-- MetricFlow needs a time dimension in every measure's own semantic model, and its measure
-- expressions cannot reach joined columns: order date and unit cost are denormalised onto lines.
select l.*, o.ordered_at, o.customer_id, p.unit_cost
from mart.fact_order_line l
join mart.fact_order o using (order_id)
join mart.dim_product p using (product_id)
