package sqlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rosschiu/kiban/sdk"

	"github.com/hareno-bi/hareno/internal/authz"
)

// Saved SQL reports (Phase 2 round 1, R1-4; ARCHITECTURE-PLAN §6.2, §9):
//
//	GET    /api/reports               reports the user may view or edit
//	POST   /api/reports               {"connection", "draft"}: create; the creator becomes its editor
//	GET    /api/reports/{id}          the draft (editors) and the published revision
//	PUT    /api/reports/{id}          {"draft", "version"}: save the draft; 409 if someone saved since
//	POST   /api/reports/{id}/publish  {"version"}: the draft becomes a new, immutable revision
//	POST   /api/reports/{id}/run      run the published revision as the viewer, under their own row scope
//	DELETE /api/reports/{id}
//
// Kiban holds who may do what: report#editor and report#viewer, and connection#sql_user.
// Changing a report's SQL needs editor and sql_user; viewing the published revision needs only
// viewer (or editor), not SQL rights. The database filters rows for whoever runs it.

// Granter writes relation tuples on Hareno's object types; *sdk.Client satisfies it.
type Granter interface {
	Grant(ctx context.Context, companyID string, tuples ...sdk.Tuple) error
	Revoke(ctx context.Context, companyID string, tuples ...sdk.Tuple) error
}

// Draft is a report's content.
type Draft struct {
	Title string          `json:"title"`
	SQL   string          `json:"sql"`
	Chart json.RawMessage `json:"chart"` // the editor's chart settings, kept as given
}

func (d Draft) valid() bool {
	return d.Title != "" && len(d.Title) <= 200 && d.SQL != "" && len(d.Chart) <= 16<<10 &&
		(len(d.Chart) == 0 || d.Chart[0] == '{')
}

type reportRow struct {
	ID, Connection, Company, CreatedBy string
	Draft                              Draft
	Version                            int
	Published                          *int
	PublishedTitle                     *string
	UpdatedAt                          time.Time
}

const reportCols = `r.id, c.name, c.company, r.created_by, r.draft, r.version, r.published,
	(SELECT body->>'title' FROM report_revision WHERE report_id = r.id AND revision = r.published), r.updated_at`

func scanReport(row pgx.Row) (reportRow, error) {
	var r reportRow
	err := row.Scan(&r.ID, &r.Connection, &r.Company, &r.CreatedBy, &r.Draft, &r.Version, &r.Published, &r.PublishedTitle, &r.UpdatedAt)
	return r, err
}

// caller verifies the bearer; on failure it has answered.
func (h *Handler) caller(w http.ResponseWriter, r *http.Request) (bearer, sub string, ok bool) {
	bearer = bearerOf(r)
	sub, err := h.Authz.User(r.Context(), bearer)
	if err != nil {
		fail(w, http.StatusUnauthorized, "sign in required")
		return "", "", false
	}
	return bearer, sub, true
}

// rights are what the user may do with one report.
type rights struct {
	company    string // Kiban company id
	view, edit bool
	sql        bool // sql_user on the report's connection: needed to change its SQL
}

func (h *Handler) rights(ctx context.Context, bearer, sub string, rep reportRow) (rights, error) {
	company, err := h.Authz.Company(ctx, bearer, sub, rep.Company)
	if errors.Is(err, authz.ErrNoCompany) {
		return rights{}, nil
	} else if err != nil {
		return rights{}, err
	}
	view := authz.Item{Type: "report", ID: rep.ID, Relation: "viewer"}
	edit := authz.Item{Type: "report", ID: rep.ID, Relation: "editor"}
	sql := authz.Item{Type: "connection", ID: rep.Connection, Relation: "sql_user"}
	ok, err := h.Authz.Allowed(ctx, bearer, sub, company, []authz.Item{view, edit, sql})
	if err != nil {
		return rights{}, err
	}
	return rights{company: company, view: ok[view] || ok[edit], edit: ok[edit], sql: ok[sql]}, nil
}

// load reads a report and the user's rights on it. A report the user may not see is "not found":
// its existence is not revealed.
func (h *Handler) load(w http.ResponseWriter, r *http.Request) (rep reportRow, rt rights, bearer, sub string, ok bool) {
	bearer, sub, ok = h.caller(w, r)
	if !ok {
		return
	}
	ok = false
	rep, err := scanReport(h.DB.QueryRow(r.Context(), `SELECT `+reportCols+` FROM report r JOIN connection c ON c.id = r.connection_id WHERE r.id::text = $1`, r.PathValue("id")))
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, http.StatusNotFound, "no such report")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "could not read the report")
		return
	}
	rt, err = h.rights(r.Context(), bearer, sub, rep)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "authorization unavailable")
		return
	}
	if !rt.view {
		fail(w, http.StatusNotFound, "no such report")
		return
	}
	return rep, rt, bearer, sub, true
}

func (h *Handler) ListReports(w http.ResponseWriter, r *http.Request) {
	bearer, sub, ok := h.caller(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	rows, err := h.DB.Query(ctx, `SELECT `+reportCols+` FROM report r JOIN connection c ON c.id = r.connection_id ORDER BY r.updated_at DESC`)
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not list reports")
		return
	}
	all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (reportRow, error) { return scanReport(row) })
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not list reports")
		return
	}
	// ponytail: reads every report and asks Kiban about each (cached, 100 per call); add a
	// company column and paging when a company has thousands.
	type item struct {
		ID         string    `json:"id"`
		Title      string    `json:"title"`
		Connection string    `json:"connection"`
		Published  *int      `json:"published"`
		Version    int       `json:"version"`
		UpdatedAt  time.Time `json:"updatedAt"`
		CanEdit    bool      `json:"canEdit"`
	}
	out := []item{}
	byCompany := map[string][]reportRow{}
	for _, rep := range all {
		byCompany[rep.Company] = append(byCompany[rep.Company], rep)
	}
	for code, reps := range byCompany {
		company, err := h.Authz.Company(ctx, bearer, sub, code)
		if errors.Is(err, authz.ErrNoCompany) {
			continue
		}
		var items []authz.Item
		for _, rep := range reps {
			items = append(items, authz.Item{Type: "report", ID: rep.ID, Relation: "viewer"}, authz.Item{Type: "report", ID: rep.ID, Relation: "editor"})
		}
		var ok map[authz.Item]bool
		if err == nil {
			ok, err = h.Authz.Allowed(ctx, bearer, sub, company, items)
		}
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "authorization unavailable")
			return
		}
		for _, rep := range reps {
			edit := ok[authz.Item{Type: "report", ID: rep.ID, Relation: "editor"}]
			view := ok[authz.Item{Type: "report", ID: rep.ID, Relation: "viewer"}]
			switch {
			case edit:
				out = append(out, item{rep.ID, rep.Draft.Title, rep.Connection, rep.Published, rep.Version, rep.UpdatedAt, true})
			case view && rep.Published != nil: // viewers see the published title; before the first publish, nothing
				out = append(out, item{rep.ID, *rep.PublishedTitle, rep.Connection, rep.Published, 0, rep.UpdatedAt, false})
			}
		}
	}
	reply(w, map[string]any{"reports": out})
}

func (h *Handler) CreateReport(w http.ResponseWriter, r *http.Request) {
	bearer, sub, ok := h.caller(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var req struct {
		Connection string `json:"connection"`
		Draft      Draft  `json:"draft"`
	}
	if !decode(w, r, &req) || !req.Draft.valid() {
		fail(w, http.StatusBadRequest, "expected {\"connection\": ..., \"draft\": {\"title\", \"sql\", \"chart\"}}")
		return
	}
	conn, ok := h.Connections[req.Connection]
	if !ok {
		fail(w, http.StatusNotFound, "no such connection")
		return
	}
	company, err := h.Authz.Company(ctx, bearer, sub, conn.Company)
	if errors.Is(err, authz.ErrNoCompany) {
		fail(w, http.StatusNotFound, "no such connection")
		return
	} else if err != nil {
		fail(w, http.StatusServiceUnavailable, "authorization unavailable")
		return
	}
	use := authz.Item{Type: "connection", ID: req.Connection, Relation: "sql_user"}
	allowed, err := h.Authz.Allowed(ctx, bearer, sub, company, []authz.Item{use})
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "authorization unavailable")
		return
	}
	if !allowed[use] {
		fail(w, http.StatusForbidden, "you may not query this connection with SQL")
		return
	}

	// The row and the creator's editor grant: if Kiban refuses the grant, the row goes too.
	var id string
	err = pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO report (connection_id, created_by, draft) VALUES ($1, $2, $3) RETURNING id`,
			conn.ID, sub, req.Draft).Scan(&id); err != nil {
			return err
		}
		return h.Grants.Grant(ctx, company, sdk.AnchorTuple("hareno", "report", id, company),
			sdk.Tuple{ObjectType: "report", ObjectID: id, Relation: "editor", SubjectType: "user", SubjectID: sub})
	})
	if err != nil {
		h.Log.Error("report create failed", "user", sub, "error", err.Error())
		fail(w, http.StatusBadGateway, "could not create the report")
		return
	}
	h.Log.Info("report created", "user", sub, "report", id, "connection", req.Connection)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"id": id, "version": 1})
}

func (h *Handler) GetReport(w http.ResponseWriter, r *http.Request) {
	rep, rt, _, _, ok := h.load(w, r)
	if !ok {
		return
	}
	out := map[string]any{"id": rep.ID, "connection": rep.Connection, "canEdit": rt.edit, "canEditSQL": rt.edit && rt.sql}
	if rt.edit {
		out["draft"], out["version"] = rep.Draft, rep.Version
	}
	if rep.Published != nil {
		var body Draft
		var at time.Time
		if err := h.DB.QueryRow(r.Context(), `SELECT body, published_at FROM report_revision WHERE report_id = $1 AND revision = $2`,
			rep.ID, *rep.Published).Scan(&body, &at); err != nil {
			fail(w, http.StatusInternalServerError, "could not read the report")
			return
		}
		out["published"] = map[string]any{"revision": *rep.Published, "publishedAt": at, "body": body}
	}
	reply(w, out)
}

// editable loads a report for a change to its content: editor and sql_user both.
func (h *Handler) editable(w http.ResponseWriter, r *http.Request) (reportRow, rights, string, string, bool) {
	rep, rt, bearer, sub, ok := h.load(w, r)
	if ok && !(rt.edit && rt.sql) {
		fail(w, http.StatusForbidden, "you may not change this report")
		ok = false
	}
	return rep, rt, bearer, sub, ok
}

func (h *Handler) SaveReport(w http.ResponseWriter, r *http.Request) {
	rep, _, _, sub, ok := h.editable(w, r)
	if !ok {
		return
	}
	var req struct {
		Draft   Draft `json:"draft"`
		Version int   `json:"version"`
	}
	if !decode(w, r, &req) || !req.Draft.valid() {
		fail(w, http.StatusBadRequest, "expected {\"draft\": {\"title\", \"sql\", \"chart\"}, \"version\": ...}")
		return
	}
	var version int
	err := h.DB.QueryRow(r.Context(), `UPDATE report SET draft = $1, version = version + 1, updated_at = now()
		WHERE id = $2 AND version = $3 RETURNING version`, req.Draft, rep.ID, req.Version).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, http.StatusConflict, "someone else saved this report since you opened it; reload to see their changes")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "could not save the report")
		return
	}
	h.Log.Info("report saved", "user", sub, "report", rep.ID, "version", version)
	reply(w, map[string]any{"version": version})
}

func (h *Handler) PublishReport(w http.ResponseWriter, r *http.Request) {
	rep, _, bearer, sub, ok := h.editable(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var req struct {
		Version int `json:"version"`
	}
	if !decode(w, r, &req) {
		fail(w, http.StatusBadRequest, "expected {\"version\": ...}")
		return
	}
	if req.Version != rep.Version {
		fail(w, http.StatusConflict, "someone else saved this report since you opened it; reload to see their changes")
		return
	}
	// Publish only SQL that runs: once, as the publisher, under their own scope.
	if status, msg := h.run(ctx, bearer, sub, rep, rep.Draft.SQL, nil); status != http.StatusOK {
		fail(w, status, "not published: "+msg)
		return
	}
	var revision int
	err := pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO report_revision (report_id, revision, body, published_by)
			SELECT id, coalesce((SELECT max(revision) FROM report_revision WHERE report_id = $1), 0) + 1, draft, $2
			FROM report WHERE id = $1 AND version = $3 FOR UPDATE RETURNING revision`, rep.ID, sub, req.Version).Scan(&revision)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE report SET published = $1, updated_at = now() WHERE id = $2`, revision, rep.ID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, http.StatusConflict, "someone else saved this report since you opened it; reload to see their changes")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "could not publish the report")
		return
	}
	h.Log.Info("report published", "user", sub, "report", rep.ID, "revision", revision)
	reply(w, map[string]any{"revision": revision})
}

func (h *Handler) RunReport(w http.ResponseWriter, r *http.Request) {
	rep, _, bearer, sub, ok := h.load(w, r)
	if !ok {
		return
	}
	if rep.Published == nil {
		fail(w, http.StatusNotFound, "this report has not been published")
		return
	}
	var body Draft
	if err := h.DB.QueryRow(r.Context(), `SELECT body FROM report_revision WHERE report_id = $1 AND revision = $2`, rep.ID, *rep.Published).Scan(&body); err != nil {
		fail(w, http.StatusInternalServerError, "could not read the report")
		return
	}
	var buf bytes.Buffer
	if status, msg := h.run(r.Context(), bearer, sub, rep, body.SQL, &buf); status != http.StatusOK {
		fail(w, status, msg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(buf.Bytes())
}

// run executes sql on the report's connection under the user's own row scope, without needing
// sql_user: the report's editors chose the SQL. On success the result is written to out (if set).
func (h *Handler) run(ctx context.Context, bearer, sub string, rep reportRow, sql string, out *bytes.Buffer) (int, string) {
	start := time.Now()
	conn, ok := h.Connections[rep.Connection]
	if !ok {
		return http.StatusNotFound, "the report's connection has been removed"
	}
	company, err := h.Authz.Company(ctx, bearer, sub, conn.Company)
	if err != nil {
		return http.StatusServiceUnavailable, "authorization unavailable"
	}
	scope, _, err := h.access(ctx, bearer, sub, company, rep.Connection, conn)
	if err != nil {
		return http.StatusServiceUnavailable, "authorization unavailable"
	}
	audit := h.Log.With("user", sub, "report", rep.ID, "connection", rep.Connection, "scope", scope)
	res, err := conn.Exec.Run(ctx, scope, sql)
	if err != nil {
		audit.Info("report run failed", "error", err.Error())
		return http.StatusBadRequest, err.Error()
	}
	audit.Info("report ran", "rows", len(res.Rows), "ms", time.Since(start).Milliseconds())
	if out != nil {
		if res.Rows == nil {
			res.Rows = [][]any{}
		}
		json.NewEncoder(out).Encode(response{Columns: res.Columns, Rows: res.Rows, Truncated: res.Truncated})
	}
	return http.StatusOK, ""
}

func (h *Handler) DeleteReport(w http.ResponseWriter, r *http.Request) {
	rep, rt, _, sub, ok := h.load(w, r)
	if !ok {
		return
	}
	if !rt.edit {
		fail(w, http.StatusForbidden, "you may not delete this report")
		return
	}
	ctx := r.Context()
	if _, err := h.DB.Exec(ctx, `DELETE FROM report WHERE id = $1`, rep.ID); err != nil {
		fail(w, http.StatusInternalServerError, "could not delete the report")
		return
	}
	// The report is gone whatever Kiban says; a tuple left behind names an object that no longer exists.
	if err := h.Grants.Revoke(ctx, rt.company, sdk.Tuple{ObjectType: "report", ObjectID: rep.ID, Relation: "editor", SubjectType: "user", SubjectID: rep.CreatedBy},
		sdk.AnchorTuple("hareno", "report", rep.ID, rt.company)); err != nil {
		h.Log.Warn("report grants not revoked", "report", rep.ID, "error", err.Error())
	}
	h.Log.Info("report deleted", "user", sub, "report", rep.ID)
	w.WriteHeader(http.StatusNoContent)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v) == nil
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
