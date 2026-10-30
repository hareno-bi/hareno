package sqlapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hareno-bi/hareno/internal/sqlexec"
)

// The sample report, for dev and demo stacks only, as the sign-in page's sample data (Demo): one
// published dashboard, chosen by its title in the configuration, that anyone may view without
// signing in, showing every row of its data. Read only: no editing, sharing or downloads.
//
//	GET  /api/sample-report       its published revision, as GET /api/reports/{id} gives it
//	POST /api/sample-report/run   a page of it, as POST /api/reports/{id}/run
func (h *Handler) sample(w http.ResponseWriter, r *http.Request) (reportRow, bool) {
	rep, err := scanReport(h.DB.QueryRow(r.Context(), `SELECT `+reportCols+` FROM report r JOIN connection c ON c.id = r.connection_id
		WHERE r.published IS NOT NULL AND (SELECT body->>'title' FROM report_revision WHERE report_id = r.id AND revision = r.published) = $1
		ORDER BY r.updated_at DESC LIMIT 1`, h.SampleReport))
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, http.StatusNotFound, "the sample report has not been published")
		return rep, false
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "could not read the report")
		return rep, false
	}
	return rep, true
}

func (h *Handler) SampleReportView(w http.ResponseWriter, r *http.Request) {
	rep, ok := h.sample(w, r)
	if !ok {
		return
	}
	var body Draft
	var at time.Time
	if err := h.DB.QueryRow(r.Context(), `SELECT body, published_at FROM report_revision WHERE report_id = $1 AND revision = $2`,
		rep.ID, *rep.Published).Scan(&body, &at); err != nil {
		fail(w, http.StatusInternalServerError, "could not read the report")
		return
	}
	reply(w, map[string]any{"published": map[string]any{"revision": *rep.Published, "publishedAt": at, "body": body}, "canEdit": false, "canExport": false})
}

func (h *Handler) SampleReportRun(w http.ResponseWriter, r *http.Request) {
	rep, ok := h.sample(w, r)
	if !ok {
		return
	}
	req, body, ok := h.runRequest(w, r, rep)
	if !ok {
		return
	}
	conn, ok := h.Connections[rep.Connection]
	if !ok {
		fail(w, http.StatusNotFound, "the report's connection has been removed")
		return
	}
	all := sqlexec.Scope{}
	for attr := range conn.Scope {
		all[attr] = []string{"*"}
	}
	h.run(w, r.Context(), "sample", rep, body, all, "sample", req)
}
