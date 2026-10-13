-- Row-level security for the SQL editor (ARCHITECTURE-PLAN §5.3, "database-enforced" mode), as
-- a client DBA would apply it to their mart. Runs after init.sql. Dev-only passwords.
--
-- Per query, Hareno connects as hareno_gate, sets the viewer's scope with
-- set_config('hareno.region', '<JSON array of region names, or ["*"]>', true), switches to
-- hareno_sql with SET LOCAL ROLE, and runs the user's statement in a read-only transaction.
-- hareno_sql cannot call set_config, so a statement cannot change its own scope or role.
-- Without a scope, policies return no rows.
SET search_path = mart;

CREATE ROLE hareno_gate LOGIN PASSWORD 'hareno_gate';
CREATE ROLE hareno_sql NOLOGIN;
GRANT hareno_sql TO hareno_gate;
GRANT USAGE ON SCHEMA mart TO hareno_sql;
GRANT SELECT ON ALL TABLES IN SCHEMA mart TO hareno_sql;

-- Only the gate may set configuration. set_config is callable by PUBLIC by default; the SET
-- statement itself cannot be smuggled into a single SELECT.
REVOKE EXECUTE ON FUNCTION pg_catalog.set_config(text, text, boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION pg_catalog.set_config(text, text, boolean) TO hareno_gate;

-- The viewer's regions; NULL (no scope set) matches nothing.
CREATE FUNCTION mart.hareno_region_allowed(region text) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM jsonb_array_elements_text(nullif(current_setting('hareno.region', true), '')::jsonb) v
        WHERE v = '*' OR v = region)
$$;
GRANT EXECUTE ON FUNCTION mart.hareno_region_allowed(text) TO hareno_sql;

ALTER TABLE dim_region ENABLE ROW LEVEL SECURITY;
ALTER TABLE dim_customer ENABLE ROW LEVEL SECURITY;
ALTER TABLE fact_order ENABLE ROW LEVEL SECURITY;
ALTER TABLE fact_order_line ENABLE ROW LEVEL SECURITY;

-- hareno_sql: rows in the viewer's regions only.
CREATE POLICY sql_scope ON dim_region FOR SELECT TO hareno_sql USING (hareno_region_allowed(region_name));
CREATE POLICY sql_scope ON dim_customer FOR SELECT TO hareno_sql
    USING (hareno_region_allowed((SELECT r.region_name FROM dim_region r WHERE r.region_id = dim_customer.region_id)));
CREATE POLICY sql_scope ON fact_order FOR SELECT TO hareno_sql
    USING (customer_id IN (SELECT customer_id FROM dim_customer));       -- dim_customer is itself filtered
CREATE POLICY sql_scope ON fact_order_line FOR SELECT TO hareno_sql
    USING (order_id IN (SELECT order_id FROM fact_order));               -- so is fact_order

-- hareno_reader (governed path, "Hareno" mode): RLS denies by default, so it gets an explicit
-- allow; Hareno applies its row policy in generated queries instead.
CREATE POLICY reader_all ON dim_region FOR SELECT TO hareno_reader USING (true);
CREATE POLICY reader_all ON dim_customer FOR SELECT TO hareno_reader USING (true);
CREATE POLICY reader_all ON fact_order FOR SELECT TO hareno_reader USING (true);
CREATE POLICY reader_all ON fact_order_line FOR SELECT TO hareno_reader USING (true);
