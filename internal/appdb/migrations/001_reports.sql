-- Connections, saved SQL reports and their published revisions (Phase 2 round 1, R1-4).

-- A connection as Hareno first saw it. Its company and data access mode are fixed for its life
-- (ARCHITECTURE-PLAN §5.3): changing either means removing the connection and adding a new one.
CREATE TABLE connection (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    company    text NOT NULL,  -- Kiban company code
    mode       text NOT NULL CHECK (mode IN ('hareno', 'database-enforced', 'database-owned')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION connection_fixed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.mode <> OLD.mode OR NEW.company <> OLD.company THEN
        RAISE EXCEPTION 'connection %: company and data access mode are fixed; remove it and add a new connection', OLD.name;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER connection_fixed BEFORE UPDATE ON connection FOR EACH ROW EXECUTE FUNCTION connection_fixed();

-- A saved report. draft is what its editors work on; published points at the revision viewers see.
-- version increases on every draft save, so a save based on an older version is refused.
CREATE TABLE report (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id bigint NOT NULL REFERENCES connection,  -- removing a connection needs its reports removed first
    created_by    text NOT NULL,                          -- Kiban subject
    draft         jsonb NOT NULL,                         -- {"title", "sql", "chart"}
    version       int NOT NULL DEFAULT 1,
    published     int,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Published revisions never change (§6.2); rollback points published at an older one.
CREATE TABLE report_revision (
    report_id    uuid NOT NULL REFERENCES report ON DELETE CASCADE,
    revision     int NOT NULL,
    body         jsonb NOT NULL,  -- the draft as published
    published_by text NOT NULL,
    published_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (report_id, revision)
);

CREATE FUNCTION revision_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'published report revisions cannot be changed';
END $$;
CREATE TRIGGER revision_immutable BEFORE UPDATE ON report_revision FOR EACH ROW EXECUTE FUNCTION revision_immutable();

ALTER TABLE report ADD FOREIGN KEY (id, published) REFERENCES report_revision (report_id, revision) DEFERRABLE INITIALLY DEFERRED;
