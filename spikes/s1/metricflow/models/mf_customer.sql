-- MetricFlow joins at most two hops from a measure: region_name is denormalised onto customers
-- so order-line measures (lines -> order -> customer) can group by it.
select c.*, r.region_name
from mart.dim_customer c
join mart.dim_region r using (region_id)
