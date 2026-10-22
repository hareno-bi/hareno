package sqlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rosschiu/kiban/sdk"

	"github.com/hareno-bi/hareno/internal/authz"
	"github.com/hareno-bi/hareno/internal/sqlexec"
)

// Saved SQL reports (Phase 2 round 1, R1-4; ARCHITECTURE-PLAN §6.2, §9). A report is a dashboard:
// a title and tiles, each its own query and chart, all on one connection.
//
//	GET    /api/reports               reports the user may view or edit
//	POST   /api/reports               {"connection", "draft"}: create; the creator becomes its editor
//	GET    /api/reports/{id}          the draft (editors) and the published revision
//	PUT    /api/reports/{id}          {"draft", "version"}: save the draft; 409 if someone saved since
//	POST   /api/reports/{id}/publish  {"version"}: the draft becomes a new, immutable revision
//	POST   /api/reports/{id}/run      run the published revision's tiles for the viewer (see scope)
//	DELETE /api/reports/{id}
//
// Kiban holds who may do what: report#editor and report#viewer, and connection#sql_user and
// dataset_user. Changing a report needs editor, and sql_user to change its SQL; with dataset_user
// instead, an editor may change everything but SQL, and add dataset tiles (datasets.go). Viewing
// the published revision needs only viewer (or editor). The database filters rows for whoever runs it.

// Granter writes relation tuples on Hareno's object types; *sdk.Client satisfies it.
type Granter interface {
	Grant(ctx context.Context, companyID string, tuples ...sdk.Tuple) error
	Revoke(ctx context.Context, companyID string, tuples ...sdk.Tuple) error
}

// Draft is a report's content.
type Draft struct {
	Title   string   `json:"title"`
	Tiles   []Tile   `json:"tiles"`
	Filters []string `json:"filters,omitempty"` // columns the filter bar offers (filters.go)
	// Pages are the dashboard's page names, in order (D5); none is one page. Tiles stay one list,
	// each naming its page, so tile numbers (CSV links, drill-downs) do not depend on pages.
	Pages []string `json:"pages,omitempty"`
}

// Tile is one query on a dashboard and how it is shown.
type Tile struct {
	Title  string          `json:"title"`
	SQL    string          `json:"sql"`
	Chart  json.RawMessage `json:"chart"`            // the editor's chart settings, kept as given
	Show   string          `json:"show"`             // "chart", "table" or "matrix"
	Wide   bool            `json:"wide"`             // the dashboard's full width, or half
	Page   int             `json:"page,omitempty"`   // which of the pages it is on, from 0
	Matrix *Matrix         `json:"matrix,omitempty"` // with show "matrix" only (matrix.go)
	// Dataset, instead of SQL, builds the tile from a governed dataset (datasets.go).
	Dataset *DatasetQuery `json:"dataset,omitempty"`
}

const (
	maxTiles = 24
	maxPages = 10
)

func (d Draft) valid() bool {
	if d.Title == "" || len(d.Title) > 200 || len(d.Tiles) == 0 || len(d.Tiles) > maxTiles || len(d.Filters) > maxFilters {
		return false
	}
	for i, c := range d.Filters {
		if !validColumn(c) || slices.Contains(d.Filters[:i], c) {
			return false
		}
	}
	if len(d.Pages) == 1 || len(d.Pages) > maxPages {
		return false
	}
	for i, p := range d.Pages {
		if strings.TrimSpace(p) == "" || len(p) > 100 || slices.Contains(d.Pages[:i], p) {
			return false
		}
	}
	for _, t := range d.Tiles {
		if t.Page < 0 || t.Page >= max(len(d.Pages), 1) || (t.SQL == "") == (t.Dataset == nil) || (t.Dataset != nil && !t.Dataset.valid()) || len(t.Title) > 200 || len(t.Chart) > 16<<10 || !slices.Contains([]string{"chart", "table", "matrix"}, t.Show) ||
			(len(t.Chart) > 0 && t.Chart[0] != '{' && string(t.Chart) != "null") ||
			(t.Show == "matrix") != (t.Matrix != nil) || (t.Matrix != nil && !t.Matrix.valid(t.Dataset != nil)) {
			return false
		}
	}
	return true
}

// sqls are the draft's SQL tiles' queries.
func (d Draft) sqls() []string {
	var out []string
	for _, t := range d.Tiles {
		if t.SQL != "" {
			out = append(out, t.SQL)
		}
	}
	return out
}

// name is how errors refer to tile i.
func (d Draft) name(i int) string {
	if d.Tiles[i].Title != "" {
		return fmt.Sprintf("tile %d (%s)", i+1, d.Tiles[i].Title)
	}
	return fmt.Sprintf("tile %d", i+1)
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
	design     bool // dataset_user or sql_user: may change the report, but its SQL only with sql
	export     bool // exporter on the report's connection: may download its rows (export.go)
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
	export := authz.Item{Type: "connection", ID: rep.Connection, Relation: "exporter"}
	design := authz.Item{Type: "connection", ID: rep.Connection, Relation: "dataset_user"}
	ok, err := h.Authz.Allowed(ctx, bearer, sub, company, []authz.Item{view, edit, sql, export, design})
	if err != nil {
		return rights{}, err
	}
	return rights{company: company, view: ok[view] || ok[edit], edit: ok[edit], sql: ok[sql], export: ok[export], design: ok[design] || ok[sql]}, nil
}

func (h *Handler) find(ctx context.Context, id string) (reportRow, error) {
	return scanReport(h.DB.QueryRow(ctx, `SELECT `+reportCols+` FROM report r JOIN connection c ON c.id = r.connection_id WHERE r.id::text = $1`, id))
}

// load reads a report and the user's rights on it. A report the user may not see is "not found":
// its existence is not revealed.
func (h *Handler) load(w http.ResponseWriter, r *http.Request) (rep reportRow, rt rights, bearer, sub string, ok bool) {
	bearer, sub, ok = h.caller(w, r)
	if !ok {
		return
	}
	ok = false
	rep, err := h.find(r.Context(), r.PathValue("id"))
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
		fail(w, http.StatusBadRequest, "expected {\"connection\": ..., \"draft\": {\"title\", \"tiles\": [{\"title\", \"sql\", \"chart\", \"show\", \"wide\"}]}}")
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
	design := authz.Item{Type: "connection", ID: req.Connection, Relation: "dataset_user"}
	allowed, err := h.Authz.Allowed(ctx, bearer, sub, company, []authz.Item{use, design})
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "authorization unavailable")
		return
	}
	if !allowed[use] && !(allowed[design] && len(req.Draft.sqls()) == 0) {
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
	rep, rt, bearer, sub, ok := h.load(w, r)
	if !ok {
		return
	}
	out := map[string]any{"id": rep.ID, "connection": rep.Connection, "canEdit": rt.edit && rt.design, "canEditSQL": rt.edit && rt.sql, "canExport": rt.export}
	if rt.edit {
		out["draft"], out["version"] = rep.Draft, rep.Version
		canShare, err := h.refresh(r.Context(), bearer, sub, rep, rt)
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "authorization unavailable")
			return
		}
		out["canShareData"] = canShare
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

// editable loads a report for a change to its content: editor, and sql_user or dataset_user.
func (h *Handler) editable(w http.ResponseWriter, r *http.Request) (reportRow, rights, string, string, bool) {
	rep, rt, bearer, sub, ok := h.load(w, r)
	if ok && !(rt.edit && rt.design) {
		fail(w, http.StatusForbidden, "you may not change this report")
		ok = false
	}
	return rep, rt, bearer, sub, ok
}

func (h *Handler) SaveReport(w http.ResponseWriter, r *http.Request) {
	rep, rt, _, sub, ok := h.editable(w, r)
	if !ok {
		return
	}
	var req struct {
		Draft   Draft `json:"draft"`
		Version int   `json:"version"`
	}
	if !decode(w, r, &req) || !req.Draft.valid() {
		fail(w, http.StatusBadRequest, "expected {\"draft\": {\"title\", \"tiles\": [{\"title\", \"sql\", \"chart\", \"show\", \"wide\"}]}, \"version\": ...}")
		return
	}
	// Without sql_user, SQL tiles may be moved, restyled or removed, but no SQL may be added or changed.
	if old := rep.Draft.sqls(); !rt.sql && slices.ContainsFunc(req.Draft.sqls(), func(s string) bool { return !slices.Contains(old, s) }) {
		fail(w, http.StatusForbidden, "you may not change this report's SQL")
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
	// Publish only SQL that runs: each tile once, as the publisher, under their own scope.
	scope, err := h.ownScope(ctx, bearer, sub, rep)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "authorization unavailable")
		return
	}
	for i, t := range rep.Draft.Tiles {
		sql, args, _, _, err := h.tileQuery(ctx, rep.Connection, t, nil, nil, nil, t.Matrix)
		if err == nil {
			_, err = h.exec(ctx, sub, rep, scope, sql, args...)
		}
		if err != nil {
			fail(w, http.StatusBadRequest, "not published: "+rep.Draft.name(i)+": "+err.Error())
			return
		}
	}
	var revision int
	err = pgx.BeginFunc(ctx, h.DB, func(tx pgx.Tx) error {
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

// RunReport runs every tile of the published revision, narrowed by the viewer's filters
// ({"filters": {"<column>": ["<value>", ...]}}, see filters.go) and date range ({"dates": {"last":
// "3m"}} or {"from", "to"}, dates.go); a tile that fails carries its error. "dates" in the reply
// says whether any tile has a date a range would narrow.
func (h *Handler) RunReport(w http.ResponseWriter, r *http.Request) {
	rep, rt, bearer, sub, ok := h.load(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var req struct {
		Filters Filters          `json:"filters"`
		Dates   *Dates           `json:"dates"`
		Drill   map[int][]string `json:"drill"` // by tile: the values clicked, top down; they narrow the whole dashboard (drill.go)
		Page    int              `json:"page"`  // the page shown: only its tiles run
	}
	if !decode(w, r, &req) || !req.Filters.valid() || !req.Dates.valid() || !validDrill(req.Drill) {
		fail(w, http.StatusBadRequest, `expected {"filters": {"<column>": ["<value>", ...]}, "dates": {"last": "7d|30d|3m|12m|year|lastyear"} or {"from": "YYYY-MM-DD", "to": "YYYY-MM-DD"}, "drill": {"<tile>": ["<value>", ...]}}`)
		return
	}
	if rep.Published == nil {
		fail(w, http.StatusNotFound, "this report has not been published")
		return
	}
	var body Draft
	if err := h.DB.QueryRow(ctx, `SELECT body FROM report_revision WHERE report_id = $1 AND revision = $2`, rep.ID, *rep.Published).Scan(&body); err != nil {
		fail(w, http.StatusInternalServerError, "could not read the report")
		return
	}
	scope, data, err := h.scope(ctx, bearer, sub, rep, rt)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "authorization unavailable")
		return
	}
	conn, ok := h.Connections[rep.Connection]
	if !ok {
		fail(w, http.StatusNotFound, "the report's connection has been removed")
		return
	}
	mark := ""
	if conn.Watermark != "" {
		mark = h.results.mark(rep.Connection, conn.Refresh, func() (string, error) {
			res, err := conn.Exec.Run(context.WithoutCancel(ctx), sqlexec.Scope{}, conn.Watermark)
			if err != nil || len(res.Rows) == 0 || len(res.Rows[0]) == 0 || res.Rows[0][0] == nil {
				return "", err
			}
			if t, ok := res.Rows[0][0].(time.Time); ok {
				return t.UTC().Format(time.RFC3339Nano), nil
			}
			return fmt.Sprint(res.Rows[0][0]), nil
		})
	}
	// ponytail: tiles run one after another; run them concurrently when dashboards get slow.
	get := func(sql string, args ...any) tileResult {
		key := sql
		if len(args) > 0 {
			a, _ := json.Marshal(args)
			key += "\x00" + string(a)
		}
		return h.results.get(resultKey(rep.Connection, scope, key), mark, conn.Refresh, func() (*response, error) {
			return h.exec(context.WithoutCancel(ctx), sub, rep, scope, sql, args...) // shared: one viewer leaving does not cancel it for the rest
		})
	}
	// Each tile unfiltered first: its columns say which filters apply, and its values are the
	// filter bar's choices. Then as shown: filtered, and a matrix totalled. All runs are cached.
	all := make([]tileResult, len(body.Tiles))
	tiles := make([]tileResult, len(body.Tiles))
	datable := false
	for i, t := range body.Tiles {
		if t.Dataset != nil && h.Datasets != nil && h.Datasets.ByName[t.Dataset.Dataset] != nil && h.Datasets.ByName[t.Dataset.Dataset].Date != "" {
			datable = true // on any page: the date range is the dashboard's
		}
		if t.Page != req.Page {
			continue
		}
		base, _, _, _, err := h.tileQuery(ctx, rep.Connection, t, nil, nil, nil, nil)
		if err == nil {
			all[i] = get(base)
		}
		var drill *Drill
		if err == nil && all[i].response != nil {
			var sql string
			var args []any
			var filtered []string
			var dated bool
			var by []int
			if t, drill, by, err = h.drilled(body.Tiles, i, req.Drill); err == nil {
				sql, args, filtered, dated, err = h.tileQuery(ctx, rep.Connection, t, all[i].Columns, req.Filters, req.Dates, t.Matrix)
			}
			if err == nil {
				tiles[i] = get(sql, args...)
				tiles[i].Filtered, tiles[i].Dated, tiles[i].Drill, tiles[i].DrilledBy = filtered, dated, drill, by
			}
		} else if err == nil {
			tiles[i] = all[i]
		}
		if err != nil {
			tiles[i] = tileResult{Error: err.Error()}
		}
		tiles[i].Display = h.display(t.Dataset)
		if drill != nil {
			maps.Copy(tiles[i].Display, shown(h.Datasets.ByName[t.Dataset.Dataset], drill.Levels))
		}
	}
	cols := slices.Clone(body.Filters)
	for c := range req.Filters {
		if !slices.Contains(cols, c) {
			cols = append(cols, c)
		}
	}
	h.Log.Info("report viewed", "user", sub, "report", rep.ID, "revision", *rep.Published, "data", data, "scope", scope, "filters", req.Filters, "dates", req.Dates, "drill", req.Drill, "page", req.Page)
	shown := make([]*tileResult, len(tiles)) // null for the other pages' tiles
	for i := range tiles {
		if body.Tiles[i].Page == req.Page {
			shown[i] = &tiles[i]
		}
	}
	reply(w, map[string]any{"tiles": shown, "data": data, "options": options(cols, all), "dates": datable})
}

// scope is the row scope the report runs under for the user, and whose it is: their own, or for
// a viewer it was shared with showing the sharer's data, the sharer's (see shares.go).
func (h *Handler) scope(ctx context.Context, bearer, sub string, rep reportRow, rt rights) (sqlexec.Scope, string, error) {
	if rt.edit {
		_, err := h.refresh(ctx, bearer, sub, rep, rt)
		if err != nil {
			return nil, "", err
		}
	} else if scope, ok, err := h.sharedScope(ctx, sub, rep, rt); err != nil || ok {
		return scope, "sharer", err
	}
	scope, err := h.ownScope(ctx, bearer, sub, rep)
	return scope, "viewer", err
}

// ownScope is the user's own row scope on the report's connection.
func (h *Handler) ownScope(ctx context.Context, bearer, sub string, rep reportRow) (sqlexec.Scope, error) {
	conn, ok := h.Connections[rep.Connection]
	if !ok {
		return nil, errors.New("the report's connection has been removed")
	}
	company, err := h.Authz.Company(ctx, bearer, sub, conn.Company)
	if err != nil {
		return nil, err
	}
	scope, _, err := h.access(ctx, bearer, sub, company, rep.Connection, conn)
	return scope, err
}

// exec runs sql on the report's connection under scope, without needing sql_user: the report's
// editors chose the SQL.
func (h *Handler) exec(ctx context.Context, sub string, rep reportRow, scope sqlexec.Scope, sql string, args ...any) (*response, error) {
	start := time.Now()
	conn, ok := h.Connections[rep.Connection]
	if !ok {
		return nil, errors.New("the report's connection has been removed")
	}
	audit := h.Log.With("user", sub, "report", rep.ID, "connection", rep.Connection, "scope", scope)
	res, err := conn.Exec.Run(ctx, scope, sql, args...)
	if err != nil {
		audit.Info("report run failed", "error", err.Error())
		return nil, err
	}
	audit.Info("report ran", "rows", len(res.Rows), "ms", time.Since(start).Milliseconds())
	if res.Rows == nil {
		res.Rows = [][]any{}
	}
	return &response{Columns: res.Columns, Rows: res.Rows, Truncated: res.Truncated}, nil
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
	tuples := []sdk.Tuple{{ObjectType: "report", ObjectID: rep.ID, Relation: "editor", SubjectType: "user", SubjectID: rep.CreatedBy}}
	var member, relation string
	rows, _ := h.DB.Query(ctx, `SELECT member, relation FROM report_share WHERE report_id = $1`, rep.ID)
	if _, err := pgx.ForEachRow(rows, []any{&member, &relation}, func() error {
		tuples = append(tuples, shareTuple(rep.ID, member, relation))
		return nil
	}); err != nil {
		fail(w, http.StatusInternalServerError, "could not delete the report")
		return
	}
	if _, err := h.DB.Exec(ctx, `DELETE FROM report WHERE id = $1`, rep.ID); err != nil {
		fail(w, http.StatusInternalServerError, "could not delete the report")
		return
	}
	// The report is gone whatever Kiban says; a tuple left behind names an object that no longer exists.
	// Relations first, the anchor last: an anchor revoked first strands the rest (§10.4 item 15).
	err := h.Grants.Revoke(ctx, rt.company, tuples...)
	if err == nil {
		err = h.Grants.Revoke(ctx, rt.company, sdk.AnchorTuple("hareno", "report", rep.ID, rt.company))
	}
	if err != nil {
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
