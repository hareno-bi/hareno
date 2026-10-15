// Package sqlapi serves the SQL editor's query endpoint (Phase 2 round 1, R1-2):
//
//	POST /api/sql  {"connection": "mart", "sql": "SELECT ..."}   Authorization: Bearer <user token>
//	GET  /api/connections   the connections the user may query
//
// Per request: verify the user, resolve the connection's company for them, require the
// connection's sql_user relation in Kiban, derive the user's row scope from data_scope objects in
// Kiban, run the statement under that scope (internal/sqlexec), and audit it.
package sqlapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hareno-bi/hareno/internal/authz"
	"github.com/hareno-bi/hareno/internal/model"
	"github.com/hareno-bi/hareno/internal/sqlexec"
)

// Connection is a source the SQL editor may query. Its data access mode is database-enforced
// (ARCHITECTURE-PLAN §5.3): rows are filtered by the database's policies under the scope set here.
type Connection struct {
	Company string              // Kiban company code that owns the connection, e.g. ALPHA
	Exec    sqlexec.Exec        // runs as the connection's gate login
	Scope   map[string][]string // scope attributes and their possible values, e.g. region: [North, South, West]
	ID      int64               // the connection's row in the app database, which fixes its company and mode
	// Watermark is a query returning the source's last load time, or "" if it has none (cache.go).
	Watermark string
	Refresh   time.Duration // minimum refresh interval: how often the watermark is read, or how long a result lasts without one
	// Downloads (export.go) run with their own limits instead of Exec's.
	ExportRows    int
	ExportTimeout time.Duration
}

// Handler serves POST /api/sql.
type Handler struct {
	Authz       *authz.Authz
	Connections map[string]Connection
	Log         *slog.Logger  // audit log
	DB          *pgxpool.Pool // application database: saved reports
	Grants      Granter
	People      People
	Members     Members
	Datasets    *Datasets // governed datasets (datasets.go); nil when none are configured
	Demo        Demo      // sample data for the sign-in page of dev and demo stacks; none when SQL is empty

	results results // dashboard results (cache.go)
}

// Demo is a fixed query whose result anyone may read, signed in or not: dev and demo stacks only.
type Demo struct {
	Connection string `yaml:"connection"`
	SQL        string `yaml:"sql"`
}

// DemoData serves GET /api/demo-data: the Demo query run with every row (each scope attribute "*").
func (h *Handler) DemoData(w http.ResponseWriter, r *http.Request) {
	conn, ok := h.Connections[h.Demo.Connection]
	if !ok {
		fail(w, http.StatusNotFound, "no such connection")
		return
	}
	all := sqlexec.Scope{}
	for attr := range conn.Scope {
		all[attr] = []string{"*"}
	}
	res, err := conn.Exec.Run(r.Context(), all, h.Demo.SQL)
	if err != nil {
		h.Log.Error("demo data failed", "error", err.Error())
		fail(w, http.StatusBadGateway, "query failed")
		return
	}
	if res.Rows == nil {
		res.Rows = [][]any{}
	}
	reply(w, response{Columns: res.Columns, Rows: res.Rows, Truncated: res.Truncated})
}

type request struct {
	Connection string  `json:"connection"`
	SQL        string  `json:"sql"`
	Matrix     *Matrix `json:"matrix"` // optional: total the rows as a matrix tile does (matrix.go)
	// Dataset, instead of SQL, previews a dataset tile; it needs dataset_user or sql_user (datasets.go).
	Dataset *DatasetQuery `json:"dataset"`
}

type response struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	bearer := bearerOf(r)
	sub, err := h.Authz.User(ctx, bearer)
	if err != nil {
		fail(w, http.StatusUnauthorized, "sign in required")
		return
	}
	var req request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil || (req.SQL == "") == (req.Dataset == nil) ||
		(req.Matrix != nil && !req.Matrix.valid(req.Dataset != nil)) || (req.Dataset != nil && !req.Dataset.valid()) {
		fail(w, http.StatusBadRequest, "expected {\"connection\": ..., \"sql\": ...}")
		return
	}
	conn, ok := h.Connections[req.Connection]
	if !ok {
		fail(w, http.StatusNotFound, "no such connection")
		return
	}
	company, err := h.Authz.Company(ctx, bearer, sub, conn.Company)
	if errors.Is(err, authz.ErrNoCompany) {
		fail(w, http.StatusNotFound, "no such connection") // do not reveal other companies' connections
		return
	} else if err != nil {
		fail(w, http.StatusServiceUnavailable, "authorization unavailable")
		return
	}

	scope, allowed, err := h.access(ctx, bearer, sub, company, req.Connection, conn)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "authorization unavailable")
		return
	}
	if req.Dataset != nil && !allowed && err == nil {
		allowed, err = h.mayDesign(ctx, bearer, sub, req.Connection, conn)
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "authorization unavailable")
			return
		}
	}
	audit := h.Log.With("user", sub, "company", conn.Company, "connection", req.Connection, "sql", req.SQL, "dataset", req.Dataset, "scope", scope)
	if !allowed {
		audit.Info("sql refused", "reason", "no sql_user")
		fail(w, http.StatusForbidden, "you may not query this connection with SQL")
		return
	}

	sql, _, _, err := h.tileQuery(ctx, req.Connection, Tile{SQL: req.SQL, Dataset: req.Dataset}, nil, nil, req.Matrix)
	if err != nil {
		audit.Info("sql failed", "error", err.Error())
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := conn.Exec.Run(ctx, scope, sql)
	if err != nil {
		audit.Info("sql failed", "error", err.Error(), "ms", time.Since(start).Milliseconds())
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) || errors.Is(err, sqlexec.ErrNotQuery) {
			fail(w, http.StatusBadRequest, err.Error()) // the user's own statement: show why
			return
		}
		fail(w, http.StatusBadGateway, "query failed")
		return
	}
	audit.Info("sql ran", "rows", len(res.Rows), "truncated", res.Truncated, "ms", time.Since(start).Milliseconds())
	if res.Rows == nil {
		res.Rows = [][]any{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		response
		Display map[string]model.Display `json:"display,omitempty"`
	}{response{Columns: res.Columns, Rows: res.Rows, Truncated: res.Truncated}, h.display(req.Dataset)})
}

// List serves GET /api/connections: the connections the user may build dashboards on (sql_user
// or dataset_user), and those they may query with SQL, by name.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bearer := bearerOf(r)
	sub, err := h.Authz.User(ctx, bearer)
	if err != nil {
		fail(w, http.StatusUnauthorized, "sign in required")
		return
	}
	names, sql := []string{}, []string{}
	for name, c := range h.Connections {
		company, err := h.Authz.Company(ctx, bearer, sub, c.Company)
		if errors.Is(err, authz.ErrNoCompany) {
			continue
		}
		use := authz.Item{Type: "connection", ID: name, Relation: "sql_user"}
		design := authz.Item{Type: "connection", ID: name, Relation: "dataset_user"}
		var ok map[authz.Item]bool
		if err == nil {
			ok, err = h.Authz.Allowed(ctx, bearer, sub, company, []authz.Item{use, design})
		}
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "authorization unavailable")
			return
		}
		if ok[use] || ok[design] {
			names = append(names, name)
		}
		if ok[use] {
			sql = append(sql, name)
		}
	}
	slices.Sort(names)
	slices.Sort(sql)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]string{"connections": names, "sql": sql})
}

// access asks Kiban, in one batch, whether the user may use SQL on the connection and which
// scope values they hold (data_scope "<connection>.<attribute>.<value>", or ".*" for all).
func (h *Handler) access(ctx context.Context, bearer, sub, company, name string, c Connection) (sqlexec.Scope, bool, error) {
	use := authz.Item{Type: "connection", ID: name, Relation: "sql_user"}
	ok, err := h.Authz.Allowed(ctx, bearer, sub, company, append(scopeItems(name, c), use))
	if err != nil {
		return nil, false, err
	}
	return scopeOf(ok, name, c), ok[use], nil
}

// scopeItems are the questions that decide a user's row scope on connection name.
func scopeItems(name string, c Connection) []authz.Item {
	var items []authz.Item
	for attr, vals := range c.Scope {
		for _, v := range append(slices.Clone(vals), "*") {
			items = append(items, authz.Item{Type: "data_scope", ID: name + "." + attr + "." + v, Relation: "viewer"})
		}
	}
	return items
}

// scopeOf is the row scope Kiban's answers to scopeItems give.
func scopeOf(ok map[authz.Item]bool, name string, c Connection) sqlexec.Scope {
	scope := sqlexec.Scope{}
	for attr, vals := range c.Scope {
		if ok[authz.Item{Type: "data_scope", ID: name + "." + attr + ".*", Relation: "viewer"}] {
			scope[attr] = []string{"*"}
			continue
		}
		scope[attr] = []string{}
		for _, v := range vals {
			if ok[authz.Item{Type: "data_scope", ID: name + "." + attr + "." + v, Relation: "viewer"}] {
				scope[attr] = append(scope[attr], v)
			}
		}
	}
	return scope
}

func bearerOf(r *http.Request) string {
	b, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return b
}

func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
