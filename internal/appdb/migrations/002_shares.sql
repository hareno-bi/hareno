-- Who a report is shared with (R1-4b). Kiban holds the decisions; Hareno keeps what it granted,
-- because Kiban cannot list an object's grants, and the share list and revoking need it.
-- member is a Kiban member id: the grant is report:<id>#<relation> @ member:<member>#mapped_user,
-- which follows the member's linked login and ends with the membership.
CREATE TABLE report_share (
    report_id uuid NOT NULL REFERENCES report ON DELETE CASCADE,
    member    text NOT NULL,
    name      text NOT NULL,  -- the member's display name when shared, for the list
    relation  text NOT NULL CHECK (relation IN ('viewer', 'editor')),
    shared_by text NOT NULL,  -- Kiban subject
    shared_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (report_id, member)
);
