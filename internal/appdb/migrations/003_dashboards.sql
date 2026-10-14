-- Reports become dashboards (R1-4c): a title and tiles, each its own query and chart.
-- {"title", "sql", "chart"} -> {"title", "tiles": [{"title", "sql", "chart", "show", "wide"}]}
CREATE FUNCTION pg_temp.tiles(d jsonb) RETURNS jsonb LANGUAGE sql AS $$
    SELECT jsonb_build_object('title', d->'title', 'tiles', jsonb_build_array(jsonb_build_object(
        'title', '', 'sql', d->'sql', 'chart', coalesce(d->'chart', 'null'), 'show', 'chart', 'wide', true)))
$$;
UPDATE report SET draft = pg_temp.tiles(draft) WHERE NOT draft ? 'tiles';
ALTER TABLE report_revision DISABLE TRIGGER revision_immutable;
UPDATE report_revision SET body = pg_temp.tiles(body) WHERE NOT body ? 'tiles';
ALTER TABLE report_revision ENABLE TRIGGER revision_immutable;

-- Whose rows a share shows. 'viewer': each person's own (the database filters for whoever looks).
-- 'sharer': the sharer's, for people who could not see them; only a connection's data_sharer may
-- give that, and only to viewers. scope is the sharer's row scope, refreshed whenever the sharer
-- opens the report; the share stops showing data when the sharer leaves the company.
ALTER TABLE report_share
    ADD COLUMN data  text NOT NULL DEFAULT 'viewer' CHECK (data IN ('viewer', 'sharer')),
    ADD COLUMN scope jsonb,
    ADD CHECK (data = 'viewer' OR (relation = 'viewer' AND scope IS NOT NULL));
