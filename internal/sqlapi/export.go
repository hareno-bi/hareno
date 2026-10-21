package sqlapi

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hareno-bi/hareno/internal/authz"
	"github.com/hareno-bi/hareno/internal/sqlexec"
)

// CSV export (P2-10; ARCHITECTURE-PLAN §6.2):
//
//	GET /api/reports/{id}/tiles/{n}/csv   tile n (from 0) of the published revision
//
// A download takes data out of Hareno, so it is checked harder than a view:
//   - It needs connection#exporter as well as the report: the permission is per data source,
//     and a report or query spanning several needs it on every one (the strictest applies).
//   - Every answer comes fresh from Kiban, not the decision cache: a revoked permission stops
//     the very next download.
//   - The query runs fresh, never from the results cache, under the downloader's own row scope,
//     even where the report was shared showing the sharer's data: that is for viewing only.
//   - Rows stream to the file as they arrive, up to the connection's ExportRows.
//   - ?filters={"<column>": ["<value>", ...]} narrows it as the dashboard does (filters.go); the
//     dashboard passes only the filters that apply to the tile.
func (h *Handler) ExportTile(w http.ResponseWriter, r *http.Request) {
	bearer, sub, ok := h.caller(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	start := time.Now()
	rep, err := h.find(ctx, r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, http.StatusNotFound, "no such report")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "could not read the report")
		return
	}
	conn, ok := h.Connections[rep.Connection]
	if !ok {
		fail(w, http.StatusNotFound, "the report's connection has been removed")
		return
	}
	company, err := h.Authz.Company(ctx, bearer, sub, rep.Company)
	if errors.Is(err, authz.ErrNoCompany) {
		fail(w, http.StatusNotFound, "no such report")
		return
	} else if err != nil {
		fail(w, http.StatusServiceUnavailable, "authorization unavailable")
		return
	}
	view := authz.Item{Type: "report", ID: rep.ID, Relation: "viewer"}
	edit := authz.Item{Type: "report", ID: rep.ID, Relation: "editor"}
	export := authz.Item{Type: "connection", ID: rep.Connection, Relation: "exporter"}
	allowed, err := h.Authz.Fresh(ctx, bearer, sub, company, append(scopeItems(rep.Connection, conn), view, edit, export))
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "authorization unavailable")
		return
	}
	if !allowed[view] && !allowed[edit] {
		fail(w, http.StatusNotFound, "no such report")
		return
	}
	scope := scopeOf(allowed, rep.Connection, conn)
	audit := h.Log.With("user", sub, "report", rep.ID, "connection", rep.Connection, "scope", scope)
	if !allowed[export] {
		audit.Info("report export refused", "reason", "no exporter")
		fail(w, http.StatusForbidden, "you may not download data from this data source")
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
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 || n >= len(body.Tiles) {
		fail(w, http.StatusNotFound, "no such tile")
		return
	}
	// The viewer's filters on this tile, as the dashboard applied them (filters.go).
	var filters Filters
	if f := r.URL.Query().Get("filters"); f != "" && (json.Unmarshal([]byte(f), &filters) != nil || !filters.valid()) {
		fail(w, http.StatusBadRequest, `filters: expected {"<column>": ["<value>", ...]}`)
		return
	}
	var dates *Dates
	if d := r.URL.Query().Get("dates"); d != "" && (json.Unmarshal([]byte(d), &dates) != nil || !dates.valid()) {
		fail(w, http.StatusBadRequest, `dates: expected {"last": ...} or {"from": "YYYY-MM-DD", "to": "YYYY-MM-DD"}`)
		return
	}
	// a matrix tile downloads the rows it totals
	sql, args, _, _, err := h.tileQuery(ctx, rep.Connection, body.Tiles[n], slices.Collect(maps.Keys(filters)), filters, dates, nil)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	audit = audit.With("revision", *rep.Published, "tile", n, "filters", filters, "dates", dates)

	ex := conn.Exec
	ex.MaxRows, ex.Timeout = conn.ExportRows, conn.ExportTimeout
	out := csv.NewWriter(w)
	rows := 0
	truncated, err := ex.Each(ctx, scope, sql, args, func(cols []string) error {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", attachment(body, n))
		w.Write([]byte("\uFEFF")) // so Excel reads UTF-8
		return out.Write(cols)
	}, func(row []any) error {
		rows++
		rec := make([]string, len(row))
		for i, v := range row {
			rec[i] = cell(v)
		}
		return out.Write(rec)
	})
	if err != nil {
		audit.Info("report export failed", "error", err.Error(), "rows", rows, "ms", time.Since(start).Milliseconds())
		if w.Header().Get("Content-Disposition") != "" {
			panic(http.ErrAbortHandler) // the file has begun: break it off, so it is not taken for complete
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) || errors.Is(err, sqlexec.ErrNotQuery) {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		fail(w, http.StatusBadGateway, "query failed")
		return
	}
	if truncated {
		out.Write([]string{fmt.Sprintf("… more rows not included: a download stops at %d rows", ex.MaxRows)})
	}
	out.Flush()
	audit.Info("report exported", "rows", rows, "truncated", truncated, "ms", time.Since(start).Milliseconds())
}

// attachment names the file after the report and tile.
func attachment(d Draft, n int) string {
	name := d.Title
	if t := d.Tiles[n].Title; t != "" {
		name += " - " + t
	} else {
		name += fmt.Sprintf(" - tile %d", n+1)
	}
	name = strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": name + ".csv"}); v != "" {
		return v
	}
	return `attachment; filename="export.csv"`
}

// cell is a value as CSV text. Text that a spreadsheet would run as a formula gets a leading
// apostrophe (OWASP's CSV injection advice); numbers and dates are written as they are.
func cell(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
			return "'" + v
		}
		return v
	case json.Number:
		return string(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case time.Time:
		// ponytail: every time in UTC, and midnight UTC written as a date; the column's type would
		// tell a date from a timestamp, and a time zone setting would pick the zone.
		v = v.UTC()
		if v.Equal(v.Truncate(24 * time.Hour)) {
			return v.Format(time.DateOnly)
		}
		return v.Format("2006-01-02 15:04:05.999999")
	case bool, int, int8, int16, int32, int64, uint8, uint16, uint32, uint64:
		return fmt.Sprint(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return cell(string(b))
	}
}
